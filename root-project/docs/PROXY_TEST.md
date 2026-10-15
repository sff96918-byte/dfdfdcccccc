# Proxy formats and dashboard tests — 2026-10-15

## Supported inputs

HTTP and SOCKS5, with or without username/password:

- `host:port`
- `host:port:user:pass` (password may contain colons, @ or slashes)
- `http://host:port`, `http://user:pass@host:port`
- `http://host:port:user:pass`
- `socks5://host:port`, `socks5://user:pass@host:port`
- `socks5://host:port:user:pass`
- `user:pass@host:port` (default HTTP unless dashboard default is SOCKS5)
- Bracketed IPv6: `[::1]:8080`, `socks5://user:pass@[::1]:1080`

`socks5h://` and `sock5://` aliases normalize to SOCKS5. Destination DNS is
resolved by the upstream proxy. URL-form special characters in credentials must
be percent-encoded, e.g. `@` becomes `%40`. SOCKS5 username/password must each be
1–255 bytes. HTTP Basic supports a nonempty username with an empty password.
Ports must be 1–65535. Unknown protocols, SOCKS4, HTTPS-to-proxy and NTLM/Digest
proxy authentication are NOT advertised as supported. "HTTP proxy" can carry
HTTPS destination traffic through CONNECT; it is not an HTTPS proxy endpoint.

The dashboard default selector applies to **plain lines only**. HTTP is the
initial default for backward compatibility. Explicit protocol prefixes override
it, allowing a mixed list. Save serializes SOCKS5 prefixes into plain SOCKS5
lines, so the chosen protocol survives restart without a new settings schema.
Raw parser callers outside the dashboard still default bare lines to HTTP.

## Test Proxy button

Tests the current unsaved form even if Enable proxy is unchecked. It neither
saves/enables proxies, opens a browser/login, nor changes running workers or
rotation. Up to 20 nonblank lines / 64 KiB are accepted, with four concurrent
checks, a 12-second budget per proxy and a 65-second batch budget. Invalid lines
have individual results; original physical line numbers and result order remain.

Each check uses the same upstream tunnel/authentication implementation as the
browser relay. It opens CONNECT through HTTP or RFC 1928/1929 SOCKS5, verifies
TLS with system trust, and sends HEAD to `https://app.chitchat.gg/`. There are no
account cookies, chat API calls, redirect following or direct fallback.

Results contain only line number, credential-free endpoint, elapsed milliseconds,
HTTP status, and sanitized diagnostics:

- **PASS**: authentication/tunnel, destination TLS and HTTP 2xx/3xx succeeded.
- **REACHABLE / SITE RESTRICTED**: tunnel and TLS work, but destination returned
  e.g. 403. This does not mean proxy credentials failed.
- **FAIL**: invalid input, proxy offline/timeout, HTTP 407, rejected SOCKS5 auth,
  CONNECT rejection, TLS validation failure or response failure.

Latency is the full check time, not ICMP ping. PASS is not proof of browser login
or live chat: HEAD may differ from browser requests and site protections can
change. Public exit IP is not collected. Results disappear on restart. Credentials
and raw provider bodies are not included in test results or logged. Test errors
never echo a malformed input URL. Edits during a test are preserved and stale
results are labeled as belonging to the previous tested values.

## Browser transport

Rod (including Show Mode) and ChromeDP start one ephemeral **127.0.0.1-only HTTP
relay per proxied browser**. Chrome sees no upstream credentials. The relay
handles upstream HTTP Basic and authenticated SOCKS5 (which Chrome cannot handle
directly). HTTPS/WSS CONNECT tunnel bytes are forwarded transparently; normal
HTTP forwarding strips proxy credentials before reaching destinations. The relay
always uses the selected upstream. A failed proxy is NOT retried directly.

Listener/active tunnels are closed on browser close, startup failure or parent
cancellation. Handshakes are time/header-size bounded. Relay ports are local to
the desktop, not web-preview ports; no separate frontend localhost fetch is used.
The loopback listener has no client authentication: another process on the same
machine could connect while it runs. This is not a VPN, UDP relay or a guarantee
against browser/WebRTC traffic outside HTTP(S); existing WebRTC behavior is not
changed. No AI, worker assignment, license, queue/reply logic, DOM chat or Home
transcript code is changed by this feature.

## Evidence and limits

