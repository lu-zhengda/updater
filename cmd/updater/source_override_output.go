package main

import (
	"fmt"

	"github.com/lu-zhengda/updater/internal/app"
	"github.com/lu-zhengda/updater/internal/checker"
)

type sourceOverrideJSON struct {
	SourceOverride     bool   `json:"source_override,omitempty"`
	SourceOverrideKind string `json:"source_override_kind,omitempty"`
}

func sourceOverrideFieldsFromApp(a *app.App) sourceOverrideJSON {
	if a == nil || !a.SourceOverrideActive {
		return sourceOverrideJSON{}
	}
	return sourceOverrideJSON{
		SourceOverride:     true,
		SourceOverrideKind: a.SourceOverrideKind,
	}
}

func sourceOverrideFieldsFromResult(r *checker.UpdateResult) sourceOverrideJSON {
	if r == nil || !r.SourceOverrideActive {
		return sourceOverrideJSON{}
	}
	return sourceOverrideJSON{
		SourceOverride:     true,
		SourceOverrideKind: r.SourceOverrideKind,
	}
}

func canonicalSourceLabel(source string, sourceOverride bool) string {
	if !sourceOverride {
		return source
	}
	return fmt.Sprintf("%s (override)", source)
}

func checkSourceLabel(source string, sourceOverride bool) string {
	if sourceOverride {
		return canonicalSourceLabel(source, true)
	}
	return cliSourceName(source)
}
