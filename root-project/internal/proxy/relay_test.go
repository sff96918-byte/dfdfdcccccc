package proxy

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// All network endpoints in these tests are local synthetic servers.
func testHTTPProxy(t *testing.T, authenticated bool, rejection int) Entry {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rejection != 0 {
			w.WriteHeader(rejection)
			return
		}
		if authenticated && r.Header.Get("Proxy-Authorization") != "Basic "+base64.StdEncoding.EncodeToString([]byte("u:p")) {
			w.WriteHeader(407)
			return
		}
		if r.Method != http.MethodConnect {
			clone := r.Clone(r.Context())
			clone.RequestURI = ""
			clone.Header.Del("Proxy-Authorization")
			// Explicit no-environment-proxy transport for local tests only.
			transport := &http.Transport{}
			defer transport.CloseIdleConnections()
			response, err := transport.RoundTrip(clone)
			if err != nil {
				http.Error(w, "test forward failed", 502)
				return
			}
			defer response.Body.Close()
			w.WriteHeader(response.StatusCode)
			io.Copy(w, response.Body)
			return
		}
		remote, err := net.Dial("tcp", r.Host)
		if err != nil {
			http.Error(w, "local target failed", 502)
			return
		}
		client, buffer, err := w.(http.Hijacker).Hijack()
		if err != nil {
			remote.Close()
			return
		}
		defer client.Close()
		defer remote.Close()
		buffer.WriteString("HTTP/1.1 200 OK\r\n\r\n")
		buffer.Flush()
		done := make(chan struct{})
		go func() { io.Copy(remote, buffer); remote.Close(); close(done) }()
		io.Copy(client, remote)
		client.Close()
		remote.Close()
		<-done
	}))
	t.Cleanup(server.Close)
	u, _ := url.Parse(server.URL)
	entry := Entry{Scheme: "http", Host: u.Hostname(), Port: u.Port()}
	if authenticated {
		entry.Username = "u"
		entry.Password = "p"
	}
	return entry
}

