package main

import "testing"

func TestIsBrewInstall(t *testing.T) {
	tests := []struct {
		name string
		path string
		want bool
	}{
		{"opt homebrew", "/opt/homebrew/Cellar/updater/1.0/bin/updater", true},
		{"usr local cellar", "/usr/local/Cellar/updater/1.0/bin/updater", true},
		{"linuxbrew", "/home/linuxbrew/.linuxbrew/bin/updater", true},
		{"usr local bin", "/usr/local/bin/updater", false},
		{"home binary", "/Users/user/bin/updater", false},
		{"tmp", "/tmp/updater", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isBrewInstall(tt.path); got != tt.want {
				t.Errorf("isBrewInstall(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}
