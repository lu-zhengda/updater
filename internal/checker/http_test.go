package checker

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lu-zhengda/updater/internal/useragent"
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

func TestHardenedHTTPClientSetsUserAgent(t *testing.T) {
	var gotUA string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := hardenedHTTPClient(server.Client(), time.Second)
	resp, err := client.Get(server.URL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	resp.Body.Close()

	if useragent.IsGoDefault(gotUA) {
		t.Fatalf("User-Agent %q is empty or Go default", gotUA)
	}
	if gotUA != useragent.String() {
		t.Fatalf("User-Agent = %q, want %q", gotUA, useragent.String())
	}
}

func TestReadMetadataResponseEnforcesLimit(t *testing.T) {
	body := strings.NewReader(strings.Repeat("x", int(maxMetadataResponseSize+1)))
	if _, err := readMetadataResponse(body); err == nil {
		t.Fatal("expected oversized metadata response to be rejected")
	}
}
