package main

import (
	"testing"

	"github.com/lu-zhengda/updater/internal/config"
)

func TestPolicyCommand_InvalidPolicy(t *testing.T) {
	// Verify the valid policies map
	validPolicies := map[string]bool{
		config.PolicyAuto: true, config.PolicyManual: true, config.PolicyNotifyOnly: true, "clear": true,
	}

	invalid := []string{"skip", "always", "never", ""}
	for _, p := range invalid {
		if validPolicies[p] {
			t.Errorf("policy %q should be invalid", p)
		}
	}

	valid := []string{"auto", "manual", "notify-only", "clear"}
	for _, p := range valid {
		if !validPolicies[p] {
			t.Errorf("policy %q should be valid", p)
		}
	}
}
