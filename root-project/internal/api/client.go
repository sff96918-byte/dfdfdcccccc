package api

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"chitchatbot/internal/model"
	"chitchatbot/internal/proxy"
)

type FetchRequest struct {
	Method  string
	URL     string
	Headers map[string]string
	Body    *string
}

type FetchResponse struct {
	OK         bool   `json:"ok"`
	Status     int    `json:"status"`
	StatusText string `json:"statusText"`
	Text       string `json:"text"`
}

type Client struct {
	httpClient *http.Client
	login      model.LoginInfo
	appURL     string
}

func New(login model.LoginInfo, appURL string, proxyRotator *proxy.Rotator) *Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DisableKeepAlives = true
	if proxyRotator != nil && proxyRotator.Len() > 0 {
		transport.Proxy = func(*http.Request) (*url.URL, error) {
			entry, ok := proxyRotator.Next()
			if !ok {
				return nil, nil
			}
			return entry.TransportURL(), nil
		}
	}

	return &Client{
		httpClient: &http.Client{
			Timeout:   30 * time.Second,
			Transport: transport,
		},
		login:  login,
		appURL: appURL,
	}
}

func (c *Client) Fetch(ctx context.Context, req FetchRequest) (FetchResponse, error) {
	token := c.login.Token()
	if token == "" {
		return FetchResponse{}, fmt.Errorf("missing token cookie in login info")
	}

	var body io.Reader
	if req.Body != nil {
		body = strings.NewReader(*req.Body)
	}

	httpReq, err := http.NewRequestWithContext(ctx, req.Method, req.URL, body)
	if err != nil {
		return FetchResponse{}, err
	}

	for key, value := range c.baseHeaders(token) {
		httpReq.Header.Set(key, value)
	}
	for key, value := range req.Headers {
		httpReq.Header.Set(key, value)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return FetchResponse{}, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return FetchResponse{}, err
	}

	return FetchResponse{
		OK:         resp.StatusCode >= 200 && resp.StatusCode < 300,
		Status:     resp.StatusCode,
		StatusText: resp.Status,
		Text:       string(data),
	}, nil
}

func (c *Client) baseHeaders(token string) map[string]string {
	userAgent := c.login.UserAgent
	if strings.TrimSpace(userAgent) == "" {
		userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/146.0.0.0 Safari/537.36"
	}

	chromeMajor := chromeMajorFromUserAgent(userAgent)

	cookieHeader := c.login.CookieHeader()
	if cookieHeader == "" {
		cookieHeader = "token=" + token
	}

	return map[string]string{
		"Accept":             "*/*",
		"Accept-Language":    "en-US,en;q=0.9",
		"Cookie":             cookieHeader,
		"Origin":             "https://app.chitchat.gg",
		"Referer":            c.appURL,
		"Sec-Fetch-Dest":     "empty",
		"Sec-Fetch-Mode":     "cors",
		"Sec-Fetch-Site":     "same-site",
		"User-Agent":         userAgent,
		"sec-ch-ua":          fmt.Sprintf(`"Chromium";v="%s", "Not-A.Brand";v="24", "Google Chrome";v="%s"`, chromeMajor, chromeMajor),
		"sec-ch-ua-mobile":   "?0",
		"sec-ch-ua-platform": `"Windows"`,
	}
}

func chromeMajorFromUserAgent(userAgent string) string {
	match := regexp.MustCompile(`Chrome/(\d+)\.`).FindStringSubmatch(userAgent)
	if len(match) == 2 {
		return match[1]
	}
	return "146"
}
