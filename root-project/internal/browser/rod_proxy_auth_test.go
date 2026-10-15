//go:build browser_live

package browser

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	internalproxy "chitchatbot/internal/proxy"
)

func TestRodBackendProxyAuth(t *testing.T) {
	t.Parallel()

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprint(w, "<html><body>proxy-ok</body></html>")
	}))
	defer target.Close()

	targetURL, err := url.Parse(target.URL)
	if err != nil {
		t.Fatalf("parse target url: %v", err)
	}

	var targetHits atomic.Int32
	proxyServer := httptest.NewServer(newAuthenticatedProxy(targetURL.Host, &targetHits))
	defer proxyServer.Close()

	proxyURL, err := url.Parse(proxyServer.URL)
	if err != nil {
		t.Fatalf("parse proxy url: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	backend := &RodBackend{}
	err = backend.Start(ctx, Options{
		StartURL:    target.URL,
		UserDataDir: t.TempDir(),
		Headless:    true,
		Proxy: &internalproxy.Entry{
			Scheme:   proxyURL.Scheme,
			Host:     proxyURL.Hostname(),
			Port:     proxyURL.Port(),
			Username: "u",
			Password: "p",
		},
	})
	if err != nil {
		t.Fatalf("start rod backend with proxy auth: %v", err)
	}
	defer func() {
		if closeErr := backend.Close(); closeErr != nil {
			t.Fatalf("close rod backend: %v", closeErr)
		}
	}()

	response, err := backend.Fetch(ctx, FetchRequest{
		Method: http.MethodGet,
		URL:    target.URL,
	})
	if err != nil {
		t.Fatalf("fetch through rod backend: %v", err)
	}
	if !response.OK {
		t.Fatalf("expected successful response, got status=%d text=%q", response.Status, response.Text)
	}
	if !strings.Contains(response.Text, "proxy-ok") {
		t.Fatalf("expected proxy-ok body, got %q", response.Text)
	}
	if targetHits.Load() == 0 {
		t.Fatal("expected at least one target request to pass through the proxy")
	}
}

func newAuthenticatedProxy(targetHost string, targetHits *atomic.Int32) *httputil.ReverseProxy {
	return &httputil.ReverseProxy{
		Director: func(r *http.Request) {
			if r.Header.Get("Proxy-Authorization") != "Basic dTpw" {
				r.Header.Set("X-Proxy-Failed", "407")
				return
			}

			if r.URL.Host == targetHost {
				targetHits.Add(1)
			}
		},
		Transport: proxyTransport{RoundTripper: http.DefaultTransport},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			if err != nil && err.Error() == "407" {
				w.Header().Add("Proxy-Authenticate", `Basic realm="Proxy Authorization"`)
				w.WriteHeader(http.StatusProxyAuthRequired)
				return
			}
			w.WriteHeader(http.StatusBadGateway)
		},
	}
}

type proxyTransport struct {
	http.RoundTripper
}

func (t proxyTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if failed := r.Header.Get("X-Proxy-Failed"); failed != "" {
		return nil, fmt.Errorf("%s", failed)
	}
	return t.RoundTripper.RoundTrip(r)
}
