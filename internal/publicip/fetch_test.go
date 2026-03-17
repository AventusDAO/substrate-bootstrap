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
	// FetchFrom is exposed so tests can call the IP-fetcher against a mock HTTP server.
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
