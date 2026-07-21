package checker

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHardenedHTTPClientRejectsInsecureRedirect(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://example.com/metadata", http.StatusFound)
	}))
	defer server.Close()

	client := hardenedHTTPClient(server.Client(), time.Second)
	if _, err := client.Get(server.URL); err == nil || !strings.Contains(err.Error(), "URL must use HTTPS") {
		t.Fatalf("expected insecure redirect rejection, got %v", err)
	}
}

func TestReadMetadataResponseEnforcesLimit(t *testing.T) {
	body := strings.NewReader(strings.Repeat("x", int(maxMetadataResponseSize+1)))
	if _, err := readMetadataResponse(body); err == nil {
		t.Fatal("expected oversized metadata response to be rejected")
	}
}
