package proxy

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const MaxTestEntries = 20

type TestResult struct {
	Line      int    `json:"line"`
	Label     string `json:"label"`
	OK        bool   `json:"ok"`
	TunnelOK  bool   `json:"tunnel_ok"`
	Status    int    `json:"status"`
	LatencyMS int64  `json:"latency_ms"`
	Detail    string `json:"detail"`
	// Hint is a short, actionable next step derived from the failure kind.
	Hint string `json:"hint,omitempty"`
	// ProviderMessage is a sanitized excerpt of the proxy provider's own reply.
	ProviderMessage string `json:"provider_message,omitempty"`
	// ProxyAuthenticate is a sanitized Proxy-Authenticate value, when offered.
	ProxyAuthenticate string `json:"proxy_authenticate,omitempty"`
	// Authenticated reports whether the tested line carried a username/password.
	Authenticated bool `json:"authenticated"`
}

// hintFor turns a failure into one plain-language next step. It never guesses
// about a specific provider beyond what its own status code actually means.
func hintFor(entry Entry, status int, snippet string, networkErr bool) string {
	lower := strings.ToLower(snippet)
	switch {
	case status == 407:
		if !entry.HasAuth() {
			return "This line has no username:password. Use host:port:user:pass or allow your IP with the provider."
		}
		return "The proxy rejected the credentials. Re-copy username and password for this exact endpoint (a rotating/backbone username differs from a direct one)."
	case status == 402:
		if !entry.HasAuth() {
			return "The proxy answered 402 for a line without credentials. Add user:pass (host:port:user:pass) or authorize this machine's IP with the provider."
		}
		if strings.Contains(lower, "authenticat") || strings.Contains(lower, "credential") || strings.Contains(lower, "password") || strings.Contains(lower, "username") {
			return "The proxy answered 402 and its own message points at authentication. Re-copy the username/password that belong to this exact endpoint."
		}
		return "402 is a provider account/entitlement answer: expired plan, exhausted bandwidth, or credentials that do not match this endpoint (e.g. a direct-connection login used against the rotating endpoint)."
	case status == 403:
		return "The proxy refused this destination. Some providers block specific sites on some proxy types; test another proxy or another proxy plan."
	case status == 429:
		return "Provider rate/concurrency limit reached. Wait, or use fewer parallel browsers through this proxy."
	case status >= 500:
		return "The provider had trouble reaching the destination. Retry or pick another proxy from the list."
	case status != 0:
		return "The proxy answered HTTP " + strconv.Itoa(status) + ". Test the same line with curl or a browser to confirm the provider side."
	case networkErr:
		return "No HTTP answer at all: wrong host/port, blocked local network, or the proxy is offline."
	default:
		return ""
	}
}

// TestList never saves settings, rotates worker proxies or opens an account.
// Only an HTTPS HEAD request to the supplied fixed public endpoint is made.
func TestList(ctx context.Context, raw, endpoint string) ([]TestResult, error) {
	if len(raw) > 64<<10 {
		return nil, fmt.Errorf("proxy list is too large (maximum 64 KiB)")
	}
	lines := strings.Split(strings.ReplaceAll(raw, "\r", ""), "\n")
	results := make([]TestResult, 0)
	entries := make([]Entry, 0)
	for index, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if len(results) >= MaxTestEntries {
			return nil, fmt.Errorf("test at most %d proxy lines at a time", MaxTestEntries)
		}
		entry, err := ParseLine(line)
		result := TestResult{Line: index + 1}
		if err != nil {
			result.Detail = err.Error()
		} else {
			result.Label = entry.Label()
		}
		results = append(results, result)
		entries = append(entries, entry)
	}
	if len(results) == 0 {
		return nil, fmt.Errorf("enter at least one proxy to test")
	}
	jobs := make(chan int)
	var wg sync.WaitGroup
	for worker := 0; worker < 4; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				if results[i].Detail != "" {
					continue
				}
				c, cancel := context.WithTimeout(ctx, 12*time.Second)
				tested := TestEndpoint(c, entries[i], endpoint)
				cancel()
				tested.Line = results[i].Line
				results[i] = tested
			}
		}()
	}
	for i := range results {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	return results, nil
}
func TestEndpoint(ctx context.Context, entry Entry, endpoint string) TestResult {
	return testEndpoint(ctx, entry, endpoint, nil)
}

// Only local unit tests provide additional roots; dashboard uses system trust.
func testEndpoint(ctx context.Context, entry Entry, endpoint string, roots *x509.CertPool) (result TestResult) {
	result.Label = entry.Label()
	result.Authenticated = entry.HasAuth()
	start := time.Now()
	defer func() { result.LatencyMS = time.Since(start).Milliseconds() }()
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" {
		result.Detail = "invalid HTTPS test endpoint"
		return
	}
	port := u.Port()
	if port == "" {
		port = "443"
	}
	conn, err := entry.DialTunnel(ctx, net.JoinHostPort(u.Hostname(), port))
	if err != nil {
		result.Detail = err.Error()
		status := 0
		networkErr := false
		var tunnelErr *TunnelError
		if errors.As(err, &tunnelErr) {
			status = tunnelErr.Status
			result.ProviderMessage = tunnelErr.ProviderMessage
			result.ProxyAuthenticate = tunnelErr.ProxyAuth
			networkErr = tunnelErr.Kind == "network"
		}
		result.Status = status
		result.Hint = hintFor(entry, status, result.ProviderMessage, networkErr)
		return
	}
	defer conn.Close()
	secure := tls.Client(conn, &tls.Config{ServerName: u.Hostname(), MinVersion: tls.VersionTLS12, RootCAs: roots})
	if err := secure.HandshakeContext(ctx); err != nil {
		result.Detail = "proxy tunnel opened, but destination TLS verification/handshake failed"
		result.Hint = "The tunnel opened, so the credentials work. The HTTPS handshake failed: re-test in a moment, or the destination may be intercepted/blocked for this proxy."
		return
	}
	result.TunnelOK = true
	deadline := time.Now().Add(12 * time.Second)
	if d, ok := ctx.Deadline(); ok {
		deadline = d
	}
	secure.SetDeadline(deadline)
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, endpoint, nil)
	if err != nil {
		result.Detail = "invalid test request"
		return
	}
	req.Header.Set("User-Agent", "ChitChat-Proxy-Test/1.0")
	req.Close = true
	if err := req.Write(secure); err != nil {
		result.Detail = "tunnel/TLS succeeded; test request failed"
		return
	}
	response, err := http.ReadResponse(bufio.NewReader(io.LimitReader(secure, 64<<10)), req)
	if err != nil {
		result.Detail = "tunnel/TLS succeeded; destination response failed or timed out"
		return
	}
	response.Body.Close()
	result.Status = response.StatusCode
	result.OK = response.StatusCode >= 200 && response.StatusCode < 400
	if result.OK {
		result.Detail = "PASS: proxy authentication, HTTPS tunnel and destination response succeeded"
	} else {
		result.Detail = fmt.Sprintf("proxy tunnel/TLS works, but destination returned HTTP %d (site/provider access restriction)", response.StatusCode)
		result.Hint = hintFor(Entry{}, response.StatusCode, "", false)
	}
	return
}
