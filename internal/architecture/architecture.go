// Package architecture identifies native Mac hardware and ranks update artifacts.
package architecture

import (
	"debug/macho"
	"os/exec"
	"runtime"
	"strings"
	"sync"
)

// Native returns the hardware architecture, including when running under Rosetta.
var Native = sync.OnceValue(func() string {
	if runtime.GOOS == "darwin" && runtime.GOARCH == "amd64" {
		out, err := exec.Command("/usr/sbin/sysctl", "-n", "hw.optional.arm64").Output()
		if err == nil && strings.TrimSpace(string(out)) == "1" {
			return "arm64"
		}
	}
	return runtime.GOARCH
})

// Score prefers native, then universal, then unlabeled artifacts. Zero rejects
// an explicitly incompatible architecture. Unlabeled apps need binary validation.
func Score(name, arch string) int {
	arm, intel, universal, other := false, false, false, false
	// Look at each token separately so adjacent architecture names both match.
	for _, token := range strings.FieldsFunc(strings.ReplaceAll(strings.ToLower(name), "x86_64", "amd64"), func(r rune) bool { return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9') }) {
		switch token {
		case "arm64", "arm64e", "aarch64":
			arm = true
		case "amd64", "x64", "intel":
			intel = true
		case "universal", "universal2":
			universal = true
		case "x86", "ia32", "i386", "i686", "armv7", "armv7l":
			other = true
		}
	}
	if universal || arm && intel {
		return 2
	}
	if arch == "arm64" && arm || arch == "amd64" && intel {
		return 3
	}
	if arm || intel || other {
		return 0
	}
	return 1
}

// IntelOnly reports a known Intel-only Mach-O executable. Unreadable files,
// scripts and universal binaries must not trigger migration suggestions.
func IntelOnly(path string) bool {
	if fat, err := macho.OpenFat(path); err == nil {
		defer fat.Close()
		intel := false
		for _, arch := range fat.Arches {
			if arch.Cpu == macho.CpuArm64 {
				return false
			}
			intel = intel || arch.Cpu == macho.CpuAmd64 || arch.Cpu == macho.Cpu386
		}
		return intel
	}
	binary, err := macho.Open(path)
	if err != nil {
		return false
	}
	defer binary.Close()
	return binary.Cpu == macho.CpuAmd64 || binary.Cpu == macho.Cpu386
}
