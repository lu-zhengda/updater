package main

import (
	"os"
	"testing"
)

func unsetEnv(t *testing.T, key string) {
	t.Helper()

	old, had := os.LookupEnv(key)
	if err := os.Unsetenv(key); err != nil {
		t.Fatalf("unset %s: %v", key, err)
	}
	t.Cleanup(func() {
		var err error
		if had {
			err = os.Setenv(key, old)
		} else {
			err = os.Unsetenv(key)
		}
		if err != nil {
			t.Fatalf("restore %s: %v", key, err)
		}
	})
}

func TestAgentModeEnabled_OverrideFalseWins(t *testing.T) {
	t.Setenv(agentModeEnv, "0")
	t.Setenv("CODEX_CI", "1")

	if agentModeEnabled() {
		t.Fatal("expected agent mode disabled when override is false")
	}
}

func TestAgentModeEnabled_OverrideTrueWins(t *testing.T) {
	t.Setenv(agentModeEnv, "1")
	t.Setenv("CODEX_CI", "")
	t.Setenv("CLAUDECODE", "")

	if !agentModeEnabled() {
		t.Fatal("expected agent mode enabled when override is true")
	}
}

func TestAgentModeEnabled_CodexEnvDetected(t *testing.T) {
	unsetEnv(t, agentModeEnv)
	t.Setenv("CODEX_CI", "1")

	if !agentModeEnabled() {
		t.Fatal("expected agent mode enabled for Codex environment")
	}
}

func TestAgentModeEnabled_ClaudeEnvDetected(t *testing.T) {
	unsetEnv(t, agentModeEnv)
	for _, k := range codexAgentEnvVars {
		t.Setenv(k, "")
	}
	t.Setenv("__CFBundleIdentifier", "")
	t.Setenv("CLAUDE_CODE", "1")

	if !agentModeEnabled() {
		t.Fatal("expected agent mode enabled for Claude Code environment")
	}
}

func TestJSONOutputEnabled(t *testing.T) {
	t.Setenv(agentModeEnv, "0")
	if !jsonOutputEnabled(true) {
		t.Fatal("explicit --json should enable JSON output")
	}
	if jsonOutputEnabled(false) {
		t.Fatal("expected JSON output disabled with no flag and agent mode off")
	}

	t.Setenv(agentModeEnv, "1")
	if !jsonOutputEnabled(false) {
		t.Fatal("expected JSON output enabled in agent mode")
	}
}
