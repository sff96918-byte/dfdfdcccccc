package proxy

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
)

type Entry struct {
	Scheme   string
	Host     string
	Port     string
	Username string
	Password string
}

type Rotator struct {
	entries []Entry
	next    atomic.Uint64
}

func ParseList(raw string) ([]Entry, error) {
	lines := strings.Split(strings.ReplaceAll(raw, "\r", ""), "\n")
	result := make([]Entry, 0, len(lines))

	for index, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		entry, err := ParseLine(line)
		if err != nil {
			return nil, fmt.Errorf("proxy line %d: %w", index+1, err)
		}

		result = append(result, entry)
	}

	return result, nil
}

// ParseLine supports HTTP/SOCKS5 URL and provider-style host:port:user:pass.
// Bare endpoints default to HTTP; use a socks5:// prefix for SOCKS5.
func ParseLine(line string) (Entry, error) {
	line = strings.TrimSpace(line)
	scheme, rest := "http", line
	if i := strings.Index(line, "://"); i >= 0 && !strings.ContainsAny(line[:i], ":@[]") {
		var err error
		scheme, err = normalizeScheme(line[:i])
		if err != nil {
			return Entry{}, err
		}
		rest = line[i+3:]
	}
	var entry Entry
	partsCheck := strings.SplitN(rest, ":", 4)
	providerStyle := false
	if len(partsCheck) == 4 {
		_, err := strconv.Atoi(partsCheck[1])
		providerStyle = err == nil
	}
	if strings.HasPrefix(rest, "[") {
		if end := strings.Index(rest, "]:"); end >= 0 {
			providerStyle = len(strings.SplitN(rest[end+2:], ":", 3)) == 3
		}
	}
	if strings.Contains(rest, "@") && !providerStyle {
		parsed, err := url.Parse(scheme + "://" + rest)
		if err != nil {
			return Entry{}, fmt.Errorf("invalid proxy URL; URL-encode special characters in credentials")
		}
		if parsed.Path != "" && parsed.Path != "/" || parsed.RawQuery != "" || parsed.Fragment != "" {
			return Entry{}, fmt.Errorf("proxy URL must contain only credentials and host:port")
		}
		entry = Entry{Scheme: scheme, Host: parsed.Hostname(), Port: parsed.Port()}
		if parsed.User != nil {
			entry.Username = parsed.User.Username()
			entry.Password, _ = parsed.User.Password()
		}
	} else {

		if strings.HasPrefix(rest, "[") {
			// Bracketed IPv6 with optional provider-style credentials.
			end := strings.Index(rest, "]:")
			if end < 0 {
				return Entry{}, fmt.Errorf("expected [IPv6]:port")
			}
			tail := strings.SplitN(rest[end+2:], ":", 3)
			if len(tail) == 1 {
				tail[0] = strings.TrimSuffix(tail[0], "/")
			}
			entry = Entry{Scheme: scheme, Host: rest[1:end], Port: tail[0]}
			if len(tail) == 3 {
				entry.Username = tail[1]
				entry.Password = tail[2]
			} else if len(tail) != 1 {
				return Entry{}, fmt.Errorf("expected host:port:user:pass")
			}
		} else {
			parts := strings.SplitN(rest, ":", 4)
			if len(parts) == 2 {
				parts[1] = strings.TrimSuffix(parts[1], "/")
			}
			if len(parts) != 2 && len(parts) != 4 {
				return Entry{}, fmt.Errorf("expected host:port, host:port:user:pass or scheme://user:pass@host:port")
			}
			entry = Entry{Scheme: scheme, Host: parts[0], Port: parts[1]}
			if len(parts) == 4 {
				entry.Username = parts[2]
				entry.Password = parts[3]
			}
		}
	}
	port, err := strconv.Atoi(entry.Port)
	if err != nil || port < 1 || port > 65535 {
		return Entry{}, fmt.Errorf("proxy port must be a number from 1 to 65535")
	}
	if entry.Host == "" || strings.ContainsAny(entry.Host, " /\\\t\r\n?#@") {
		return Entry{}, fmt.Errorf("invalid proxy host")
	}
	if strings.ContainsAny(entry.Username+entry.Password, "\r\n") {
		return Entry{}, fmt.Errorf("invalid proxy credentials")
	}
	if entry.HasAuth() && (entry.Username == "" || scheme == "socks5" && entry.Password == "") {
		return Entry{}, fmt.Errorf("proxy authentication requires both username and password")
	}
	if scheme == "socks5" && (len(entry.Username) > 255 || len(entry.Password) > 255) {
		return Entry{}, fmt.Errorf("SOCKS5 credentials must be at most 255 bytes each")
	}
	return entry, nil
}

func NewRotator(entries []Entry) *Rotator {
	if len(entries) == 0 {
		return nil
	}

	cloned := make([]Entry, len(entries))
	copy(cloned, entries)

	return &Rotator{entries: cloned}
}

func (r *Rotator) Next() (Entry, bool) {
	if r == nil || len(r.entries) == 0 {
		return Entry{}, false
	}

	index := int((r.next.Add(1) - 1) % uint64(len(r.entries)))
	return r.entries[index], true
}

func (r *Rotator) Len() int {
	if r == nil {
		return 0
	}
	return len(r.entries)
}

func (e Entry) Address() string {
	return net.JoinHostPort(e.Host, e.Port)
}

func (e Entry) BrowserURL() string {
	return e.SchemeOrDefault() + "://" + e.Address()
}

func (e Entry) TransportURL() *url.URL {
	result := &url.URL{
		Scheme: e.SchemeOrDefault(),
		Host:   e.Address(),
	}
	if e.HasAuth() {
		result.User = url.UserPassword(e.Username, e.Password)
	}
	return result
}

func (e Entry) HasAuth() bool {
	return e.Username != "" || e.Password != ""
}

func (e Entry) SupportsUDP() bool {
	switch e.SchemeOrDefault() {
	case "socks5":
		return true
	default:
		return false
	}
}

func (e Entry) SchemeOrDefault() string {
	scheme, err := normalizeScheme(e.Scheme)
	if err != nil {
		return "http"
	}
	return scheme
}

func (e Entry) Label() string {
	return e.SchemeOrDefault() + "://" + e.Address()
}

func normalizeScheme(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "http":
		return "http", nil
	case "socks5", "socks5h", "sock5":
		return "socks5", nil
	default:
		return "", fmt.Errorf("unsupported proxy protocol; use http:// or socks5://")
	}
}
