package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/luzhengda/updater/internal/app"
	"github.com/luzhengda/updater/internal/checker"
	"github.com/luzhengda/updater/internal/config"
)

// CheckFunc checks all apps for updates and returns results.
type CheckFunc func(ctx context.Context, apps []*app.App) []*checker.UpdateResult

// UpdateFunc executes an update for a single app.
type UpdateFunc func(ctx context.Context, result *checker.UpdateResult) error

// LoadResult is returned by LoadFunc with the discovered and enriched apps.
type LoadResult struct {
	Apps      []*app.App
	PinnedIDs map[string]bool
}

// LoadFunc discovers and enriches apps. Called in the background so the TUI launches instantly.
type LoadFunc func(ctx context.Context) (*LoadResult, error)

// ScheduleStatus represents the current state of scheduled update checks.
type ScheduleStatus struct {
	Enabled       bool
	IntervalHours int
}

// ScheduleFuncs provides callbacks for managing the update schedule from the TUI.
type ScheduleFuncs struct {
	Check   func() ScheduleStatus
	Install func(ctx context.Context, intervalHours int) error
	Remove  func(ctx context.Context) error
}

// row represents a single row in the TUI table.
type row struct {
	app      *app.App
	result   *checker.UpdateResult
	checked  bool
	updating bool
}

// Messages sent by background operations.
type loadDoneMsg struct {
	result *LoadResult
	err    error
}

type checkDoneMsg struct {
	results []*checker.UpdateResult
}

type updateDoneMsg struct {
	index int
	err   error
}

type scheduleCheckMsg struct{ status ScheduleStatus }
type scheduleInstallMsg struct {
	err   error
	hours int
}
type scheduleRemoveMsg struct{ err error }

// Model is the main Bubbletea model for the updater TUI.
type Model struct {
	apps       []*app.App
	rows       []row
	cursor     int
	offset     int // scroll offset for the viewport
	loading    bool // true while discovering/enriching apps
	checking   bool
	updating   map[int]bool
	ignored    map[int]bool
	pinned     map[int]bool
	pinnedIDs  map[string]bool
	width      int
	height     int
	spinner    spinner.Model
	loadFn     LoadFunc
	checkFn    CheckFunc
	updateFn   UpdateFunc
	selected       map[int]bool // row indices toggled for batch update
	showAll        bool         // true = show everything; false = actionable only
	visible        []int        // indices into m.rows for currently displayed items
	statusMsg      string
	showDetail     bool
	detailIdx      int
	detailViewport viewport.Model
	scheduleFns    *ScheduleFuncs
	scheduleStatus ScheduleStatus
	schedulePrompt bool   // first-launch prompt overlay
	showSchedule   bool   // schedule settings modal
	cfg            *config.Config
	cfgPath        string
	searchMode     bool
	searchInput    textinput.Model
	searchQuery    string
}

// NewModel creates a new TUI model that launches instantly.
// loadFn runs in the background to discover and enrich apps.
// checkFn runs after loading to check for updates.
// updateFn executes updates for individual apps.
// scheduleFns, cfg, and cfgPath are optional — pass nil/empty to disable scheduler UI.
func NewModel(loadFn LoadFunc, checkFn CheckFunc, updateFn UpdateFunc, scheduleFns *ScheduleFuncs, cfg *config.Config, cfgPath string) Model {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(colorCyan)

	ti := textinput.New()
	ti.Placeholder = "search..."
	ti.Prompt = "/ "
	ti.CharLimit = 64

	return Model{
		loading:     true,
		updating:    make(map[int]bool),
		ignored:     make(map[int]bool),
		pinned:      make(map[int]bool),
		pinnedIDs:   make(map[string]bool),
		selected:    make(map[int]bool),
		spinner:     s,
		loadFn:      loadFn,
		checkFn:     checkFn,
		updateFn:    updateFn,
		scheduleFns: scheduleFns,
		cfg:         cfg,
		cfgPath:     cfgPath,
		searchInput: ti,
	}
}

// Init starts the spinner and kicks off the background load.
func (m Model) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, m.startLoad())
}

// startLoad returns a command that runs discovery/enrichment in the background.
func (m Model) startLoad() tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		result, err := m.loadFn(ctx)
		return loadDoneMsg{result: result, err: err}
	}
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

