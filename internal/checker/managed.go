package checker

import (
	"context"

	"github.com/lu-zhengda/updater/internal/app"
)

// ManagedChecker handles apps managed by external platforms (Setapp, JetBrains Toolbox, Adobe CC).
// These platforms manage their own updates, so this checker simply acknowledges the app.
type ManagedChecker struct{}

// NewManagedChecker creates a new ManagedChecker.
func NewManagedChecker() *ManagedChecker {
	return &ManagedChecker{}
}

// Name returns the checker's display name.
func (m *ManagedChecker) Name() string {
	return "managed"
}

// CanCheck returns true if the app is managed by Setapp, JetBrains Toolbox, or Adobe CC.
func (m *ManagedChecker) CanCheck(a *app.App) bool {
	switch a.Source {
	case app.SourceSetapp, app.SourceToolbox, app.SourceAdobe:
		return true
	default:
		return false
	}
}

// Check returns a no-update result. Managed platforms handle their own updates.
func (m *ManagedChecker) Check(_ context.Context, a *app.App) (*UpdateResult, error) {
	return &UpdateResult{
		App:            a,
		Source:         string(a.Source),
		CurrentVersion: a.Version,
		LatestVersion:  a.Version,
		HasUpdate:      false,
	}, nil
}
