package architecture

import "testing"

func TestScore(t *testing.T) {
	for _, tc := range []struct {
		name       string
		arm, intel int
	}{
		{"Notion-arm64-7.34.zip", 3, 0},
		{"app-aarch64.dmg", 3, 0},
		{"App-x86_64.dmg", 0, 3},
		{"App-X86_64.dmg", 0, 3},
		{"app-X64.zip", 0, 3},
		{"app-intel.pkg", 0, 3},
		{"app-universal2.dmg", 2, 2},
		{"app-arm64-x64.zip", 2, 2},
		{"app-i386.zip", 0, 0},
		{"Notion-7.34.zip", 1, 1},
	} {
		for arch, want := range map[string]int{"arm64": tc.arm, "amd64": tc.intel} {
			if got := Score(tc.name, arch); got != want {
				t.Errorf("Score(%q, %q) = %d, want %d", tc.name, arch, got, want)
			}
		}
	}
}
