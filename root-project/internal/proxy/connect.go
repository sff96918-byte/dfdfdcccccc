package proxy

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// TunnelError explains, in credential-free form, why a proxy tunnel failed.
// Status/ProxyAuth/ProviderMessage come from the proxy's own reply and are the
// evidence a support conversation needs when a provider rejects a connection.
type TunnelError struct {
	Kind            string // http, socks, network, protocol, input, canceled
	Status          int    // HTTP status the proxy answered with (0 if unknown)
	ProxyAuth       string // sanitized Proxy-Authenticate value, e.g. Basic realm="x"
	ProviderMessage string // sanitized excerpt of the proxy's own response body
	Detail          string // human-readable, credential-free reason
}

func (e *TunnelError) Error() string { return e.Detail }

const providerMessageLimit = 200

var htmlTagPattern = regexp.MustCompile(`<[^<>]{0,200}>`)

// sanitizeProviderText keeps a short, single-line, credential-free excerpt of
// whatever the proxy sent back. Control characters are dropped, HTML tags are
// stripped, and the configured username/password (or their Basic token) are
// redacted so a failure screenshot can never leak them.
func sanitizeProviderText(raw string, entry Entry, limit int) string {
	text := htmlTagPattern.ReplaceAllString(raw, " ")
	var builder strings.Builder
	space := false
	for _, r := range text {
		if r < 0x20 || r == 0x7f {
			r = ' '
		}
		if unicode.IsSpace(r) {
			if space {
				continue
			}
			space = true
			builder.WriteByte(' ')
			continue
		}
		space = false
		builder.WriteRune(r)
	}
	cleaned := strings.TrimSpace(builder.String())
	secrets := []string{entry.Username, entry.Password}
	if entry.HasAuth() {
		secrets = append(secrets, base64.StdEncoding.EncodeToString([]byte(entry.Username+":"+entry.Password)))
	}
	for _, secret := range secrets {
		if len(secret) >= 3 {
			cleaned = strings.ReplaceAll(cleaned, secret, "***")
		}
	}
	cleaned = strings.TrimSpace(cleaned)
	if runes := []rune(cleaned); len(runes) > limit {
		cleaned = strings.TrimSpace(string(runes[:limit])) + "..."
	}
	return cleaned
}

// readProviderMessage reads a bounded excerpt of the proxy's rejection body.
// It never blocks longer than 1.2s and never fails the whole check.
func readProviderMessage(conn net.Conn, reader *bufio.Reader, entry Entry) string {
	if err := conn.SetReadDeadline(time.Now().Add(1200 * time.Millisecond)); err != nil {
		return ""
	}
	buffer := make([]byte, 512)
	n, _ := io.ReadFull(reader, buffer)
	if n <= 0 {
		return ""
	}
	return sanitizeProviderText(string(buffer[:n]), entry, providerMessageLimit)
}

// bufferedConn preserves bytes already received after a CONNECT response.
type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) { return c.reader.Read(p) }

// Cap handshake headers without capping the subsequent tunnel byte stream.
type handshakeReader struct {
	reader    io.Reader
	remaining int
	bounded   bool
}

func (r *handshakeReader) Read(p []byte) (int, error) {
	if r.bounded {
		if r.remaining <= 0 {
			return 0, io.ErrUnexpectedEOF
		}
		if len(p) > r.remaining {
			p = p[:r.remaining]
		}
	}
	n, err := r.reader.Read(p)
	if r.bounded {
		r.remaining -= n
	}
	return n, err
}

