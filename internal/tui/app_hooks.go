package tui

import tea "github.com/charmbracelet/bubbletea"

// SetHint shows a transient status-bar message that the next key press clears.
// Used by App for mode-switch feedback that originates outside the model.
func (m *Model) SetHint(hint string) {
	m.keys.hint = hint
}

// RefreshCmd re-fetches the file list and commit log while keeping annotations,
// reviewed marks and the current selection. App calls it when the user returns
// from commit mode, where staging may have changed the working tree.
func (m *Model) RefreshCmd() tea.Cmd {
	return m.triggerReload()
}

// inputBusy reports whether a modal currently owns the keyboard: text input,
// a confirmation prompt, an open overlay or a pending chord. App must not
// intercept keys while this is true.
func (m Model) inputBusy() bool {
	return m.annot.annotating || m.search.active || m.output.saving ||
		m.inConfirmDiscard || m.reload.pending || m.stage.confirmQuit || m.pr.submitting || m.overlay.Active() ||
		m.keys.chordPending != ""
}
