package checker

import (
	"encoding/json"
	"strings"
	"testing"
)

// FuzzGitHubRelease parses release JSON the way the checker and the
// self-update helper do. A parsed tag keeps a single listed prefix removed,
// and parsing the same bytes twice yields the same tag and asset fields.
func FuzzGitHubRelease(f *testing.F) {
	f.Add([]byte(`{"tag_name":"v1.2.3","name":"1.2.3","body":"notes","assets":[{"name":"updater_1.2.3_darwin.tar.gz","browser_download_url":"https://example.com/a.tar.gz","digest":"sha256:ab"}]}`))
	f.Add([]byte(`{"tag_name":"version-1.2.3"}`))
	f.Add([]byte(`{"tag_name":"release/3.5.4","assets":[]}`))
	f.Add([]byte(`not json`))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<16 {
			t.Skip()
		}
		var release GitHubRelease
		if err := json.Unmarshal(data, &release); err != nil {
			return
		}
		if got := CleanTagVersion(release.TagName); got != cleanTagOnce(release.TagName) {
			t.Fatalf("CleanTagVersion(%q) = %q, want %q", release.TagName, got, cleanTagOnce(release.TagName))
		}
		again, err := json.Marshal(release)
		if err != nil {
			t.Fatal(err)
		}
		var round GitHubRelease
		if err := json.Unmarshal(again, &round); err != nil {
			t.Fatal(err)
		}
		if round.TagName != release.TagName || round.Name != release.Name || round.Body != release.Body {
			t.Fatalf("release fields changed: %+v then %+v", release, round)
		}
		if len(round.Assets) != len(release.Assets) {
			t.Fatalf("asset count %d then %d", len(release.Assets), len(round.Assets))
		}
		for i := range release.Assets {
			if round.Assets[i] != release.Assets[i] {
				t.Fatalf("asset %d: %+v then %+v", i, release.Assets[i], round.Assets[i])
			}
		}
	})
}

// cleanTagOnce is the property, not a second implementation of the scanner:
// the first matching prefix in longest-first order is the only one removed.
func cleanTagOnce(tag string) string {
	prefixes := []string{"version-", "version/", "release-", "release/", "ver-", "ver/", "v"}
	for _, p := range prefixes {
		if after, ok := strings.CutPrefix(tag, p); ok {
			return after
		}
	}
	return tag
}
