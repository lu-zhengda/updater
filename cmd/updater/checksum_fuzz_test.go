package main

import (
	"crypto/sha256"
	"strings"
	"testing"
)

// FuzzChecksumForAsset checks checksums.txt lines used by downloadSelfArchive.
// A returned digest is 64 hex characters and is the first matching line.
func FuzzChecksumForAsset(f *testing.F) {
	sum := strings.Repeat("ab", 32)
	f.Add([]byte(sum+"  updater_1.2.3_darwin.tar.gz\n"), "updater_1.2.3_darwin.tar.gz")
	f.Add([]byte(strings.Repeat("g", 64)+"  updater_1.2.3_darwin.tar.gz\n"), "updater_1.2.3_darwin.tar.gz")
	f.Add([]byte("nope\n"), "updater_1.2.3_darwin.tar.gz")
	f.Fuzz(func(t *testing.T, data []byte, name string) {
		if len(data) > 1<<16 || len(name) > 200 {
			t.Skip()
		}
		got, err := checksumForAsset(data, name)
		if err != nil {
			return
		}
		if len(got) != sha256.Size*2 {
			t.Fatalf("len %d: %q", len(got), got)
		}
		for _, r := range got {
			if !strings.ContainsRune("0123456789abcdef", r) {
				t.Fatalf("non-hex digest %q", got)
			}
		}
		found := false
		for _, line := range strings.Split(string(data), "\n") {
			fields := strings.Fields(line)
			if len(fields) == 2 && fields[1] == name {
				if strings.ToLower(fields[0]) != got {
					t.Fatalf("returned %q, first line %q", got, fields[0])
				}
				found = true
				break
			}
		}
		if !found {
			t.Fatal("returned a digest with no matching line")
		}
	})
}
