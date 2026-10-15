package proxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// testRejectingProxy always answers CONNECT with the given rejection.
func testRejectingProxy(t *testing.T, status int, authenticate string, body string) Entry {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if authenticate != "" {
			w.Header().Set("Proxy-Authenticate", authenticate)
		}
		w.WriteHeader(status)
		if body != "" {
			w.Write([]byte(body))
		}
	}))
	t.Cleanup(server.Close)
	u, _ := url.Parse(server.URL)
	return Entry{Scheme: "http", Host: u.Hostname(), Port: u.Port(), Username: "user-abc123", Password: "s3cretpw"}
}

func testRejectionResult(t *testing.T, entry Entry) TestResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return testEndpoint(ctx, entry, "https://app.example.invalid/", nil)
}

func TestProviderRejectionDiagnostics(t *testing.T) {
	entry := testRejectingProxy(t, 402, `Basic realm="webshare"`,
		"<html><body>402 Payment Required\n   user: user-abc123 s3cretpw quota exhausted</body></html>")
	result := testRejectionResult(t, entry)
	if result.OK || result.TunnelOK || result.Status != 402 {
		t.Fatalf("402 must be reported as a failure with its status: %+v", result)
	}
	if !result.Authenticated {
		t.Fatal("authenticated line not reported")
	}
	if !strings.Contains(result.ProviderMessage, "quota exhausted") {
		t.Fatalf("provider message not captured: %q", result.ProviderMessage)
	}
	if strings.Contains(result.ProviderMessage, "<") || strings.Contains(result.ProviderMessage, "\n") {
		t.Fatalf("provider message not sanitized to one line: %q", result.ProviderMessage)
	}
	if !strings.Contains(result.ProxyAuthenticate, "webshare") {
		t.Fatalf("Proxy-Authenticate not captured: %q", result.ProxyAuthenticate)
	}
	if result.Hint == "" || !strings.Contains(result.Hint, "402") {
		t.Fatalf("missing 402 hint: %+v", result)
	}
	for _, field := range []string{result.Label, result.Detail, result.ProviderMessage, result.ProxyAuthenticate, result.Hint} {
		if strings.Contains(field, "user-abc123") || strings.Contains(field, "s3cretpw") {
			t.Fatalf("credentials leaked into %q", field)
		}
	}
}

func TestHintsForAuthenticationAndNetworkFailures(t *testing.T) {
	withAuth := testRejectingProxy(t, 407, `Basic realm="proxy"`, "")
	authenticated := testRejectionResult(t, withAuth)
	if authenticated.Hint == "" || !strings.Contains(authenticated.Hint, "credentials") {
		t.Fatalf("407 hint must mention credentials: %+v", authenticated)
	}
	unauthenticated := Entry{Scheme: "http", Host: withAuth.Host, Port: withAuth.Port}
	result := testRejectionResult(t, unauthenticated)
	if result.Authenticated {
		t.Fatal("line without credentials reported as authenticated")
	}
	if !strings.Contains(result.Hint, "user:pass") {
		t.Fatalf("missing credentials hint: %+v", result)
	}
	offline := Entry{Scheme: "http", Host: "127.0.0.1", Port: "1"}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	network := testEndpoint(ctx, offline, "https://app.example.invalid/", nil)
	if network.Status != 0 || !strings.Contains(network.Hint, "offline") {
		t.Fatalf("network failure hint missing: %+v", network)
	}
}

func TestSanitizeProviderText(t *testing.T) {
	entry := Entry{Username: "u", Password: "longpassword"}
	raw := "a\x00b\tc  d\n\n<tag>x</tag>longpassword " + strings.Repeat("y", 400)
	cleaned := sanitizeProviderText(raw, entry, providerMessageLimit)
	if strings.ContainsAny(cleaned, "\x00\t\n<") {
		t.Fatalf("control characters or tags survived: %q", cleaned)
	}
	if strings.Contains(cleaned, "longpassword") {
		t.Fatalf("password echoed: %q", cleaned)
	}
	if !strings.HasSuffix(cleaned, "...") || len([]rune(cleaned)) > providerMessageLimit+3 {
		t.Fatalf("not truncated as expected: %q", cleaned)
	}
}
