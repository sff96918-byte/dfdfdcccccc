package browser

import (
	"context"
	"errors"
	"fmt"

	"chitchatbot/internal/model"
	"chitchatbot/internal/proxy"
)

type Options struct {
	StartURL            string
	UserDataDir         string
	Headless            bool
	DisablePageRecovery bool
	Proxy               *proxy.Entry
}

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

type Backend interface {
	Name() string
	Start(context.Context, Options) error
	CaptureSession(context.Context) (model.LoginInfo, error)
	Fetch(context.Context, FetchRequest) (FetchResponse, error)
	Close() error
}

func New(name string) (Backend, error) {
	switch name {
	case "chromedp":
		return &ChromeDPBackend{}, nil
	case "rod":
		return &RodBackend{}, nil
	default:
		return nil, fmt.Errorf("unsupported backend %q", name)
	}
}

var ErrPageUnavailable = errors.New("browser page is not available")
