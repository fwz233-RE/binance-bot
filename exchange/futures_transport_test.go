package exchange

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"testing"
	"time"
)

type scriptedTransport struct {
	attempts       int
	closeIdleCalls int
	roundTrip      func(int, *http.Request) (*http.Response, error)
}

func (t *scriptedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.attempts++
	return t.roundTrip(t.attempts, req)
}

func (t *scriptedTransport) CloseIdleConnections() {
	t.closeIdleCalls++
}

func testFuturesClient(transport http.RoundTripper, waits *[]time.Duration) *FuturesClient {
	return &FuturesClient{
		baseURL: "https://futures.test",
		http:    &http.Client{Transport: transport},
		retryWait: func(delay time.Duration) {
			*waits = append(*waits, delay)
		},
	}
}

func response(status int, body string, headers http.Header) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     headers,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestFuturesRequestRetriesTransientGET(t *testing.T) {
	transport := &scriptedTransport{
		roundTrip: func(attempt int, _ *http.Request) (*http.Response, error) {
			if attempt <= 2 {
				return nil, io.ErrUnexpectedEOF
			}
			return response(http.StatusOK, `{"ok":true}`, nil), nil
		},
	}
	var waits []time.Duration
	client := testFuturesClient(transport, &waits)

	body, err := client.request(http.MethodGet, "/fapi/v1/klines", url.Values{}, false)
	if err != nil {
		t.Fatalf("request returned error: %v", err)
	}
	if string(body) != `{"ok":true}` {
		t.Fatalf("body = %q, want successful response", body)
	}
	if transport.attempts != 3 {
		t.Fatalf("attempts = %d, want 3", transport.attempts)
	}
	if transport.closeIdleCalls != 2 {
		t.Fatalf("CloseIdleConnections calls = %d, want 2", transport.closeIdleCalls)
	}
	if len(waits) != 2 {
		t.Fatalf("retry waits = %d, want 2", len(waits))
	}
}

func TestFuturesRequestStopsAtTransientRetryBudget(t *testing.T) {
	transport := &scriptedTransport{
		roundTrip: func(_ int, _ *http.Request) (*http.Response, error) {
			return nil, io.EOF
		},
	}
	var waits []time.Duration
	client := testFuturesClient(transport, &waits)

	_, err := client.request(http.MethodGet, "/fapi/v1/klines", nil, false)
	if err == nil {
		t.Fatal("request unexpectedly succeeded")
	}
	if transport.attempts != 3 {
		t.Fatalf("attempts = %d, want 3", transport.attempts)
	}
	if len(waits) != 2 {
		t.Fatalf("retry waits = %d, want 2", len(waits))
	}
}

func TestFuturesRequestDoesNotRetryPOSTTransportError(t *testing.T) {
	transport := &scriptedTransport{
		roundTrip: func(_ int, _ *http.Request) (*http.Response, error) {
			return nil, io.ErrUnexpectedEOF
		},
	}
	var waits []time.Duration
	client := testFuturesClient(transport, &waits)

	_, err := client.request(http.MethodPost, "/fapi/v1/order", nil, true)
	if err == nil {
		t.Fatal("request unexpectedly succeeded")
	}
	if transport.attempts != 1 {
		t.Fatalf("attempts = %d, want 1", transport.attempts)
	}
	if len(waits) != 0 {
		t.Fatalf("retry waits = %d, want 0", len(waits))
	}
}

func TestFuturesRequestDoesNotRetryHTTPClientError(t *testing.T) {
	transport := &scriptedTransport{
		roundTrip: func(_ int, _ *http.Request) (*http.Response, error) {
			return response(http.StatusBadRequest, `{"code":-1121,"msg":"Invalid symbol."}`, nil), nil
		},
	}
	var waits []time.Duration
	client := testFuturesClient(transport, &waits)

	_, err := client.request(http.MethodGet, "/fapi/v1/klines", nil, false)
	if err == nil {
		t.Fatal("request unexpectedly succeeded")
	}
	if transport.attempts != 1 {
		t.Fatalf("attempts = %d, want 1", transport.attempts)
	}
}

func TestFuturesRequestRetainsRateLimitRetry(t *testing.T) {
	transport := &scriptedTransport{
		roundTrip: func(attempt int, _ *http.Request) (*http.Response, error) {
			if attempt == 1 {
				headers := http.Header{"Retry-After": []string{"1"}}
				return response(http.StatusTooManyRequests, `{"code":-1003,"msg":"Too many requests."}`, headers), nil
			}
			return response(http.StatusOK, `ok`, nil), nil
		},
	}
	var waits []time.Duration
	client := testFuturesClient(transport, &waits)

	body, err := client.request(http.MethodGet, "/fapi/v1/klines", nil, false)
	if err != nil {
		t.Fatalf("request returned error: %v", err)
	}
	if string(body) != "ok" || transport.attempts != 2 {
		t.Fatalf("body = %q, attempts = %d; want ok after 2 attempts", body, transport.attempts)
	}
	if len(waits) != 1 || waits[0] != 2*time.Second {
		t.Fatalf("retry waits = %v, want [2s]", waits)
	}
}

func TestIsTransientTransportError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "EOF", err: io.EOF, want: true},
		{name: "unexpected EOF", err: fmt.Errorf("wrapped: %w", io.ErrUnexpectedEOF), want: true},
		{name: "deadline", err: context.DeadlineExceeded, want: true},
		{name: "connection reset", err: syscall.ECONNRESET, want: true},
		{name: "plain error", err: fmt.Errorf("permanent"), want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isTransientTransportError(tc.err); got != tc.want {
				t.Fatalf("isTransientTransportError() = %v, want %v", got, tc.want)
			}
		})
	}
}