func testSOCKSProxy(t *testing.T, auth bool, reject bool, forcedTarget string) (Entry, <-chan string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	connections := make(map[net.Conn]bool)
	var mu sync.Mutex
	var wg sync.WaitGroup
	addresses := make(chan string, 32)
	t.Cleanup(func() {
		listener.Close()
		mu.Lock()
		for c := range connections {
			c.Close()
		}
		mu.Unlock()
		wg.Wait()
	})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			c, err := listener.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			connections[c] = true
			mu.Unlock()
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer func() { c.Close(); mu.Lock(); delete(connections, c); mu.Unlock() }()
				c.SetDeadline(time.Now().Add(5 * time.Second))
				header := make([]byte, 2)
				if _, err := io.ReadFull(c, header); err != nil {
					return
				}
				methods := make([]byte, int(header[1]))
				if _, err := io.ReadFull(c, methods); err != nil {
					return
				}
				method := byte(0)
				if auth {
					method = 2
				}
				c.Write([]byte{5, method})
				if auth {
					if _, err := io.ReadFull(c, header); err != nil {
						return
					}
					user := make([]byte, int(header[1]))
					io.ReadFull(c, user)
					size := make([]byte, 1)
					io.ReadFull(c, size)
					pass := make([]byte, int(size[0]))
					io.ReadFull(c, pass)
					if reject || string(user) != "u" || string(pass) != "p" {
						c.Write([]byte{1, 1})
						return
					}
					c.Write([]byte{1, 0})
				}
				request := make([]byte, 4)
				if _, err := io.ReadFull(c, request); err != nil {
					return
				}
				var host string
				switch request[3] {
				case 1:
					b := make([]byte, 4)
					io.ReadFull(c, b)
					host = net.IP(b).String()
				case 4:
					b := make([]byte, 16)
					io.ReadFull(c, b)
					host = net.IP(b).String()
				case 3:
					size := make([]byte, 1)
					io.ReadFull(c, size)
					b := make([]byte, int(size[0]))
					io.ReadFull(c, b)
					host = string(b)
				default:
					return
				}
				portBytes := make([]byte, 2)
				io.ReadFull(c, portBytes)
				target := net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(portBytes))))
				addresses <- target
				if forcedTarget != "" {
					target = forcedTarget
				}
				remote, err := net.Dial("tcp", target)
				if err != nil {
					c.Write([]byte{5, 5, 0, 1, 0, 0, 0, 0, 0, 0})
					return
				}
				defer remote.Close()
				c.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0})
				done := make(chan struct{})
				go func() { io.Copy(remote, c); remote.Close(); close(done) }()
				io.Copy(c, remote)
				c.Close()
				remote.Close()
				<-done
			}()
		}
	}()
	address := listener.Addr().(*net.TCPAddr)
	entry := Entry{Scheme: "socks5", Host: "127.0.0.1", Port: fmt.Sprint(address.Port)}
	if auth {
		entry.Username = "u"
		entry.Password = "p"
	}
	return entry, addresses
}
func testTarget(t *testing.T, status int) (*httptest.Server, *x509.CertPool) {
	t.Helper()
	s := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Proxy-Authorization") != "" {
			t.Error("proxy credentials leaked to destination")
		}
		w.WriteHeader(status)
		if r.Method != http.MethodHead {
			io.WriteString(w, "local-target")
		}
	}))
	s.Config.ErrorLog = log.New(io.Discard, "", 0)
	s.StartTLS()
	t.Cleanup(s.Close)
	roots := x509.NewCertPool()
	roots.AddCert(s.Certificate())
	return s, roots
}
func TestProxyHTTPSAndRelay(t *testing.T) {
	for _, protocol := range []string{"http", "http-auth", "socks5", "socks5-auth"} {
		t.Run(protocol, func(t *testing.T) {
			target, roots := testTarget(t, 200)
			var entry Entry
			if strings.HasPrefix(protocol, "http") {
				entry = testHTTPProxy(t, strings.HasSuffix(protocol, "-auth"), 0)
			} else {
				entry, _ = testSOCKSProxy(t, strings.HasSuffix(protocol, "-auth"), false, "")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			result := testEndpoint(ctx, entry, target.URL, roots)
			if !result.OK || !result.TunnelOK || result.Status != 200 {
				t.Fatalf("HTTPS test failed: %+v", result)
			}
			// Production default rejects untrusted certificates, never disables TLS verification.
			if got := TestEndpoint(ctx, entry, target.URL); got.OK || !strings.Contains(got.Detail, "TLS") {
				t.Fatalf("accepted self-signed TLS: %+v", got)
			}
			relay, err := StartRelay(ctx, entry)
			if err != nil {
				t.Fatal(err)
			}
			defer relay.Close()
			transport := &http.Transport{Proxy: http.ProxyURL(relay.Entry.TransportURL()), TLSClientConfig: &tls.Config{RootCAs: roots}}
			defer transport.CloseIdleConnections()
			client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
			response, err := client.Get(target.URL)
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(response.Body)
			response.Body.Close()
			if string(body) != "local-target" {
				t.Fatalf("relay failed: %s", body)
			}
			relay.Close()
			relay.Close()
			if _, err := client.Get(target.URL); err == nil {
				t.Fatal("closed relay still accepts connections")
			}
		})
	}
}
func TestProxyFailuresAndTargetRestriction(t *testing.T) {
	for _, code := range []int{407, 403, 502} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			e := testHTTPProxy(t, true, code)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			conn, err := e.DialTunnel(ctx, "unresolvable.invalid:443")
			if conn != nil {
				conn.Close()
			}
			if err == nil || !strings.Contains(err.Error(), fmt.Sprint(code)) {
				t.Fatalf("missing status: %v", err)
			}
			if strings.Contains(err.Error(), "u:p") {
				t.Fatal("credentials leaked")
			}
		})
	}
	e, _ := testSOCKSProxy(t, true, true, "")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if conn, err := e.DialTunnel(ctx, "unresolvable.invalid:443"); err == nil {
		conn.Close()
		t.Fatal("wrong credentials accepted")
	}
	target, roots := testTarget(t, 403)
	e = testHTTPProxy(t, true, 0)
	got := testEndpoint(ctx, e, target.URL, roots)
	if got.OK || !got.TunnelOK || got.Status != 403 {
		t.Fatalf("site refusal must not be reported proxy PASS: %+v", got)
	}
}
func TestSOCKSRemoteDNS(t *testing.T) {
	target, _ := testTarget(t, 200)
	u, _ := url.Parse(target.URL)
	entry, addresses := testSOCKSProxy(t, true, false, u.Host)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	conn, err := entry.DialTunnel(ctx, "does-not-resolve.invalid:443")
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
	if got := <-addresses; got != "does-not-resolve.invalid:443" {
		t.Fatalf("target DNS not sent to proxy: %s", got)
	}
}
func TestHTTPForwardingThroughRelay(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Proxy-Authorization") != "" {
			t.Error("proxy credentials leaked")
		}
		io.WriteString(w, "http-forward")
	}))
	defer target.Close()
	upstream := testHTTPProxy(t, true, 0)
	relay, err := StartRelay(context.Background(), upstream)
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()
	transport := &http.Transport{Proxy: http.ProxyURL(relay.Entry.TransportURL())}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: time.Second}
	response, err := client.Get(target.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if string(body) != "http-forward" {
		t.Fatal(string(body))
	}
}
func TestCancellationAndListLimits(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	results, err := TestList(ctx, "invalid\n\nhttp://secret:password@127.0.0.1:1\nsocks5://127.0.0.1:1", "https://does-not-resolve.invalid/")
	if err != nil || len(results) != 3 {
		t.Fatalf("list: %v %v", results, err)
	}
	for i, line := range []int{1, 3, 4} {
		if results[i].Line != line || results[i].OK {
			t.Fatal(results)
		}
		if strings.Contains(results[i].Label+results[i].Detail, "secret") {
			t.Fatal("credentials leaked")
		}
	}
	if _, err := TestList(ctx, "", "https://synthetic.invalid"); err == nil {
		t.Fatal("accepted empty list")
	}
	if _, err := TestList(ctx, strings.Repeat("host:80\n", MaxTestEntries+1), "https://synthetic.invalid"); err == nil {
		t.Fatal("test limit ignored")
	}
	relay, err := StartRelay(context.Background(), Entry{Scheme: "http", Host: "127.0.0.1", Port: "1"})
	if err != nil {
		t.Fatal(err)
	}
	address := relay.Entry.Address()
	relay.Close()
	if c, err := net.DialTimeout("tcp", address, 100*time.Millisecond); err == nil {
		c.Close()
		t.Fatal("relay listener not closed")
	}
}
func TestBufferedCONNECTBytes(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		http.ReadRequest(bufio.NewReader(conn))
		io.WriteString(conn, "HTTP/1.1 200 OK\r\n\r\nhello")
	}()
	host, port, _ := net.SplitHostPort(listener.Addr().String())
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	conn, err := (Entry{Scheme: "http", Host: host, Port: port}).DialTunnel(ctx, "synthetic.invalid:443")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	data, err := io.ReadAll(conn)
	if err != nil || string(data) != "hello" {
		t.Fatalf("buffered tunnel bytes lost: %q %v", data, err)
	}
	<-done
}

