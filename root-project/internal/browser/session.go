package browser

import (
	"context"
	"strings"
)

func SessionAuthenticated(ctx context.Context, backend Backend, apiBaseURL string) (bool, error) {
	resp, err := backend.Fetch(ctx, FetchRequest{
		Method: "GET",
		URL:    strings.TrimRight(apiBaseURL, "/") + "/users/me/relationships",
		Headers: map[string]string{
			"accept": "*/*",
		},
	})
	if err != nil {
		return false, err
	}

	return resp.Status >= 200 && resp.Status < 400, nil
}
