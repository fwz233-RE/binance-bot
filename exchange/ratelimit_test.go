package exchange

import (
	"fmt"
	"net/http"
	"testing"
	"time"
)

func TestParseRetryAfter(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
	}{
		{"", 0},
		{"abc", 0},
		{"-3", 0},
		{"0", 0},
		{"7", 7 * time.Second},
	}
	for _, c := range cases {
		if got := parseRetryAfter(c.in); got != c.want {
			t.Errorf("parseRetryAfter(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestRateLimitDelay(t *testing.T) {
	cases := []struct {
		name        string
		err         error
		wantLimited bool
		wantDelay   time.Duration
	}{
		{"nil", nil, false, 0},
		{"plain error", fmt.Errorf("boom"), false, 0},
		{"api error other code", &FuturesAPIError{Code: -2019, HTTPStatus: 400}, false, 0},
		{"http 429", &FuturesAPIError{HTTPStatus: http.StatusTooManyRequests, RetryAfter: 5 * time.Second}, true, 5 * time.Second},
		{"http 418 ip ban", &FuturesAPIError{HTTPStatus: 418}, true, 0},
		{"code -1003", &FuturesAPIError{Code: -1003, HTTPStatus: 400}, true, 0},
		{"wrapped api error", fmt.Errorf("futures: %w", &FuturesAPIError{HTTPStatus: 429}), true, 0},
	}
	for _, c := range cases {
		delay, limited := rateLimitDelay(c.err)
		if limited != c.wantLimited || delay != c.wantDelay {
			t.Errorf("%s: rateLimitDelay() = (%v, %v), want (%v, %v)", c.name, delay, limited, c.wantDelay, c.wantLimited)
		}
	}
}