// checkSchedule returns a command that checks the current schedule status.
func (m Model) checkSchedule() tea.Cmd {
	if m.scheduleFns == nil {
		return nil
	}
	fn := m.scheduleFns.Check
	return func() tea.Msg {
		return scheduleCheckMsg{status: fn()}
	}
}

// installSchedule returns a command that installs the schedule with the given interval.
func (m Model) installSchedule(hours int) tea.Cmd {
	if m.scheduleFns == nil {
		return nil
	}
	fn := m.scheduleFns.Install
	return func() tea.Msg {
		err := fn(context.Background(), hours)
		return scheduleInstallMsg{err: err, hours: hours}
	}
}

// removeSchedule returns a command that removes the schedule.
func (m Model) removeSchedule() tea.Cmd {
	if m.scheduleFns == nil {
		return nil
	}
	fn := m.scheduleFns.Remove
	return func() tea.Msg {
		err := fn(context.Background())
		return scheduleRemoveMsg{err: err}
	}
}

// saveLastChecked persists the current time as the last-checked timestamp.
func (m *Model) saveLastChecked() {
	if m.cfg == nil {
		return
	}
	m.cfg.LastChecked = time.Now()
	if err := m.cfg.Save(m.cfgPath); err != nil {
		m.statusMsg = fmt.Sprintf("Warning: failed to save config: %v", err)
	}
}

// saveScheduleOffered persists the ScheduleOffered flag to config.
func (m *Model) saveScheduleOffered() {
	if m.cfg == nil {
		return
	}
	m.cfg.ScheduleOffered = true
	if err := m.cfg.Save(m.cfgPath); err != nil {
		m.statusMsg = fmt.Sprintf("Warning: failed to save config: %v", err)
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
		if m.showDetail {
			m.detailViewport.Width = msg.Width
			m.detailViewport.Height = msg.Height - 4
		}
		return m, nil

	case spinner.TickMsg:
		if m.loading || m.checking || len(m.updating) > 0 {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}
		return m, nil

	case loadDoneMsg:
		m.loading = false
		if msg.err != nil {
			m.statusMsg = fmt.Sprintf("Error: %v", msg.err)
			return m, nil
		}
		m.apps = msg.result.Apps
		m.pinnedIDs = msg.result.PinnedIDs
		m.rows = make([]row, len(m.apps))
		m.pinned = make(map[int]bool)
		for i, a := range m.apps {
			m.rows[i] = row{app: a}
			if m.pinnedIDs[a.BundleID] {
				m.pinned[i] = true
			}
		}
		m.checking = true
		m.rebuildVisible()
		m.statusMsg = fmt.Sprintf("Found %d apps, checking for updates...", len(m.apps))
		cmds := []tea.Cmd{m.startCheck(), m.spinner.Tick}
		// Probe the schedule right after load so the prompt appears early.
		if m.scheduleFns != nil && m.cfg != nil && !m.cfg.ScheduleOffered {
			cmds = append(cmds, m.checkSchedule())
		}
		return m, tea.Batch(cmds...)

	case checkDoneMsg:
		m.checking = false
		m.applyResults(msg.results)
		m.rebuildVisible()
		m.statusMsg = fmt.Sprintf("Checked %d apps", len(msg.results))
		m.saveLastChecked()
		return m, nil

	case scheduleCheckMsg:
		m.scheduleStatus = msg.status
		if !msg.status.Enabled && m.cfg != nil && !m.cfg.ScheduleOffered {
			m.schedulePrompt = true
		}
		return m, nil

	case scheduleInstallMsg:
		if msg.err != nil {
			m.statusMsg = fmt.Sprintf("Failed to enable schedule: %v", msg.err)
		} else {
			m.scheduleStatus.Enabled = true
			m.scheduleStatus.IntervalHours = msg.hours
			m.statusMsg = fmt.Sprintf("Scheduled update checks every %dh", msg.hours)
		}
		return m, nil

	case scheduleRemoveMsg:
		if msg.err != nil {
			m.statusMsg = fmt.Sprintf("Failed to disable schedule: %v", msg.err)
		} else {
			m.scheduleStatus.Enabled = false
			m.statusMsg = "Disabled scheduled update checks"
		}
		return m, nil

	case updateDoneMsg:
		delete(m.updating, msg.index)
		m.rows[msg.index].updating = false
		if errors.Is(msg.err, checker.ErrOpenedExternally) {
			m.statusMsg = fmt.Sprintf("Opened %s for update", m.rows[msg.index].app.Name)
		} else if msg.err != nil {
			m.statusMsg = fmt.Sprintf("Update failed for %s: %v", m.rows[msg.index].app.Name, msg.err)
		} else {
			m.statusMsg = fmt.Sprintf("Updated %s successfully", m.rows[msg.index].app.Name)
			// Mark as no longer having an update after success.
			if m.rows[msg.index].result != nil {
				m.rows[msg.index].result.HasUpdate = false
			}
		}
		delete(m.selected, msg.index)
		m.rebuildVisible()
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
	// Schedule prompt overlay takes highest priority.
	if m.schedulePrompt {
		return m.handleSchedulePromptKey(msg)
	}

	// Schedule settings modal.
	if m.showSchedule {
		return m.handleScheduleKey(msg)
	}

	// In detail view, handle viewport scrolling and escape.
	if m.showDetail {
		return m.handleDetailKey(msg)
	}

	// Search mode: forward input to textinput.
	if m.searchMode {
		return m.handleSearchKey(msg)
	}

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
	case tea.KeyEsc:
		return m, tea.Quit
	case tea.KeySpace:
		m.toggleSelection()
		return m, nil
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
		case "d":
			return m.toggleDetail()
		case "p":
			m.togglePin()
			return m, nil
		case "t":
			m.toggleShowAll()
			return m, nil
		case "s":
			return m.openSchedule()
		case " ":
			m.toggleSelection()
			return m, nil
		case "/":
			m.searchMode = true
			m.searchInput.Focus()
			return m, textinput.Blink
		}
	}
	return m, nil
}

