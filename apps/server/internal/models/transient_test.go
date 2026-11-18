package models

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestIsTransient(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"tls handshake timeout (rpt_0mwmcmitl94zpj4htnc)", &APICallError{Message: `Cannot connect to API: Post "https://example.test/v1/messages": net/http: TLS handshake timeout`, IsRetryable: true}, true},
		{"wrapped by the loop's retry", fmt.Errorf("Failed after 2 attempts: %w", &APICallError{Message: "Cannot connect to API: connection refused"}), true},
		{"503", NewHTTPError(503), true},
		{"429 rate limit", &APICallError{Message: "HTTP 429", StatusCode: 429, ResponseBody: `{"error":"rate limit"}`}, true},
		{"429 quota exhausted", &APICallError{Message: "HTTP 429", StatusCode: 429, ResponseBody: `{"error":"insufficient_quota"}`}, false},
		{"400", NewHTTPError(400), false},
		{"401", NewHTTPError(401), false},
		{"overloaded stream error", &StreamError{Message: "Overloaded", Type: "overloaded_error", StatusCode: 529}, true},
		{"invalid request stream error", &StreamError{Message: "prompt is too long", Type: "invalid_request_error", StatusCode: 400}, false},
		{"deadline", context.DeadlineExceeded, true},
		{"canceled", context.Canceled, false},
		{"plain bug", errors.New("nil pointer dereference"), false},
	}
	for _, c := range cases {
		if got := IsTransient(c.err); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}
