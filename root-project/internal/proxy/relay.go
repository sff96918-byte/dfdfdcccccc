package proxy

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Relay bridges Chrome's unauthenticated localhost proxy to one fixed upstream.
// It is bound ONLY to loopback, lives for the browser lifetime, and never dials
// a destination directly. Credentials stay in this process, not Chrome flags.
type Relay struct {
	server      *http.Server
	listener    net.Listener
	transport   *http.Transport
	mu          sync.Mutex
	closed      bool
	connections map[net.Conn]struct{}
	once        sync.Once
	cancel      context.CancelFunc
	Entry       Entry
}

func StartRelay(parent context.Context, upstream Entry) (*Relay, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("cannot start local proxy relay")
	}
	ctx, cancel := context.WithCancel(parent)
	r := &Relay{listener: listener, connections: make(map[net.Conn]struct{}), cancel: cancel}
	address := listener.Addr().(*net.TCPAddr)
	r.Entry = Entry{Scheme: "http", Host: "127.0.0.1", Port: fmt.Sprint(address.Port)}
	r.transport = &http.Transport{DialContext: (&net.Dialer{Timeout: 12 * time.Second}).DialContext, DisableKeepAlives: true, ResponseHeaderTimeout: 15 * time.Second, TLSHandshakeTimeout: 12 * time.Second}
	if upstream.SchemeOrDefault() == "http" {
		// Absolute-form HTTP forwarding; HTTPS uses the tunnel branch below.
		r.transport.Proxy = http.ProxyURL(upstream.TransportURL())
	} else {
		r.transport.DialContext = func(c context.Context, network, target string) (net.Conn, error) {
			return upstream.DialTunnel(c, target)
		}
	}
	r.server = &http.Server{ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 64 << 10, ErrorLog: log.New(io.Discard, "", 0)}
	r.server.Handler = http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodConnect {
			if req.URL.Scheme != "http" || req.URL.Host == "" {
				http.Error(w, "absolute HTTP proxy URL required", 400)
				return
			}
			forward := req.Clone(req.Context())
			forward.RequestURI = ""
			stripHopHeaders(forward.Header)
			response, err := r.transport.RoundTrip(forward)
			if err != nil {
				http.Error(w, "upstream proxy HTTP forwarding failed", 502)
				return
			}
			defer response.Body.Close()
			stripHopHeaders(response.Header)
			for k, values := range response.Header {
				for _, v := range values {
					w.Header().Add(k, v)
				}
			}
			w.WriteHeader(response.StatusCode)
			io.Copy(w, response.Body)
			return
		}
		dialCtx, done := context.WithTimeout(req.Context(), 12*time.Second)
		remote, err := upstream.DialTunnel(dialCtx, req.Host)
		done()
		if err != nil {
			http.Error(w, err.Error(), 502)
			return
		}
		hijacker, yes := w.(http.Hijacker)
		if !yes {
			remote.Close()
			http.Error(w, "tunnelling unavailable", 500)
			return
		}
		client, buffer, err := hijacker.Hijack()
		if err != nil {
			remote.Close()
			return
		}
		r.mu.Lock()
		if r.closed {
			r.mu.Unlock()
			remote.Close()
			client.Close()
			return
		}
		r.connections[remote] = struct{}{}
		r.connections[client] = struct{}{}
		r.mu.Unlock()
		defer func() {
			remote.Close()
			client.Close()
			r.mu.Lock()
			delete(r.connections, remote)
			delete(r.connections, client)
			r.mu.Unlock()
		}()
		if _, err := buffer.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
			return
		}
		if err := buffer.Flush(); err != nil {
			return
		}
		finished := make(chan struct{})
		go func() { io.Copy(remote, buffer); remote.Close(); close(finished) }()
		io.Copy(client, remote)
		client.Close()
		remote.Close()
		<-finished
	})
	go func() { _ = r.server.Serve(listener) }()
	go func() { <-ctx.Done(); r.Close() }()
	return r, nil
}
func (r *Relay) Close() {
	r.once.Do(func() {
		r.cancel()
		r.mu.Lock()
		r.closed = true
		for c := range r.connections {
			c.Close()
		}
		r.mu.Unlock()
		r.listener.Close() // Also closes immediately if Serve has not registered it yet.
		r.server.Close()
		r.transport.CloseIdleConnections()
	})
}
func stripHopHeaders(header http.Header) {
	// Remove both standard and Connection-nominated hop-by-hop fields.
	for _, name := range strings.Split(header.Get("Connection"), ",") {
		header.Del(strings.TrimSpace(name))
	}
	for _, name := range []string{"Connection", "Proxy-Connection", "Proxy-Authorization", "Proxy-Authenticate", "Keep-Alive", "TE", "Trailer", "Transfer-Encoding", "Upgrade"} {
		header.Del(name)
	}
}
