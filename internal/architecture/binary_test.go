package architecture

import (
	"bytes"
	"debug/macho"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func TestIntelOnly(t *testing.T) {
	thin := func(cpu macho.Cpu) []byte {
		var buf bytes.Buffer
		binary.Write(&buf, binary.LittleEndian, []uint32{macho.Magic64, uint32(cpu), 3, 2, 0, 0, 0, 0})
		return buf.Bytes()
	}
	var fat bytes.Buffer
	binary.Write(&fat, binary.BigEndian, []uint32{macho.MagicFat, 2})
	binary.Write(&fat, binary.BigEndian, []uint32{uint32(macho.CpuAmd64), 3, 48, 32, 0})
	binary.Write(&fat, binary.BigEndian, []uint32{uint32(macho.CpuArm64), 3, 80, 32, 0})
	fat.Write(thin(macho.CpuAmd64))
	fat.Write(thin(macho.CpuArm64))
	for _, tc := range []struct {
		name string
		data []byte
		want bool
	}{
		{"intel", thin(macho.CpuAmd64), true},
		{"arm", thin(macho.CpuArm64), false},
		{"universal", fat.Bytes(), false},
		{"script", []byte("#!/bin/sh\n"), false},
		{"truncated", []byte{0xcf, 0xfa, 0xed, 0xfe}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "binary")
			if err := os.WriteFile(path, tc.data, 0o600); err != nil {
				t.Fatal(err)
			}
			if got := IntelOnly(path); got != tc.want {
				t.Fatalf("IntelOnly = %v, want %v", got, tc.want)
			}
		})
	}
	if IntelOnly(filepath.Join(t.TempDir(), "missing")) {
		t.Fatal("missing binary detected as Intel")
	}
}
