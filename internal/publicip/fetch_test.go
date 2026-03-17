package publicip

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFetch_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("203.0.113.42\n"))
	}))
	defer server.Close()

	client := &http.Client{Timeout: 5 * time.Second}
	// Override URL for test - we need to make Fetch use the test server.
	// Fetch uses a constant URL, so we need to either make it configurable or
	// use a different approach. Let me check the fetch.go - it uses fetchURL constant.
	// We need to make the URL injectable for testing. Let me add an optional base URL parameter.

	// Actually, the plan says "Tests can use a mock HTTP server." - the standard approach
	// is to inject the URL or use httptest with a custom transport that redirects.
	// Simpler: add a FetchFrom(client, url) or make Fetch accept an optional URL.
	// Or we could use the env var to override the URL in tests.

	// Simplest: add a second function FetchFrom(ctx, client, url) that takes URL, and
	// Fetch() calls FetchFrom with the default URL. Then tests use FetchFrom with server.URL.

	// Let me update fetch.go to support a configurable URL. I'll add:
	// FetchFrom(ctx, client, url) (string, error)
	// And Fetch(ctx, client) calls FetchFrom(ctx, client, fetchURL)

	// Actually, looking at the plan again - it just says "Tests can use a mock HTTP server."
	// The simplest way without changing the API is to use a custom Transport that
	// intercepts requests to ifconfig.io and returns our mock. But that's complex.

	// Simpler: add a FetchFrom(ctx, client, url) that the production Fetch calls.
	// The plan didn't specify the exact API. Let me add FetchFrom for testability.
	ip, err := FetchFrom(context.Background(), client, server.URL)
	require.NoError(t, err)
	assert.Equal(t, "203.0.113.42", ip)
}

func TestFetch_EmptyResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := &http.Client{Timeout: 5 * time.Second}
	_, err := FetchFrom(context.Background(), client, server.URL)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty")
}

func TestFetch_InvalidIP(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("not-an-ip"))
	}))
	defer server.Close()

	client := &http.Client{Timeout: 5 * time.Second}
	_, err := FetchFrom(context.Background(), client, server.URL)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid IP")
}

func TestFetch_NonOKStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	client := &http.Client{Timeout: 5 * time.Second}
	_, err := FetchFrom(context.Background(), client, server.URL)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "status")
}

func TestFetch_ContextCanceled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(1 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	client := &http.Client{Timeout: 5 * time.Second}
	_, err := FetchFrom(ctx, client, server.URL)
	require.Error(t, err)
}
