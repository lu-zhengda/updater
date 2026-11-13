package checker

import (
	"encoding/json"
	"strings"
	"testing"
)

// FuzzParseBrewInfo checks cask JSON from `brew info --cask --json=v2`.
// A successful parse keeps the version before a comma and uses sha256 only
// when the cask did not say no_check.
func FuzzParseBrewInfo(f *testing.F) {
	f.Add([]byte(`{"casks":[{"token":"updater","version":"1.2.3","url":"https://example.com/Updater.tar.gz","sha256":"abc"}]}`))
	f.Add([]byte(`{"casks":[{"version":"4.60.1,218372","sha256":"no_check","url":"https://example.com/Docker.dmg"}]}`))
	f.Add([]byte(`{"casks":[]}`))
	f.Add([]byte(`{"casks":[{"version":""}]}`))
	f.Add([]byte(`not json`))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<16 {
			t.Skip()
		}
		got, err := parseBrewInfo(data)
		if err != nil {
			return
		}
		var resp brewInfoResponse
		if err := json.Unmarshal(data, &resp); err != nil || len(resp.Casks) == 0 {
			t.Fatalf("parseBrewInfo succeeded without a cask: %v", err)
		}
		wantVer := resp.Casks[0].Version
		if i := strings.IndexByte(wantVer, ','); i >= 0 {
			wantVer = wantVer[:i]
		}
		if got.Version != wantVer || got.Version == "" || strings.Contains(got.Version, ",") {
			t.Fatalf("version %q, want %q", got.Version, wantVer)
		}
		if got.DownloadURL != resp.Casks[0].URL {
			t.Fatalf("url %q, want %q", got.DownloadURL, resp.Casks[0].URL)
		}
		sha := resp.Casks[0].Sha256
		switch {
		case sha == "" || sha == "no_check":
			if got.DownloadDigest != "" {
				t.Fatalf("digest %q for sha256 %q", got.DownloadDigest, sha)
			}
		default:
			if got.DownloadDigest != "sha256:"+sha {
				t.Fatalf("digest %q, want sha256:%s", got.DownloadDigest, sha)
			}
		}
	})
}
