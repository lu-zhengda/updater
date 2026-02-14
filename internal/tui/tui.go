package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/luzhengda/updater/internal/app"
	"github.com/luzhengda/updater/internal/checker"
)

// CheckFunc checks all apps for updates and returns results.
type CheckFunc func(ctx context.Context, apps []*app.App) []*checker.UpdateResult

// UpdateFunc executes an update for a single app.
type UpdateFunc func(ctx context.Context, result *checker.UpdateResult) error

// row represents a single row in the TUI table.
type row struct {
	app      *app.App
	result   *checker.UpdateResult
	checked  bool
	updating bool
}

// Messages sent by background operations.
type checkDoneMsg struct {
	results []*checker.UpdateResult
}

type updateDoneMsg struct {
	index int
	err   error
}

// Model is the main Bubbletea model for the updater TUI.
type Model struct {
	apps      []*app.App
	rows      []row
	cursor    int
	offset    int // scroll offset for the viewport
	checking  bool
	updating  map[int]bool
	ignored   map[int]bool
	width     int
	height    int
	spinner   spinner.Model
	checkFn   CheckFunc
	updateFn  UpdateFunc
	statusMsg string
}

// NewModel creates a new TUI model with the given apps and callback functions.
func NewModel(apps []*app.App, checkFn CheckFunc, updateFn UpdateFunc) Model {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(colorCyan)

	rows := make([]row, len(apps))
	for i, a := range apps {
		rows[i] = row{app: a}
	}

	return Model{
		apps:     apps,
		rows:     rows,
		checking: true,
		updating: make(map[int]bool),
		ignored:  make(map[int]bool),
		spinner:  s,
		checkFn:  checkFn,
		updateFn: updateFn,
	}
}

// Init starts the spinner and kicks off the background check.
func (m Model) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, m.startCheck())
}

// startCheck returns a command that runs the check function in the background.
func (m Model) startCheck() tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		results := m.checkFn(ctx, m.apps)
		return checkDoneMsg{results: results}
	}
}

// startUpdate returns a command that runs the update function for a single row.
func (m Model) startUpdate(index int) tea.Cmd {
	result := m.rows[index].result
	return func() tea.Msg {
		ctx := context.Background()
		err := m.updateFn(ctx, result)
		return updateDoneMsg{index: index, err: err}
	}
}

// Update handles all messages.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return m.handleKey(msg)

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.adjustOffset()
		return m, nil

	case spinner.TickMsg:
		if m.checking || len(m.updating) > 0 {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}
		return m, nil

	case checkDoneMsg:
		m.checking = false
		m.applyResults(msg.results)
		m.statusMsg = fmt.Sprintf("Checked %d apps", len(msg.results))
		return m, nil

	case updateDoneMsg:
		delete(m.updating, msg.index)
		m.rows[msg.index].updating = false
		if msg.err != nil {
			m.statusMsg = fmt.Sprintf("Update failed for %s: %v", m.rows[msg.index].app.Name, msg.err)
		} else {
			m.statusMsg = fmt.Sprintf("Updated %s successfully", m.rows[msg.index].app.Name)
			// Mark as no longer having an update after success.
			if m.rows[msg.index].result != nil {
				m.rows[msg.index].result.HasUpdate = false
			}
		}
		// Keep spinner ticking if there are still active updates.
		if len(m.updating) > 0 {
			return m, m.spinner.Tick
		}
		return m, nil
	}

	return m, nil
}

// handleKey processes keyboard input.
func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlC:
		return m, tea.Quit
	case tea.KeyUp:
		m.moveCursor(-1)
		return m, nil
	case tea.KeyDown:
		m.moveCursor(1)
		return m, nil
	case tea.KeyEnter:
		return m.handleUpdate()
	case tea.KeyRunes:
		switch string(msg.Runes) {
		case "q":
			return m, tea.Quit
		case "j":
			m.moveCursor(1)
			return m, nil
		case "k":
			m.moveCursor(-1)
			return m, nil
		case "a":
			return m.handleUpdateAll()
		case "i":
			m.toggleIgnore()
			return m, nil
		case "r":
			return m.handleRefresh()
		}
	}
	return m, nil
}

// moveCursor moves the cursor up or down by delta, clamping to valid range
// and adjusting the scroll offset to keep the cursor visible.
func (m *Model) moveCursor(delta int) {
	if len(m.rows) == 0 {
		return
	}
	m.cursor += delta
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor >= len(m.rows) {
		m.cursor = len(m.rows) - 1
	}
	m.adjustOffset()
}

