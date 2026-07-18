package main

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/lu-zhengda/updater/internal/app"
)

// joinAppNameArgs joins CLI args into a single app selector string so users can
// pass multi-word app names without shell quoting.
func joinAppNameArgs(args []string) string {
	return strings.TrimSpace(strings.Join(args, " "))
}

// resolveAppSelection resolves a user-provided app selector to a discovered app.
// It supports normalized matching across display name, bundle filename, cask
// candidates, and bundle ID signals.
func resolveAppSelection(apps []*app.App, query string) (*app.App, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("app name is required")
	}

	// Keep exact display-name match highest priority — but names can collide
	// across sources (e.g. a brew formula and a uv tool both named "httpie"),
	// so an exact match must still be unique.
	var exact []*app.App
	for _, a := range apps {
		if strings.EqualFold(a.Name, query) {
			exact = append(exact, a)
		}
	}
	if len(exact) == 1 {
		return exact[0], nil
	}
	if len(exact) > 1 {
		ids := make([]string, 0, len(exact))
		for _, a := range exact {
			ids = append(ids, a.BundleID)
		}
		sort.Strings(ids)
		return nil, fmt.Errorf("app %q is ambiguous (bundle IDs: %s); use --bundle-id to pick one", query, strings.Join(ids, ", "))
	}

	queryNorm := normalizeAppSelector(query)
	if queryNorm == "" {
		return nil, fmt.Errorf("app %q not found", query)
	}

	var matches []*app.App
	for _, a := range apps {
		if appMatchesQuery(a, queryNorm) {
			matches = append(matches, a)
		}
	}

	if len(matches) == 0 {
		return nil, fmt.Errorf("app %q not found", query)
	}
	if len(matches) > 1 {
		names := make([]string, 0, len(matches))
		for _, a := range matches {
			names = append(names, a.Name)
		}
		sort.Strings(names)
		return nil, fmt.Errorf("app %q is ambiguous (matches: %s)", query, strings.Join(names, ", "))
	}

	return matches[0], nil
}

func appMatchesQuery(a *app.App, queryNorm string) bool {
	for _, id := range appLookupIdentifiers(a) {
		if normalizeAppSelector(id) == queryNorm {
			return true
		}
	}
	return false
}

func appLookupIdentifiers(a *app.App) []string {
	seen := make(map[string]bool)
	var ids []string

	add := func(v string) {
		if v == "" || seen[v] {
			return
		}
		seen[v] = true
		ids = append(ids, v)
	}

	add(a.Name)
	if a.Path != "" {
		add(strings.TrimSuffix(filepath.Base(a.Path), ".app"))
	}
	add(a.CaskName)
	add(app.ToCaskName(a.Name))

	for _, cand := range app.CaskCandidates(a) {
		add(cand)
	}

	if a.BundleID != "" {
		add(a.BundleID)
		for _, p := range strings.Split(a.BundleID, ".") {
			add(p)
		}
	}

	return ids
}

func normalizeAppSelector(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}
