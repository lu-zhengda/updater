package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Color palette.
var (
	colorGreen  = lipgloss.Color("2")
	colorYellow = lipgloss.Color("3")
	colorRed    = lipgloss.Color("1")
	colorGray   = lipgloss.Color("8")
	colorBlue   = lipgloss.Color("4")
	colorOrange = lipgloss.Color("208")
	colorWhite  = lipgloss.Color("15")
	colorCyan   = lipgloss.Color("6")
)

// Status styles.
var (
	styleUpToDate = lipgloss.NewStyle().Foreground(colorGreen)
	styleUpdate   = lipgloss.NewStyle().Foreground(colorYellow)
	styleError    = lipgloss.NewStyle().Foreground(colorRed)
	styleSkipped  = lipgloss.NewStyle().Foreground(colorGray)
)

// Source label styles.
var (
	sourceStyleMAS     = lipgloss.NewStyle().Foreground(colorBlue)
	sourceStyleSparkle = lipgloss.NewStyle().Foreground(colorOrange)
	sourceStyleBrew    = lipgloss.NewStyle().Foreground(colorGreen)
	sourceStyleGitHub  = lipgloss.NewStyle().Foreground(colorWhite)
	sourceStyleUnknown = lipgloss.NewStyle().Foreground(colorGray)
)

// Layout styles.
var (
	styleHeader = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorCyan).
			PaddingBottom(1)

	styleStatusBar = lipgloss.NewStyle().
			Foreground(colorGray).
			PaddingTop(1)

	styleCursor = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorCyan)

	styleIgnored = lipgloss.NewStyle().
			Foreground(colorGray).
			Strikethrough(true)

	styleUpdating = lipgloss.NewStyle().
			Foreground(colorYellow).
			Bold(true)

	styleColumnHeader = lipgloss.NewStyle().
				Bold(true).
				Underline(true)
)

// Source label style for brew-info.
var sourceStyleBrewInfo = lipgloss.NewStyle().Foreground(colorCyan)

// Source label style for system updates.
var sourceStyleSystem = lipgloss.NewStyle().Foreground(colorWhite).Bold(true)

// Status style for pinned apps.
var stylePinned = lipgloss.NewStyle().Foreground(colorBlue).Bold(true)

// styledSource returns the source name rendered in its color.
func styledSource(source string) string {
	// Handle +brew suffix: style the base source, then append styled "+brew".
	if strings.HasSuffix(source, "+brew") {
		base := strings.TrimSuffix(source, "+brew")
		return styledSource(base) + sourceStyleBrew.Render("+brew")
	}
	switch source {
	case "mas":
		return sourceStyleMAS.Render("app store")
	case "sparkle":
		return sourceStyleSparkle.Render("sparkle")
	case "brew":
		return sourceStyleBrew.Render("homebrew")
	case "brew-info":
		return sourceStyleBrewInfo.Render("homebrew")
	case "github":
		return sourceStyleGitHub.Render("github")
	case "system":
		return sourceStyleSystem.Render("system")
	case "unknown":
		return sourceStyleUnknown.Render("unknown")
	default:
		return sourceStyleUnknown.Render(source)
	}
}

// sourceDisplayName returns the display name for a source (without ANSI styling).
// Used for padding calculations since display names differ from internal names.
func sourceDisplayName(source string) string {
	if strings.HasSuffix(source, "+brew") {
		base := strings.TrimSuffix(source, "+brew")
		return sourceDisplayName(base) + "+brew"
	}
	switch source {
	case "mas":
		return "app store"
	case "brew", "brew-info":
		return "homebrew"
	default:
		return source
	}
}