// handleSearchKey processes keyboard input during search mode.
func (m Model) handleSearchKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlC:
		return m, tea.Quit
	case tea.KeyEsc:
		m.searchMode = false
		m.searchQuery = ""
		m.searchInput.SetValue("")
		m.searchInput.Blur()
		m.rebuildVisible()
		return m, nil
	case tea.KeyEnter:
		m.searchMode = false
		m.searchInput.Blur()
		return m, nil
	}

	var cmd tea.Cmd
	m.searchInput, cmd = m.searchInput.Update(msg)
	m.searchQuery = m.searchInput.Value()
	m.rebuildVisible()
	return m, cmd
}

// handleDetailKey processes keyboard input when the detail view is active.
func (m Model) handleDetailKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlC:
		return m, tea.Quit
	case tea.KeyEsc:
		m.showDetail = false
		return m, nil
	case tea.KeyRunes:
		switch string(msg.Runes) {
		case "q", "d":
			m.showDetail = false
			return m, nil
		case "j":
			m.detailViewport.LineDown(1)
			return m, nil
		case "k":
			m.detailViewport.LineUp(1)
			return m, nil
		}
	case tea.KeyDown:
		m.detailViewport.LineDown(1)
		return m, nil
	case tea.KeyUp:
		m.detailViewport.LineUp(1)
		return m, nil
	}
	return m, nil
}

// handleSchedulePromptKey processes keyboard input during the first-launch schedule prompt.
func (m Model) handleSchedulePromptKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlC:
		return m, tea.Quit
	case tea.KeyEsc:
		m.schedulePrompt = false
		m.saveScheduleOffered()
		return m, nil
	case tea.KeyRunes:
		switch string(msg.Runes) {
		case "y":
			m.schedulePrompt = false
			m.saveScheduleOffered()
			return m, m.installSchedule(24)
		case "n":
			m.schedulePrompt = false
			m.saveScheduleOffered()
			m.statusMsg = "Scheduled checks declined"
			return m, nil
		case "s":
			m.schedulePrompt = false
			m.saveScheduleOffered()
			m.showSchedule = true
			return m, nil
		}
	}
	return m, nil
}

// handleScheduleKey processes keyboard input in the schedule settings modal.
func (m Model) handleScheduleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlC:
		return m, tea.Quit
	case tea.KeyEsc:
		m.showSchedule = false
		return m, nil
	case tea.KeyRunes:
		switch string(msg.Runes) {
		case "q", "s":
			m.showSchedule = false
			return m, nil
		case "d":
			if m.scheduleStatus.Enabled {
				m.showSchedule = false
				return m, m.removeSchedule()
			}
			return m, nil
		case "1":
			if !m.scheduleStatus.Enabled {
				m.showSchedule = false
				return m, m.installSchedule(12)
			}
			return m, nil
		case "2":
			if !m.scheduleStatus.Enabled {
				m.showSchedule = false
				return m, m.installSchedule(24)
			}
			return m, nil
		case "3":
			if !m.scheduleStatus.Enabled {
				m.showSchedule = false
				return m, m.installSchedule(48)
			}
			return m, nil
		}
	}
	return m, nil
}

