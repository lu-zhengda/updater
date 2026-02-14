package tui

import "github.com/charmbracelet/lipgloss"

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

// styledSource returns the source name rendered in its color.
func styledSource(source string) string {
	switch source {
	case "mas":
		return sourceStyleMAS.Render("mas")
	case "sparkle":
		return sourceStyleSparkle.Render("sparkle")
	case "brew":
		return sourceStyleBrew.Render("brew")
	case "brew-info":
		return sourceStyleBrewInfo.Render("brew-info")
	case "github":
		return sourceStyleGitHub.Render("github")
	default:
		return sourceStyleUnknown.Render(source)
	}
}