// DialTunnel connects ONLY through this proxy; target DNS is resolved upstream.
// Returned errors never include credentials or raw provider response bodies.
func (e Entry) DialTunnel(ctx context.Context, target string) (net.Conn, error) {
	host, portText, err := net.SplitHostPort(target)
	if err != nil || host == "" {
		return nil, &TunnelError{Kind: "input", Detail: "invalid tunnel target"}
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return nil, &TunnelError{Kind: "input", Detail: "invalid tunnel target port"}
	}
	conn, err := (&net.Dialer{Timeout: 12 * time.Second}).DialContext(ctx, "tcp", e.Address())
	if err != nil {
		return nil, &TunnelError{Kind: "network", Detail: "proxy TCP connection failed (offline, DNS, firewall or timeout)"}
	}
	rawConn := conn
	ok := false
	defer func() {
		if !ok {
			rawConn.Close()
		}
	}()
	deadline := time.Now().Add(12 * time.Second)
	if d, yes := ctx.Deadline(); yes && d.Before(deadline) {
		deadline = d
	}
	conn.SetDeadline(deadline)
	stop := context.AfterFunc(ctx, func() { rawConn.Close() })
	defer stop()
	switch e.SchemeOrDefault() {
	case "http":
		req := &http.Request{Method: http.MethodConnect, Host: target, Header: make(http.Header)}
		// Request.Write needs a URL even for CONNECT.
		req.URL = &url.URL{Host: target}
		if e.HasAuth() {
			req.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(e.Username+":"+e.Password)))
		}
		if err := req.Write(conn); err != nil {
			return nil, fmt.Errorf("HTTP proxy CONNECT write failed")
		}
		limited := &handshakeReader{reader: conn, remaining: 64 << 10, bounded: true}
		reader := bufio.NewReader(limited)
		response, err := http.ReadResponse(reader, req)
		if err != nil {
			return nil, fmt.Errorf("HTTP proxy did not return a valid CONNECT response (protocol or timeout)")
		}
		if response.StatusCode != 200 {
			proxyAuth := sanitizeProviderText(response.Header.Get("Proxy-Authenticate"), e, 80)
			detail := fmt.Sprintf("HTTP proxy rejected CONNECT (%d): check provider permissions, IP allowlist and target access", response.StatusCode)
			if response.StatusCode == 407 {
				detail = "HTTP proxy authentication failed (407): check username/password"
			}
			return nil, &TunnelError{
				Kind:            "http",
				Status:          response.StatusCode,
				ProxyAuth:       proxyAuth,
				ProviderMessage: readProviderMessage(conn, reader, e),
				Detail:          detail,
			}
		}
		limited.bounded = false
		conn = &bufferedConn{Conn: conn, reader: reader}
	case "socks5":
		methods := []byte{5, 1, 0}
		if e.HasAuth() {
			methods = []byte{5, 1, 2}
		}
		if _, err := conn.Write(methods); err != nil {
			return nil, fmt.Errorf("SOCKS5 greeting failed")
		}
		reply := make([]byte, 2)
		if _, err := io.ReadFull(conn, reply); err != nil || reply[0] != 5 {
			return nil, fmt.Errorf("invalid SOCKS5 response (protocol or timeout)")
		}
		if reply[1] == 2 && e.HasAuth() {
			auth := append([]byte{1, byte(len(e.Username))}, []byte(e.Username)...)
			auth = append(auth, byte(len(e.Password)))
			auth = append(auth, []byte(e.Password)...)
			if _, err := conn.Write(auth); err != nil {
				return nil, fmt.Errorf("SOCKS5 authentication write failed")
			}
			if _, err := io.ReadFull(conn, reply); err != nil || reply[0] != 1 || reply[1] != 0 {
				return nil, &TunnelError{Kind: "socks", Detail: "SOCKS5 authentication failed: check username/password"}
			}
		} else if reply[1] != 0 || e.HasAuth() {
			return nil, fmt.Errorf("SOCKS5 proxy rejected the configured authentication method")
		}
		request := []byte{5, 1, 0}
		if ip := net.ParseIP(host); ip != nil {
			if v4 := ip.To4(); v4 != nil {
				request = append(request, 1)
				request = append(request, v4...)
			} else {
				request = append(request, 4)
				request = append(request, ip.To16()...)
			}
		} else {
			if len(host) > 255 {
				return nil, fmt.Errorf("SOCKS5 target hostname too long")
			}
			request = append(request, 3, byte(len(host)))
			request = append(request, []byte(host)...)
		}
		request = binary.BigEndian.AppendUint16(request, uint16(port))
		if _, err := conn.Write(request); err != nil {
			return nil, fmt.Errorf("SOCKS5 CONNECT write failed")
		}
		header := make([]byte, 4)
		if _, err := io.ReadFull(conn, header); err != nil || header[0] != 5 || header[2] != 0 {
			return nil, fmt.Errorf("invalid SOCKS5 CONNECT response")
		}
		if header[1] != 0 {
			return nil, &TunnelError{Kind: "socks", ProviderMessage: fmt.Sprintf("SOCKS5 reply code %d", header[1]), Detail: fmt.Sprintf("SOCKS5 CONNECT rejected (code %d): target/provider unavailable", header[1])}
		}
		length := 0
		switch header[3] {
		case 1:
			length = 4
		case 4:
			length = 16
		case 3:
			b := make([]byte, 1)
			if _, err := io.ReadFull(conn, b); err != nil {
				return nil, fmt.Errorf("incomplete SOCKS5 response")
			}
			length = int(b[0])
		default:
			return nil, fmt.Errorf("invalid SOCKS5 address type")
		}
		if _, err := io.CopyN(io.Discard, conn, int64(length+2)); err != nil {
			return nil, fmt.Errorf("incomplete SOCKS5 response")
		}
	default:
		return nil, fmt.Errorf("unsupported proxy protocol")
	}
	if ctx.Err() != nil {
		return nil, &TunnelError{Kind: "canceled", Detail: "proxy test cancelled or timed out"}
	}
	conn.SetDeadline(time.Time{})
	ok = true
	return conn, nil
}
