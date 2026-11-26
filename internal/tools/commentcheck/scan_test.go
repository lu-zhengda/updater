package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, dir, name, src string) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestNewFilePlainCommentFails(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "p/new.go", "package p\n\nfunc F() {\n\t// a plain note\n}\n")
	empty := t.TempDir()
	writeFile(t, empty, "p/clean.go", "package p\n\nfunc F() {}\n")
	fresh := filepath.Join(t.TempDir(), "baseline.txt")
	if _, err := WriteBaseline(empty, fresh); err != nil {
		t.Fatal(err)
	}
	_, err := Check(root, fresh, "")
	if err == nil || !strings.Contains(err.Error(), "p/new.go has 0, file has 1") {
		t.Fatalf("err = %v", err)
	}
}

func TestAllowedCommentsAreZero(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "p/ok.go", `// Copyright 2026 Example Authors.
// SPDX-License-Identifier: MIT

package p

//go:build unix

// Exported reports the value.
func Exported() int {
	return 1
}

type Box struct {
	Name string // the display name
}

const Label = "ok" // the label

// platform: the wire format is fixed by the peer protocol.
func hidden() {
}

// See https://github.com/example/repo/issues/12 for the original report.
func alsoHidden() {
}
`)
	counts, err := Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(counts) != 0 {
		t.Fatalf("counts = %v, want none", counts)
	}
}

func TestLicenseWordDoesNotExcuseAHeader(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "p/n.go", "// this note is not a license\npackage p\n")
	counts, err := Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	if counts["p/n.go"] != 1 {
		t.Fatalf("count = %d, want 1", counts["p/n.go"])
	}
}

func TestUnexportedAndPlainCommentsCount(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "p/n.go", `package p

// hidden is not part of the API.
func hidden() {}

func Exported() {
	// inside the function
}

// detached from the declaration

func Also() {}
`)
	counts, err := Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	if counts["p/n.go"] != 3 {
		t.Fatalf("count = %d, want 3 (%v)", counts["p/n.go"], counts)
	}
}

func TestCgoFunctionBodyDoesNotCount(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "p/c.go", `package p

/*
#cgo CFLAGS: -x objective-c
#include <stdint.h>
// The window server does not activate this process.
static void updaterActivate(void) {
	doWork();
	doMore();
}
*/
import "C"
`)
	counts, err := Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	if counts["p/c.go"] != 1 {
		t.Fatalf("count = %d, want 1", counts["p/c.go"])
	}
}

func TestStaleHighBaselineFails(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "p/n.go", "package p\n\nfunc F() {\n\t// one\n}\n")
	baseline := filepath.Join(root, "baseline.txt")
	if err := os.WriteFile(baseline, []byte(baselineHeader+"# total: 2\np/n.go 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Check(root, baseline, "")
	if err == nil || !strings.Contains(err.Error(), "baseline: p/n.go has 2, file has 1") {
		t.Fatalf("err = %v", err)
	}
}

func TestRatchetRejectsRaisedBaseline(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "p/n.go", "package p\n\nfunc F() {\n\t// one\n\t// two\n}\n")
	current := filepath.Join(t.TempDir(), "current.txt")
	previous := filepath.Join(t.TempDir(), "previous.txt")
	if _, err := WriteBaseline(root, current); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(previous, []byte(baselineHeader+"# total: 1\np/n.go 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Check(root, current, previous)
	if err == nil || !strings.Contains(err.Error(), "ratchet: p/n.go rose from 1 to 2") {
		t.Fatalf("err = %v", err)
	}
}

func TestRatchetAllowsALoweredCount(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "p/n.go", "package p\n\nfunc F() {\n\t// one\n}\n")
	current := filepath.Join(t.TempDir(), "current.txt")
	previous := filepath.Join(t.TempDir(), "previous.txt")
	if _, err := WriteBaseline(root, current); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(previous, []byte(baselineHeader+"# total: 2\np/n.go 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Check(root, current, previous); err != nil {
		t.Fatal(err)
	}
}

func TestBaselineTotalMustMatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "baseline.txt")
	if err := os.WriteFile(path, []byte("# total: 3\np/n.go 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readBaseline(path); err == nil {
		t.Fatal("expected a total mismatch")
	}
}
