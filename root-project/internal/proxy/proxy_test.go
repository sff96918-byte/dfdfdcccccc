package proxy

import (
	"strings"
	"testing"
)

func TestProxyFormats(t *testing.T) {
	for _, input := range []string{"host.example:8080", "http://host.example:8080", "host.example:8080:u:p", "http://host.example:8080:u:p", "http://u:p@host.example:8080", "u:p@host.example:8080", "socks5://u:p@host.example:8080", "socks5://host.example:8080:u:p", "socks5h://host.example:8080", "sock5://host.example:8080", "[::1]:8080", "socks5://u:p@[::1]:8080"} {
		t.Run(input, func(t *testing.T) {
			entry, err := ParseLine(input)
			if err != nil {
				t.Fatal(err)
			}
			if entry.Port != "8080" {
				t.Fatal(entry)
			}
			if strings.Contains(entry.Label(), "u:p") {
				t.Fatal("label exposes credentials")
			}
		})
	}
	e, err := ParseLine("socks5://u%40name:p%3Aa%40ss@host.example:8080")
	if err != nil || e.Username != "u@name" || e.Password != "p:a@ss" {
		t.Fatalf("URL decoding: %#v %v", e, err)
	}
	e, err = ParseLine("host.example:8080:user:pass:with:colons")
	if err != nil || e.Password != "pass:with:colons" {
		t.Fatal("colon password not retained")
	}
	e, err = ParseLine("socks5://host:8080:user:p@ss:with/slash/")
	if err != nil || e.Password != "p@ss:with/slash/" {
		t.Fatal("provider credentials altered")
	}
	e, err = ParseLine("host:80:user:p://secret")
	if err != nil || e.Password != "p://secret" {
		t.Fatal("scheme-like provider password altered")
	}
	e, err = ParseLine("http://[2001:db8::1]:8080")
	if err != nil || e.Address() != "[2001:db8::1]:8080" {
		t.Fatal("IPv6 endpoint invalid")
	}
}
func TestInvalidProxyFormatsAreSafe(t *testing.T) {
	for _, input := range []string{"", "host", "host:0", "host:65536", "host:no", "http://secret:password@host:abc", "http://u:p@host:80/path", "http://u:p@host:80?q=x", "ftp://host:80", "socks4://host:80", "socks5://host:80:user:", "socks5://host:80:" + strings.Repeat("x", 256) + ":p"} {
		_, err := ParseLine(input)
		if err == nil {
			t.Fatalf("accepted invalid format %q", input)
		}
		if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "password@") {
			t.Fatal("input leaked in parse error")
		}
	}
	_, err := ParseList("\r\n\ninvalid\n")
	if err == nil || !strings.Contains(err.Error(), "line 3") {
		t.Fatalf("line number: %v", err)
	}
}
func TestRotatorAndTransportPreserved(t *testing.T) {
	list, err := ParseList("host:80:u:p\r\nsocks5://host2:1080\n")
	if err != nil {
		t.Fatal(err)
	}
	r := NewRotator(list)
	for _, want := range []string{"host", "host2", "host"} {
		got, ok := r.Next()
		if !ok || got.Host != want {
			t.Fatal("rotation changed")
		}
	}
	if r.Len() != 2 || list[0].TransportURL().User.Username() != "u" || list[1].SchemeOrDefault() != "socks5" {
		t.Fatal("transport semantics changed")
	}
}