// openSchedule opens the schedule settings modal.
func (m Model) openSchedule() (tea.Model, tea.Cmd) {
	if m.scheduleFns == nil {
		m.statusMsg = "Schedule not available"
		return m, nil
	}
	m.scheduleStatus = m.scheduleFns.Check()
	m.showSchedule = true
	return m, nil
}

// moveCursor moves the cursor up or down by delta, clamping to valid range
// and adjusting the scroll offset to keep the cursor visible.
func (m *Model) moveCursor(delta int) {
	if len(m.visible) == 0 {
		return
	}
	m.cursor += delta
	if m.cursor < 0 {
		m.cursor = len(m.visible) - 1
	}
	if m.cursor >= len(m.visible) {
		m.cursor = 0
	}
	m.adjustOffset()
}

// adjustOffset ensures the cursor is visible within the viewport.
func (m *Model) adjustOffset() {
	viewportRows := m.visibleRows()
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+viewportRows {
		m.offset = m.cursor - viewportRows + 1
	}
	// Clamp offset.
	maxOffset := len(m.visible) - viewportRows
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
	reserved := 6
	// Extra line for search bar when active.
	if m.searchMode || m.searchQuery != "" {
		reserved++
	}
	visible := m.height - reserved
	if visible < 1 {
		visible = 1
	}
	return visible
}

// cursorRowIdx returns the real row index for the current cursor position,
// or -1 if the visible list is empty.
func (m *Model) cursorRowIdx() int {
	if len(m.visible) == 0 || m.cursor < 0 || m.cursor >= len(m.visible) {
		return -1
	}
	return m.visible[m.cursor]
}

// toggleIgnore toggles the ignored state of the currently selected app.
func (m *Model) toggleIgnore() {
	idx := m.cursorRowIdx()
	if idx < 0 {
		return
	}
	if m.ignored[idx] {
		delete(m.ignored, idx)
		m.statusMsg = fmt.Sprintf("Unignored %s", m.rows[idx].app.Name)
	} else {
		m.ignored[idx] = true
		m.statusMsg = fmt.Sprintf("Ignored %s", m.rows[idx].app.Name)
	}
}

// toggleDetail opens or closes the release notes detail view.
func (m Model) toggleDetail() (tea.Model, tea.Cmd) {
	if m.showDetail {
		m.showDetail = false
		return m, nil
	}

	idx := m.cursorRowIdx()
	if idx < 0 {
		return m, nil
	}

	r := m.rows[idx]
	if r.result == nil || r.result.ReleaseNotes == "" {
		m.statusMsg = "No release notes available"
		return m, nil
	}

	// Determine content: strip HTML for Sparkle, use markdown as-is for GitHub.
	content := r.result.ReleaseNotes
	if r.result.Source == "sparkle" {
		content = StripHTML(content)
	}

	vp := viewport.New(m.width, m.height-4) // reserve header + help bar
	vp.SetContent(content)

	m.showDetail = true
	m.detailIdx = idx
	m.detailViewport = vp
	return m, nil
}

// togglePin toggles the pinned state of the currently selected app.
func (m *Model) togglePin() {
	idx := m.cursorRowIdx()
	if idx < 0 {
		return
	}
	bundleID := m.rows[idx].app.BundleID
	if m.pinned[idx] {
		delete(m.pinned, idx)
		delete(m.pinnedIDs, bundleID)
		m.statusMsg = fmt.Sprintf("Unpinned %s", m.rows[idx].app.Name)
	} else {
		m.pinned[idx] = true
		m.pinnedIDs[bundleID] = true
		m.statusMsg = fmt.Sprintf("Pinned %s", m.rows[idx].app.Name)
	}
}

// toggleSelection toggles the selection checkbox on the cursor row.
func (m *Model) toggleSelection() {
	idx := m.cursorRowIdx()
	if idx < 0 {
		return
	}
	if m.selected[idx] {
		delete(m.selected, idx)
	} else {
		m.selected[idx] = true
	}
	m.rebuildVisible()
}

