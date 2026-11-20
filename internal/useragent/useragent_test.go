package useragent

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStringUsesVersion(t *testing.T) {
	orig := Version
	t.Cleanup(func() { Version = orig })

	Version = "0.20.0"
	got := String()
	want := "updater/0.20.0 (+https://github.com/lu-zhengda/updater)"
	if got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}

	Version = ""
	if !strings.HasPrefix(String(), "updater/dev ") {
		t.Fatalf("empty Version should fall back to dev, got %q", String())
	}
}

func TestWrapClientSetsUserAgent(t *testing.T) {
	orig := Version
	Version = "0.20.0"
	t.Cleanup(func() { Version = orig })

	var gotUA string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	client := WrapClient(server.Client())
	resp, err := client.Get(server.URL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	resp.Body.Close()

	if IsGoDefault(gotUA) {
		t.Fatalf("User-Agent %q is empty or Go default", gotUA)
	}
	if gotUA != String() {
		t.Fatalf("User-Agent = %q, want %q", gotUA, String())
	}
}

func TestWrapClientPreservesCallerUserAgent(t *testing.T) {
	var gotUA string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	client := WrapClient(server.Client())
	req, err := http.NewRequest(http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("User-Agent", "custom-agent/1.0")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	resp.Body.Close()

	if gotUA != "custom-agent/1.0" {
		t.Fatalf("User-Agent = %q, want caller-set custom-agent/1.0", gotUA)
	}
}

func TestIsGoDefault(t *testing.T) {
	for _, tt := range []struct {
		ua   string
		want bool
	}{
		{"", true},
		{"   ", true},
		{"Go-http-client/1.1", true},
		{"Go-http-client/2.0", true},
		{"updater/0.20.0 (+https://github.com/lu-zhengda/updater)", false},
		{"Burn/3.1.10 Sparkle/2.6", false},
	} {
		if got := IsGoDefault(tt.ua); got != tt.want {
			t.Errorf("IsGoDefault(%q) = %v, want %v", tt.ua, got, tt.want)
		}
	}
}