// adjustOffset ensures the cursor is visible within the viewport.
func (m *Model) adjustOffset() {
	visible := m.visibleRows()
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+visible {
		m.offset = m.cursor - visible + 1
	}
	// Clamp offset.
	maxOffset := len(m.rows) - visible
	if maxOffset < 0 {
		maxOffset = 0
	}
	if m.offset > maxOffset {
		m.offset = maxOffset
	}
	if m.offset < 0 {
		m.offset = 0
	}
}

// visibleRows returns how many table rows fit in the viewport.
func (m Model) visibleRows() int {
	// Reserve lines for: header (2), column headers (1), status bar (2), padding (1).
	const reservedLines = 6
	visible := m.height - reservedLines
	if visible < 1 {
		visible = 1
	}
	return visible
}

// toggleIgnore toggles the ignored state of the currently selected app.
func (m *Model) toggleIgnore() {
	if len(m.rows) == 0 {
		return
	}
	if m.ignored[m.cursor] {
		delete(m.ignored, m.cursor)
		m.statusMsg = fmt.Sprintf("Unignored %s", m.rows[m.cursor].app.Name)
	} else {
		m.ignored[m.cursor] = true
		m.statusMsg = fmt.Sprintf("Ignored %s", m.rows[m.cursor].app.Name)
	}
}

// handleUpdate starts an update for the currently selected app.
func (m Model) handleUpdate() (tea.Model, tea.Cmd) {
	if len(m.rows) == 0 || m.checking {
		return m, nil
	}
	idx := m.cursor
	r := m.rows[idx]
	if r.result == nil || !r.result.HasUpdate || r.result.Error != nil {
		m.statusMsg = "No update available for this app"
		return m, nil
	}
	if m.updating[idx] || m.ignored[idx] {
		return m, nil
	}
	m.updating[idx] = true
	m.rows[idx].updating = true
	m.statusMsg = fmt.Sprintf("Updating %s...", r.app.Name)
	return m, tea.Batch(m.startUpdate(idx), m.spinner.Tick)
}

// handleUpdateAll starts updates for all apps with available updates.
func (m Model) handleUpdateAll() (tea.Model, tea.Cmd) {
	if m.checking {
		return m, nil
	}
	var cmds []tea.Cmd
	count := 0
	for i, r := range m.rows {
		if r.result == nil || !r.result.HasUpdate || r.result.Error != nil {
			continue
		}
		if m.updating[i] || m.ignored[i] {
			continue
		}
		m.updating[i] = true
		m.rows[i].updating = true
		cmds = append(cmds, m.startUpdate(i))
		count++
	}
	if count == 0 {
		m.statusMsg = "No updates available"
		return m, nil
	}
	m.statusMsg = fmt.Sprintf("Updating %d apps...", count)
	cmds = append(cmds, m.spinner.Tick)
	return m, tea.Batch(cmds...)
}

// handleRefresh re-checks all apps for updates.
func (m Model) handleRefresh() (tea.Model, tea.Cmd) {
	if m.checking {
		return m, nil
	}
	m.checking = true
	// Reset results.
	for i := range m.rows {
		m.rows[i].result = nil
		m.rows[i].checked = false
	}
	m.statusMsg = "Refreshing..."
	return m, tea.Batch(m.startCheck(), m.spinner.Tick)
}

// applyResults matches check results back to rows by app pointer.
func (m *Model) applyResults(results []*checker.UpdateResult) {
	resultMap := make(map[*app.App]*checker.UpdateResult, len(results))
	for _, r := range results {
		resultMap[r.App] = r
	}
	for i, r := range m.rows {
		if result, ok := resultMap[r.app]; ok {
			m.rows[i].result = result
			m.rows[i].checked = true
		}
	}
}