func TestRelayNeverFallsBackAndClosesTunnels(t *testing.T) {
	target, _ := testTarget(t, 200)
	upstream := testHTTPProxy(t, true, 407)
	relay, err := StartRelay(context.Background(), upstream)
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()
	transport := &http.Transport{Proxy: http.ProxyURL(relay.Entry.TransportURL())}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: time.Second}
	if response, err := client.Get(target.URL); err == nil {
		response.Body.Close()
		t.Fatal("failed upstream must not be bypassed")
	}
	// A raw tunnel still open when relay closes must be disconnected.
	relay.Close()
	upstream = testHTTPProxy(t, false, 0)
	relay, err = StartRelay(context.Background(), upstream)
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	tunnel, err := relay.Entry.DialTunnel(ctx, strings.TrimPrefix(target.URL, "https://"))
	if err != nil {
		t.Fatal(err)
	}
	defer tunnel.Close()
	relay.Close()
	tunnel.SetReadDeadline(time.Now().Add(time.Second))
	b := make([]byte, 1)
	if _, err := tunnel.Read(b); err == nil {
		t.Fatal("active tunnel survived relay close")
	}
}
func TestHandshakeTimeoutAndHeaderLimit(t *testing.T) {
	for _, huge := range []bool{false, true} {
		t.Run(fmt.Sprint(huge), func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			done := make(chan struct{})
			go func() {
				defer close(done)
				c, err := listener.Accept()
				if err != nil {
					return
				}
				defer c.Close()
				http.ReadRequest(bufio.NewReader(c))
				if huge {
					io.WriteString(c, "HTTP/1.1 200 OK\r\nHuge: "+strings.Repeat("x", 128<<10)+"\r\n\r\n")
				} else {
					io.Copy(io.Discard, c)
				}
			}()
			host, port, _ := net.SplitHostPort(listener.Addr().String())
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			conn, err := (Entry{Scheme: "http", Host: host, Port: port}).DialTunnel(ctx, "synthetic.invalid:443")
			if err == nil {
				conn.Close()
				t.Fatal("unbounded/blocked handshake accepted")
			}
			<-done
		})
	}
}
