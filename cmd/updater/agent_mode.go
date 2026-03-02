package main

import (
	"os"
	"strings"
)

const agentModeEnv = "UPDATER_AGENT_MODE"

var codexAgentEnvVars = []string{
	"CODEX_HOME",
	"CODEX_CI",
	"CODEX_SHELL",
	"CODEX_THREAD_ID",
}

var claudeAgentEnvVars = []string{
	"CLAUDECODE",
	"CLAUDE_CODE",
	"CLAUDECODE_SESSION",
	"CLAUDE_CODE_SESSION",
}

// agentModeEnabled reports whether updater should default to machine-readable
// JSON output for commands that support it.
//
// Priority:
// 1) UPDATER_AGENT_MODE explicit override (truthy/falsy).
// 2) Known coding-agent environment signals (Codex / Claude Code).
func agentModeEnabled() bool {
	if v, ok := os.LookupEnv(agentModeEnv); ok {
		return parseAgentModeOverride(v)
	}

	if hasAnyEnv(codexAgentEnvVars) || hasAnyEnv(claudeAgentEnvVars) {
		return true
	}

	// Codex desktop bundles set this on macOS.
	return strings.EqualFold(strings.TrimSpace(os.Getenv("__CFBundleIdentifier")), "com.openai.codex")
}

func parseAgentModeOverride(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "0", "false", "off", "no":
		return false
	default:
		return true
	}
}

func hasAnyEnv(keys []string) bool {
	for _, k := range keys {
		if strings.TrimSpace(os.Getenv(k)) != "" {
			return true
		}
	}
	return false
}

func jsonOutputEnabled(explicitJSONFlag bool) bool {
	return explicitJSONFlag || agentModeEnabled()
}