// toggleShowAll flips between showing all apps and showing only actionable ones.
func (m *Model) toggleShowAll() {
	m.showAll = !m.showAll
	m.rebuildVisible()
	if m.showAll {
		m.statusMsg = "Showing all apps"
	} else {
		hidden := len(m.rows) - len(m.visible)
		m.statusMsg = fmt.Sprintf("Showing actionable apps (%d hidden)", hidden)
	}
}

// handleUpdate starts updates for selected apps, or the cursor row if none selected.
func (m Model) handleUpdate() (tea.Model, tea.Cmd) {
	if m.checking {
		return m, nil
	}

	// If there are selections, batch-update all selected.
	if len(m.selected) > 0 {
		var cmds []tea.Cmd
		count := 0
		for idx := range m.selected {
			r := m.rows[idx]
			if r.result == nil || !r.result.HasUpdate || r.result.Error != nil {
				continue
			}
			if m.updating[idx] || m.ignored[idx] {
				continue
			}
			m.updating[idx] = true
			m.rows[idx].updating = true
			cmds = append(cmds, m.startUpdate(idx))
			count++
		}
		m.selected = make(map[int]bool)
		m.rebuildVisible()
		if count == 0 {
			m.statusMsg = "No updates available for selected apps"
			return m, nil
		}
		m.statusMsg = fmt.Sprintf("Updating %d selected apps...", count)
		cmds = append(cmds, m.spinner.Tick)
		return m, tea.Batch(cmds...)
	}

	// No selections — update cursor row.
	idx := m.cursorRowIdx()
	if idx < 0 {
		return m, nil
	}
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

// handleUpdateAll starts updates for all visible apps with available updates.
// Pinned apps are skipped.
func (m Model) handleUpdateAll() (tea.Model, tea.Cmd) {
	if m.checking {
		return m, nil
	}
	var cmds []tea.Cmd
	count := 0
	skippedPinned := 0
	for _, i := range m.visible {
		r := m.rows[i]
		if r.result == nil || !r.result.HasUpdate || r.result.Error != nil {
			continue
		}
		if m.updating[i] || m.ignored[i] {
			continue
		}
		if m.pinned[i] {
			skippedPinned++
			continue
		}
		m.updating[i] = true
		m.rows[i].updating = true
		cmds = append(cmds, m.startUpdate(i))
		count++
	}
	if count == 0 {
		if skippedPinned > 0 {
			m.statusMsg = fmt.Sprintf("No updates available (%d pinned)", skippedPinned)
		} else {
			m.statusMsg = "No updates available"
		}
		return m, nil
	}
	msg := fmt.Sprintf("Updating %d apps...", count)
	if skippedPinned > 0 {
		msg += fmt.Sprintf(" (%d pinned, skipped)", skippedPinned)
	}
	m.statusMsg = msg
	cmds = append(cmds, m.spinner.Tick)
	return m, tea.Batch(cmds...)
}

// handleRefresh re-discovers apps and re-checks for updates.
func (m Model) handleRefresh() (tea.Model, tea.Cmd) {
	if m.loading || m.checking {
		return m, nil
	}
	m.loading = true
	m.rows = nil
	m.apps = nil
	m.selected = make(map[int]bool)
	m.visible = m.visible[:0]
	m.statusMsg = "Refreshing..."
	return m, tea.Batch(m.startLoad(), m.spinner.Tick)
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

// rebuildVisible recomputes the visible slice based on the current showAll flag
// and search query. In filtered mode (showAll=false), only actionable rows are shown:
// updating, selected, unchecked (pending), or having an update/error.
func (m *Model) rebuildVisible() {
	m.visible = m.visible[:0]
	query := strings.ToLower(m.searchQuery)
	for i, r := range m.rows {
		// Apply search filter.
		if query != "" && !strings.Contains(strings.ToLower(r.app.Name), query) {
			continue
		}
		if m.showAll {
			m.visible = append(m.visible, i)
			continue
		}
		if r.updating || m.selected[i] || m.updating[i] {
			m.visible = append(m.visible, i)
		} else if !r.checked {
			m.visible = append(m.visible, i)
		} else if r.result != nil && (r.result.HasUpdate || r.result.Error != nil) {
			m.visible = append(m.visible, i)
		}
	}
	if m.cursor >= len(m.visible) {
		m.cursor = max(0, len(m.visible)-1)
	}
	m.adjustOffset()
}

// View renders the TUI.
func (m Model) View() string {
	if m.schedulePrompt {
		return m.viewSchedulePrompt()
	}

	if m.showSchedule {
		return m.viewSchedule()
	}

	if m.showDetail {
		return m.viewDetail()
	}

	var b strings.Builder

	// Header.
	b.WriteString(styleHeader.Render("macOS App Updater"))
	b.WriteString("\n")

	if m.loading {
		b.WriteString(m.spinner.View())
		b.WriteString(" Discovering apps...\n")
		return b.String()
	}

	if m.checking {
		b.WriteString(m.spinner.View())
		b.WriteString(" Checking for updates...\n")
		if m.statusMsg != "" {
			b.WriteString(lipgloss.NewStyle().Foreground(colorGray).Render(m.statusMsg))
			b.WriteString("\n")
		}
		return b.String()
	}

	viewportRows := m.visibleRows()

	// Empty state when filtered view has nothing to show.
	if len(m.visible) == 0 && !m.showAll {
		b.WriteString(lipgloss.NewStyle().Foreground(colorGray).Render(
			"All apps are up to date. Press t to show all."))
		b.WriteString("\n")
		b.WriteString(m.renderStatusBar())
		return b.String()
	}

	// Search bar (shown when in search mode or when a query is active).
	if m.searchMode {
		b.WriteString(m.searchInput.View())
		b.WriteString("\n")
	} else if m.searchQuery != "" {
		b.WriteString(lipgloss.NewStyle().Foreground(colorGray).Render(
			fmt.Sprintf("  filtered: %q (/ to search, esc to clear)", m.searchQuery)))
		b.WriteString("\n")
	}

	// Column headers.
	b.WriteString(renderColumnHeaders(m.width))
	b.WriteString("\n")

	// Render visible rows based on current offset.
	end := m.offset + viewportRows
	if end > len(m.visible) {
		end = len(m.visible)
	}
	for i := m.offset; i < end; i++ {
		rowIdx := m.visible[i]
		b.WriteString(m.renderRow(rowIdx, i == m.cursor, m.selected[rowIdx]))
		b.WriteString("\n")
	}

	// Scroll indicator.
	if len(m.visible) > viewportRows {
		b.WriteString(lipgloss.NewStyle().Foreground(colorGray).Render(
			fmt.Sprintf("  showing %d-%d of %d", m.offset+1, end, len(m.visible))))
		b.WriteString("\n")
	}

	// Status bar.
	b.WriteString(m.renderStatusBar())

	return b.String()
}

// viewDetail renders the release notes detail view.
func (m Model) viewDetail() string {
	var b strings.Builder

	name := "Release Notes"
	if m.detailIdx < len(m.rows) {
		r := m.rows[m.detailIdx]
		name = fmt.Sprintf("%s — Release Notes (%s → %s)", r.app.Name, r.app.Version, r.result.LatestVersion)
	}

	b.WriteString(styleHeader.Render(name))
	b.WriteString("\n")
	b.WriteString(m.detailViewport.View())
	b.WriteString("\n")
	b.WriteString(styleStatusBar.Render("j/k: scroll | d/esc/q: back"))

	return b.String()
}

// viewSchedulePrompt renders the first-launch schedule prompt overlay.
func (m Model) viewSchedulePrompt() string {
	var b strings.Builder

	b.WriteString(styleHeader.Render("Schedule Update Checks"))
	b.WriteString("\n")
	b.WriteString("  Would you like to enable daily scheduled update checks?\n")
	b.WriteString("  You'll receive macOS notifications when updates are available.\n")
	b.WriteString("\n")
	b.WriteString(styleStatusBar.Render("y: enable daily checks | n: no thanks | s: customize"))

	return b.String()
}

// viewSchedule renders the schedule settings modal.
func (m Model) viewSchedule() string {
	var b strings.Builder

	b.WriteString(styleHeader.Render("Schedule Settings"))
	b.WriteString("\n")

	if m.scheduleStatus.Enabled {
		b.WriteString(fmt.Sprintf("  Status: %s (every %dh)\n",
			styleUpToDate.Render("enabled"),
			m.scheduleStatus.IntervalHours))
		b.WriteString("\n")
		b.WriteString(styleStatusBar.Render("d: disable | esc: back"))
	} else {
		b.WriteString(fmt.Sprintf("  Status: %s\n", styleError.Render("disabled")))
		b.WriteString("\n")
		b.WriteString(styleStatusBar.Render("1: enable (12h) | 2: enable (24h) | 3: enable (48h) | esc: back"))
	}

	return b.String()
}

// renderColumnHeaders renders the table column headers.
func renderColumnHeaders(width int) string {
	nameW, curW, latW, srcW := columnWidths(width)

	return fmt.Sprintf("   %s  %s  %s  %s  %s",
		styleColumnHeader.Render(pad("NAME", nameW)),
		styleColumnHeader.Render(pad("CURRENT", curW)),
		styleColumnHeader.Render(pad("LATEST", latW)),
		styleColumnHeader.Render(pad("SOURCE", srcW)),
		styleColumnHeader.Render("STATUS"),
	)
}

// renderRow renders a single table row.
func (m Model) renderRow(index int, isCursor bool, isSelected bool) string {
	r := m.rows[index]
	nameW, curW, latW, srcW := columnWidths(m.width)

	// 3-char cursor/selection prefix.
	var cursor string
	switch {
	case isCursor && isSelected:
		cursor = styleCursor.Render(">") + styleUpdate.Render("✓") + " "
	case isCursor:
		cursor = styleCursor.Render(">") + "  "
	case isSelected:
		cursor = " " + styleUpdate.Render("✓") + " "
	default:
		cursor = "   "
	}

	name := truncate(r.app.Name, nameW)
	current := truncate(r.app.Version, curW)
	latest := ""
	// Always show source from app metadata; override with checker result when available.
	rawSource := string(r.app.Source)
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
	} else if m.pinned[index] && r.result.HasUpdate {
		latest = r.result.LatestVersion
		rawSource = r.result.Source
		status = stylePinned.Render("pinned")
	} else if r.result.HasUpdate && r.result.IsMajorUpdate {
		latest = r.result.LatestVersion
		rawSource = r.result.Source
		status = styleMajorUpdate.Render("MAJOR update")
	} else if r.result.HasUpdate {
		latest = r.result.LatestVersion
		rawSource = r.result.Source
		status = styleUpdate.Render("update available")
	} else {
		latest = r.result.LatestVersion
		rawSource = r.result.Source
		status = styleUpToDate.Render("up to date")
	}

	// Show +brew suffix when app is installed via brew but source is from another checker.
	if r.app.InstalledViaBrew && rawSource != "" && rawSource != "brew" && rawSource != "brew-info" {
		rawSource = rawSource + "+brew"
	}

	latest = truncate(latest, latW)

	// Pad source before styling so ANSI codes don't affect width.
	displayName := sourceDisplayName(rawSource)
	source := styledSource(rawSource) + strings.Repeat(" ", max(0, srcW-len(displayName)))

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
	var helpParts []string

	helpParts = append(helpParts, "j/k: navigate", "space: select")

	if len(m.selected) > 0 {
		helpParts = append(helpParts, fmt.Sprintf("enter: update %d selected", len(m.selected)))
	} else {
		helpParts = append(helpParts, "enter: update")
	}

	helpParts = append(helpParts, "a: update all", "d: details", "p: pin", "i: ignore")

	if m.showAll {
		helpParts = append(helpParts, "t: show updatable")
	} else {
		hidden := len(m.rows) - len(m.visible)
		if hidden > 0 {
			helpParts = append(helpParts, fmt.Sprintf("t: show all (%d hidden)", hidden))
		} else {
			helpParts = append(helpParts, "t: show all")
		}
	}

	if m.scheduleFns != nil {
		helpParts = append(helpParts, "s: schedule")
	}

	helpParts = append(helpParts, "/: search", "r: refresh", "q: quit")

	// Append last-checked timestamp.
	if m.cfg != nil {
		helpParts = append(helpParts, "last checked: "+formatRelativeTime(m.cfg.LastChecked))
	}

	help := styleStatusBar.Render(strings.Join(helpParts, " | "))

	var msg string
	if m.statusMsg != "" {
		msg = "\n" + lipgloss.NewStyle().Foreground(colorWhite).Render(m.statusMsg)
	}

	return help + msg
}

// formatRelativeTime returns a human-readable relative time string.
func formatRelativeTime(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

// columnWidths returns proportional widths for each column based on terminal width.
func columnWidths(width int) (name, current, latest, source int) {
	if width < 80 {
		width = 80
	}
	// Subtract cursor (3) + gaps between columns (4*2=8) = 11 chars overhead.
	// Plus STATUS column gets the rest, so we don't allocate for it.
	available := width - 11 - 18 // 18 for status text approximate
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