- `npm test`: 65 PASS; Show Mode's 19-test suite also PASS (subset, not additional).
- Actual `internal/proxy`: 11 top-level local test groups with subcases, PASS
  under race detector and repeated runs; vet PASS. All network servers are local
  synthetic fixtures: HTTP and SOCKS5, with/without auth, remote DNS, CONNECT byte
  preservation, certificate validation, destination restriction, failed upstream
  without direct fallback, ordinary HTTP forwarding, limits/timeouts and cleanup.
- Exact actual browser/proxy/runtimeassets sources in an isolated module compile
  and pass race/vet tests with real pinned Rod/ChromeDP/CDProto dependencies.
  Legacy dependency module metadata is synthesized only for modules lacking
  go.mod; project go.mod/go.sum are unchanged. Windows-target browser/proxy test
  executables compile, but are not Windows desktop executables and were not run.
- Full standard gate remains BLOCKED by inaccessible Go dependency download;
  native Windows/Wails and actual owner proxy/live website acceptance NOT_RUN.

## Owner acceptance

Build the updated source in a separate folder using `install.bat` (or
`install.bat build` when prerequisites are ready); the old EXE does not update
just by replacing source. In Proxy: choose the plain-line default, paste entries,
click Test Proxy, inspect each row, then Save Proxy and enable it for browsers.
Try HTTP and SOCKS5 with/without authentication, bad credentials, mixed formats,
and browser close/reopen. Retest Show Mode and Home actual SMS unchanged.
Never share proxy passwords, cookies, profiles or unredacted logs.

## Provider rejection diagnostics (2026-10-15, later same day)

Owner reported a Webshare line (`http://p.webshare.io:80`) failing the dashboard
test with `HTTP proxy rejected CONNECT (402)` and no way to tell why. The wire
format was re-checked byte-for-byte locally and is correct
(`CONNECT host:port HTTP/1.1` + `Host` + `Proxy-Authorization: Basic …`); the
remaining question is always what the provider itself answered.

Per failing line the dashboard now adds, in addition to the existing summary:

- **Provider said:** a sanitized excerpt (max 200 chars, single line) of the
  proxy's own rejection body — HTML tags stripped, control characters removed,
  the configured username/password and their Basic token replaced with `***`.
  The read is bounded to 512 bytes / 1.2 s and can never fail the check.
- **Auth challenge:** the provider's `Proxy-Authenticate` value, if offered.
- **Next step:** one plain-language action derived from the status code, or
  from "no HTTP answer at all" when the TCP/handshake layer failed.
- **authenticated**: whether the tested line actually contained `user:pass`.
  A rejection on a line without credentials says so explicitly, because that is
  the most common cause of a 402/407 with a provider endpoint copied as
  `host:port` only.

Nothing new is sent anywhere: the same single HTTPS HEAD to
`https://app.chitchat.gg/` through the proxy is still the only request, and no
exit IP is collected. Sanitization rules are covered by
`internal/proxy/diagnostics_test.go`.

## Webshare-specific checklist

Webshare answers with real HTTP statuses; read the new "Provider said" line
first, then:

| Status | Meaning at Webshare | What to do |
|--------|--------------------|------------|
| 402 + line has no credentials | The proxy was called without authentication. | Use `p.webshare.io:80:user:pass` (same username/password pair from the dashboard) or authorize this machine's IP. |
| 402 + credentials present | Account/entitlement answer: expired plan, exhausted monthly bandwidth, or a username that does not belong to this endpoint. | Check Dashboard → My Plan and Stats → Errors. Copy address, port, username and password from **one** Connection Method view (Rotating / Backbone / Direct). |
| 407 | `no_proxies_allocated`-class refusal: that username/port combination is not on the current list. | Re-copy from the same view; prefer Backbone/Rotating if the plan auto-replaces proxies. |
| 403 | `client_connect_forbidden_host`: Webshare blocks that destination for that proxy type (common on residential plans). | Try another Webshare proxy type/plan, or another proxy provider. |
| 429 | `client_connect_high_concurrency`: too many parallel requests. | Lower parallel browsers/proxies through that endpoint. |

Fastest way to separate "app problem" from "provider problem" on Windows: run
the same line with the built-in curl (PowerShell or cmd):

```bat
curl -v -x "http://USER:PASS@p.webshare.io:80" https://example.com
```

If curl fails with the same status, the provider/credentials are the cause (the
app is only repeating the answer). If curl succeeds while the app fails, report
that difference — the app's CONNECT request is then compared byte-for-byte with
curl's.
