package checker

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const maxMetadataResponseSize int64 = 8 << 20 // 8 MiB

// hardenedHTTPClient clones a caller-provided client so all metadata checkers
// retain test/custom transports while still enforcing timeouts and HTTPS on
// every redirect hop.
func hardenedHTTPClient(client *http.Client, defaultTimeout time.Duration) *http.Client {
	if client == nil {
		client = &http.Client{}
	}
	clone := *client
	if clone.Timeout <= 0 {
		clone.Timeout = defaultTimeout
	}
	previousRedirectPolicy := clone.CheckRedirect
	clone.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if previousRedirectPolicy != nil {
			if err := previousRedirectPolicy(req, via); err != nil {
				return err
			}
		}
		if len(via) >= 10 {
			return fmt.Errorf("too many redirects")
		}
		return validateHTTPSURL(req.URL.String())
	}
	return &clone
}

func validateHTTPSURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	if parsed.Scheme == "https" && parsed.Hostname() != "" {
		return nil
	}
	if parsed.Scheme == "http" && isLoopbackHostname(parsed.Hostname()) {
		return nil
	}
	return fmt.Errorf("URL must use HTTPS")
}

func readMetadataResponse(body io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(body, maxMetadataResponseSize+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxMetadataResponseSize {
		return nil, fmt.Errorf("response exceeds %d-byte limit", maxMetadataResponseSize)
	}
	return data, nil
}

func isLoopbackHostname(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
