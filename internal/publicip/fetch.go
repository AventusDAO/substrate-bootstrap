package publicip

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

const (
	fetchURL     = "https://ifconfig.io/ip"
	fetchTimeout = 5 * time.Second
)

// Fetch retrieves the public IP from ifconfig.io. Returns the IP string on success,
// or empty string and error on failure (network error, invalid response, non-IP content).
func Fetch(ctx context.Context, client *http.Client) (string, error) {
	return FetchFrom(ctx, client, fetchURL)
}

// FetchFrom retrieves the public IP from the given URL. Used by Fetch with the default URL;
// exposed for testing with a mock HTTP server.
func FetchFrom(ctx context.Context, client *http.Client, url string) (string, error) {
	if client == nil {
		client = &http.Client{Timeout: fetchTimeout}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errBody := make([]byte, 256)
		n, _ := resp.Body.Read(errBody)
		snippet := strings.TrimSpace(string(errBody[:n]))
		msg := "unexpected HTTP status: " + resp.Status
		if snippet != "" {
			msg += " body: " + snippet
		}
		return "", &fetchError{msg: msg}
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64))
	if err != nil {
		return "", err
	}

	ip := strings.TrimSpace(string(body))
	if ip == "" {
		return "", &fetchError{msg: "empty response"}
	}

	parsed := net.ParseIP(ip)
	if parsed == nil {
		return "", &fetchError{msg: "invalid IP: " + ip}
	}
	if parsed.To4() == nil {
		return "", &fetchError{msg: "non-IPv4 address: " + ip}
	}

	return ip, nil
}

type fetchError struct {
	msg string
}

func (e *fetchError) Error() string { return e.msg }
