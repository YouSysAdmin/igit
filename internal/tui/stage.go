package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/yousysadmin/igit/internal/git"
	"github.com/yousysadmin/igit/internal/keymap"
	"github.com/yousysadmin/igit/internal/stageplan"
)

// stageState is the review-mode deferred staging plan: marks on files and
// change blocks, a visual line range in progress, and the quit confirmation
// shown while marks are pending.
type stageState struct {
	plan        *stageplan.Plan
	applicable  bool   // false outside a git working-tree review (ref, --staged, stdin, compare, no repository)
	rangeAnchor int    // visual range start row, -1 = no range active
	confirmQuit bool   // waiting for y to quit despite pending marks
	hint        string // transient status-bar message, cleared on next key press
}

// commitWithPlanMsg asks the App to apply the plan and switch to commit mode.
type commitWithPlanMsg struct {
	plan *stageplan.Plan
}

// StagePlan returns the review stage plan (never nil).
func (m Model) StagePlan() *stageplan.Plan { return m.stage.plan }

// stagePlanFiles returns the marked paths as a set for the file tree.
func (m Model) stagePlanFiles() map[string]bool {
	if m.stage.plan.Empty() {
		return nil
	}
	out := make(map[string]bool, m.stage.plan.Count())
	for _, p := range m.stage.plan.Files() {
		out[p] = true
	}
	return out
}

// stageMarked reports whether the diff row idx is covered by the plan.
func (m Model) stageMarked(idx int) bool {
	if m.file.name == "" || idx < 0 || idx >= len(m.file.lines) || m.stage.plan.Empty() {
		return false
	}
	dl := m.file.lines[idx]
	if dl.ChangeType != git.ChangeAdd && dl.ChangeType != git.ChangeRemove {
		return false
	}
	return m.stage.plan.Covers(m.file.name, dl.OldNum, dl.NewNum)
}

// inVisualRange reports whether idx is inside the active visual range.
func (m Model) inVisualRange(idx int) bool {
	if m.stage.rangeAnchor < 0 || m.layout.focus != paneDiff {
		return false
	}
	first, last := m.visualRange()
	return idx >= first && idx <= last
}

func (m Model) visualRange() (first, last int) {
	return min(m.stage.rangeAnchor, m.nav.diffCursor), max(m.stage.rangeAnchor, m.nav.diffCursor)
}

// stageUnavailable sets the hint explaining why stage marks are off.
func (m *Model) stageUnavailable() bool {
	if m.stage.applicable {
		return false
	}
	m.stage.hint = "stage marks require a git working-tree review"
	return true
}

// handleVisualRange toggles the visual line range in the diff pane.
func (m Model) handleVisualRange() (tea.Model, tea.Cmd) {
	if m.stageUnavailable() {
		return m, nil
	}
	if m.layout.focus != paneDiff || len(m.file.lines) == 0 {
		m.stage.hint = "range selection works in the diff pane"
		return m, nil
	}
	if m.stage.rangeAnchor >= 0 {
		m.stage.rangeAnchor = -1
	} else {
		m.stage.rangeAnchor = m.nav.diffCursor
	}
	m.invalidateRenderCaches()
	m.syncViewportToCursor()
	return m, nil
}

// handleStageMark toggles the mark on the selected file (tree focus) or on the
// visual range / change block under the cursor (diff focus).
func (m Model) handleStageMark() (tea.Model, tea.Cmd) {
	if m.stageUnavailable() {
		return m, nil
	}
	if m.layout.focus == paneTree || len(m.file.lines) == 0 {
		return m.handleStageMarkFile()
	}
	var first, last int
	switch {
	case m.stage.rangeAnchor >= 0:
		first, last = m.visualRange()
		m.stage.rangeAnchor = -1
	default:
		var ok bool
		first, last, ok = m.changeBlockAt(m.nav.diffCursor)
		if !ok {
			m.stage.hint = "move the cursor onto a change (or select a range with V)"
			return m, nil
		}
	}
	r := stageplan.NewRange(m.file.lines[first : last+1])
	if r.OldStart == 0 && r.NewStart == 0 {
		m.stage.hint = "the selection has no changed lines"
		return m, nil
	}
	if m.stage.plan.ToggleRange(m.file.name, r) {
		m.stage.hint = fmt.Sprintf("marked %d line(s) for the commit", last-first+1)
	} else {
		m.stage.hint = "mark removed"
	}
	m.invalidateRenderCaches()
	m.syncViewportToCursor()
	return m, nil
}

