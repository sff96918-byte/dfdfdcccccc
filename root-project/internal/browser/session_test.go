package browser

import (
	"context"
	"errors"
	"testing"

	"chitchatbot/internal/model"
)

type sessionBackendStub struct {
	response FetchResponse
	err      error
}

func (s sessionBackendStub) Name() string {
	return "stub"
}

func (s sessionBackendStub) Start(context.Context, Options) error {
	return nil
}

func (s sessionBackendStub) CaptureSession(context.Context) (model.LoginInfo, error) {
	return model.LoginInfo{}, nil
}

func (s sessionBackendStub) Fetch(context.Context, FetchRequest) (FetchResponse, error) {
	return s.response, s.err
}

func (s sessionBackendStub) Close() error {
	return nil
}

func TestSessionAuthenticated(t *testing.T) {
	t.Run("accepts success status", func(t *testing.T) {
		ready, err := SessionAuthenticated(context.Background(), sessionBackendStub{
			response: FetchResponse{Status: 200},
		}, "https://api.chitchat.gg/")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !ready {
			t.Fatal("expected authenticated session")
		}
	})

	t.Run("rejects unauthorized status", func(t *testing.T) {
		ready, err := SessionAuthenticated(context.Background(), sessionBackendStub{
			response: FetchResponse{Status: 401},
		}, "https://api.chitchat.gg/")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if ready {
			t.Fatal("expected unauthorized session to be rejected")
		}
	})

	t.Run("returns backend error", func(t *testing.T) {
		wantErr := errors.New("boom")
		_, err := SessionAuthenticated(context.Background(), sessionBackendStub{
			err: wantErr,
		}, "https://api.chitchat.gg/")
		if !errors.Is(err, wantErr) {
			t.Fatalf("expected %v, got %v", wantErr, err)
		}
	})
}
