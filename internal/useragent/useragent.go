// Package useragent sets updater's outbound HTTP User-Agent in one place.
package useragent

import (
	"fmt"
	"net/http"
	"strings"
)

// Version is the build-time updater version. GoReleaser and the Makefile set it
// via -ldflags; it falls back to "dev" for local builds and tests.
var Version = "dev"

const repoURL = "https://github.com/lu-zhengda/updater"

// String returns the canonical User-Agent for outbound HTTP requests.
func String() string {
	v := Version
	if v == "" {
		v = "dev"
	}
	return fmt.Sprintf("updater/%s (+%s)", v, repoURL)
}

// IsGoDefault reports whether ua is empty or Go's default client User-Agent.
func IsGoDefault(ua string) bool {
	ua = strings.TrimSpace(ua)
	return ua == "" || strings.HasPrefix(ua, "Go-http-client/")
}

type roundTripper struct {
	base http.RoundTripper
}

func (rt roundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	base := rt.base
	if base == nil {
		base = http.DefaultTransport
	}
	// Preserve a User-Agent the caller set deliberately.
	if req.Header.Get("User-Agent") != "" {
		return base.RoundTrip(req)
	}
	clone := req.Clone(req.Context())
	clone.Header.Set("User-Agent", String())
	return base.RoundTrip(clone)
}

// WrapClient returns a shallow clone of client whose Transport sets updater's
// User-Agent when the request does not already have one. A nil client is treated
// as a new http.Client. Already-wrapped transports are left alone.
func WrapClient(client *http.Client) *http.Client {
	if client == nil {
		client = &http.Client{}
	}
	clone := *client
	if _, ok := clone.Transport.(roundTripper); ok {
		return &clone
	}
	clone.Transport = roundTripper{base: clone.Transport}
	return &clone
}