// handleStageMarkFile toggles the whole-file mark of the focused file.
func (m Model) handleStageMarkFile() (tea.Model, tea.Cmd) {
	if m.stageUnavailable() {
		return m, nil
	}
	file := m.file.name
	if m.layout.focus == paneTree || file == "" {
		file = m.tree.SelectedFile()
	}
	if file == "" {
		return m, nil
	}
	if m.stage.plan.ToggleFile(file) {
		m.stage.hint = "marked " + file + " for the commit"
	} else {
		m.stage.hint = "mark removed from " + file
	}
	m.invalidateRenderCaches()
	if len(m.file.lines) > 0 {
		m.syncViewportToCursor()
	}
	return m, nil
}

// changeBlockAt returns the contiguous added/removed rows around idx.
func (m Model) changeBlockAt(idx int) (first, last int, ok bool) {
	lines := m.file.lines
	if idx < 0 || idx >= len(lines) || !isChangeRow(lines[idx]) {
		return 0, 0, false
	}
	first, last = idx, idx
	for first > 0 && isChangeRow(lines[first-1]) {
		first--
	}
	for last+1 < len(lines) && isChangeRow(lines[last+1]) {
		last++
	}
	return first, last, true
}

// handleCommitWithPlan hands the plan to the App, which applies it and opens
// commit mode. An empty plan is a plain mode switch.
func (m Model) handleCommitWithPlan() (tea.Model, tea.Cmd) {
	if m.stageUnavailable() {
		return m, nil
	}
	plan := m.stage.plan
	m.stage.rangeAnchor = -1
	return m, func() tea.Msg { return commitWithPlanMsg{plan: plan} }
}

// handleStageAction routes the stage-plan actions.
func (m Model) handleStageAction(action keymap.Action) (tea.Model, tea.Cmd) {
	switch action { //nolint:exhaustive // only the four stage actions reach here
	case keymap.ActionStageMark:
		return m.handleStageMark()
	case keymap.ActionStageMarkFile:
		return m.handleStageMarkFile()
	case keymap.ActionVisualRange:
		return m.handleVisualRange()
	case keymap.ActionCommitWithPlan:
		return m.handleCommitWithPlan()
	}
	return m, nil
}

// handleQuitAction routes quit and discard-quit. a plain quit first checks
// for pending stage marks.
func (m Model) handleQuitAction(action keymap.Action) (tea.Model, tea.Cmd) {
	if action == keymap.ActionQuitDiscarding {
		return m.handleDiscardQuit()
	}
	if model, handled := m.handleQuitWithPlan(); handled {
		return model, nil
	}
	if model, handled := m.handleQuitWithPR(); handled {
		return model, nil
	}
	return m, tea.Quit
}

// handleQuitWithPlan intercepts quit while marks are pending. y confirms.
func (m Model) handleQuitWithPlan() (tea.Model, bool) {
	if m.stage.plan.Empty() || m.cfg.noStatusBar {
		return m, false
	}
	m.stage.confirmQuit = true
	m.stage.hint = fmt.Sprintf("%d file(s) marked for the commit but not staged — press y to quit anyway, any other key to stay", m.stage.plan.Count())
	return m, true
}

func (m Model) handleStageQuitKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	m.stage.confirmQuit = false
	m.stage.hint = ""
	if msg.String() == "y" {
		return m, tea.Quit
	}
	return m, nil
}

// removeStagedFromPlan drops the files a plan run staged. skipped ones keep
// their marks so the user can see what did not land.
func (m *Model) removeStagedFromPlan(res stageplan.Result) {
	for _, p := range res.Staged {
		m.stage.plan.Remove(p)
	}
	m.invalidateRenderCaches()
}