// View renders the TUI.
func (m Model) View() string {
	var b strings.Builder

	// Header.
	b.WriteString(styleHeader.Render("macOS App Updater"))
	b.WriteString("\n")

	if m.checking {
		b.WriteString(m.spinner.View())
		b.WriteString(" Checking for updates...\n")
		return b.String()
	}

	visible := m.visibleRows()

	// Column headers.
	b.WriteString(renderColumnHeaders(m.width))
	b.WriteString("\n")

	// Render visible rows based on current offset.
	end := m.offset + visible
	if end > len(m.rows) {
		end = len(m.rows)
	}
	for i := m.offset; i < end; i++ {
		b.WriteString(m.renderRow(i, i == m.cursor))
		b.WriteString("\n")
	}

	// Scroll indicator.
	if len(m.rows) > visible {
		b.WriteString(lipgloss.NewStyle().Foreground(colorGray).Render(
			fmt.Sprintf("  showing %d-%d of %d", m.offset+1, end, len(m.rows))))
		b.WriteString("\n")
	}

	// Status bar.
	b.WriteString(m.renderStatusBar())

	return b.String()
}

// renderColumnHeaders renders the table column headers.
func renderColumnHeaders(width int) string {
	nameW, curW, latW, srcW := columnWidths(width)

	return fmt.Sprintf("  %s  %s  %s  %s  %s",
		styleColumnHeader.Render(pad("NAME", nameW)),
		styleColumnHeader.Render(pad("CURRENT", curW)),
		styleColumnHeader.Render(pad("LATEST", latW)),
		styleColumnHeader.Render(pad("SOURCE", srcW)),
		styleColumnHeader.Render("STATUS"),
	)
}

// renderRow renders a single table row.
func (m Model) renderRow(index int, isCursor bool) string {
	r := m.rows[index]
	nameW, curW, latW, srcW := columnWidths(m.width)

	// Cursor indicator.
	cursor := "  "
	if isCursor {
		cursor = styleCursor.Render("> ")
	}

	name := truncate(r.app.Name, nameW)
	current := truncate(r.app.Version, curW)
	latest := ""
	rawSource := ""
	status := ""

	if r.updating {
		status = styleUpdating.Render(m.spinner.View() + " updating")
	} else if !r.checked {
		status = styleSkipped.Render("pending")
	} else if r.result == nil {
		status = styleSkipped.Render("skipped")
	} else if r.result.Error != nil {
		status = styleError.Render("error")
		rawSource = r.result.Source
	} else if r.result.HasUpdate {
		latest = r.result.LatestVersion
		rawSource = r.result.Source
		status = styleUpdate.Render("update available")
	} else {
		latest = r.result.LatestVersion
		rawSource = r.result.Source
		status = styleUpToDate.Render("up to date")
	}

	latest = truncate(latest, latW)

	// Pad source before styling so ANSI codes don't affect width.
	source := styledSource(rawSource) + strings.Repeat(" ", max(0, srcW-len(rawSource)))

	line := fmt.Sprintf("%s%s  %s  %s  %s  %s",
		cursor,
		pad(name, nameW),
		pad(current, curW),
		pad(latest, latW),
		source,
		status,
	)

	if m.ignored[index] {
		line = styleIgnored.Render(line)
	}

	return line
}

// renderStatusBar renders the bottom status bar with key help and status message.
func (m Model) renderStatusBar() string {
	help := styleStatusBar.Render(
		"j/k: navigate | enter: update | a: update all | i: ignore | r: refresh | q: quit")

	var msg string
	if m.statusMsg != "" {
		msg = "\n" + lipgloss.NewStyle().Foreground(colorWhite).Render(m.statusMsg)
	}

	return help + msg
}

// columnWidths returns proportional widths for each column based on terminal width.
func columnWidths(width int) (name, current, latest, source int) {
	if width < 80 {
		width = 80
	}
	// Subtract cursor (2) + gaps between columns (4*2=8) = 10 chars overhead.
	// Plus STATUS column gets the rest, so we don't allocate for it.
	available := width - 10 - 18 // 18 for status text approximate
	if available < 40 {
		available = 40
	}

	// Proportional allocation: name 40%, current 20%, latest 20%, source 20%.
	name = available * 40 / 100
	current = available * 20 / 100
	latest = available * 20 / 100
	source = available * 20 / 100

	// Clamp minimum widths.
	if name < 10 {
		name = 10
	}
	if current < 8 {
		current = 8
	}
	if latest < 8 {
		latest = 8
	}
	if source < 7 {
		source = 7
	}

	return name, current, latest, source
}

// pad right-pads a string to width w with spaces.
func pad(s string, w int) string {
	if len(s) >= w {
		return s
	}
	return s + strings.Repeat(" ", w-len(s))
}

// truncate truncates a string to max length, appending "..." if truncated.
func truncate(s string, max int) string {
	if max < 4 {
		max = 4
	}
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}
