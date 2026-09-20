package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/yousysadmin/igit/internal/git"
	"github.com/yousysadmin/igit/internal/keymap"
	"github.com/yousysadmin/igit/internal/tui/overlay"
	"github.com/yousysadmin/igit/internal/tui/sidepane"
)

// helpKeyDisplay maps bubbletea key names to user-friendly display names.
var helpKeyDisplay = map[string]string{
	"pgdown": "PgDn",
	"pgup":   "PgUp",
	"left":   "←",
	"right":  "→",
	"home":   "Home",
	"end":    "End",
	"enter":  "Enter",
	"esc":    "Esc",
	"tab":    "Tab",
	"up":     "↑",
	"down":   "↓",
	" ":      "Space",
}

// displayKeyName returns a user-friendly display name for a bubbletea key.
func (m Model) displayKeyName(key string) string {
	if d, ok := helpKeyDisplay[key]; ok {
		return d
	}
	if strings.HasPrefix(key, "ctrl+") {
		suffix := key[5:]
		if suffix != "" {
			return "Ctrl+" + strings.ToUpper(suffix[:1]) + suffix[1:]
		}
		return "Ctrl+"
	}
	return key
}

// formatKeysForHelp returns a formatted key string for a given action using display names.
func (m Model) formatKeysForHelp(action keymap.Action) string {
	keys := m.keymap.KeysFor(action)
	display := make([]string, len(keys))
	for i, k := range keys {
		display[i] = m.displayKeyName(k)
	}
	return strings.Join(display, " / ")
}

// buildHelpSpec builds an overlay.HelpSpec from the keymap's help sections,
// converting raw key names to display names.
// When the vim-motion preset is active, appends a synthetic "Vim motion"
// section listing the 11 preset bindings (which have no entries in the base
// keymap since they're only reachable through the interceptor).
func (m Model) buildHelpSpec() overlay.HelpSpec {
	sections := m.keymap.HelpSections()
	result := make([]overlay.HelpSection, 0, len(sections))
	for _, sec := range sections {
		pad := m.helpIconPad(sec)
		var entries []overlay.HelpEntry
		for _, e := range sec.Entries {
			entries = append(entries, overlay.HelpEntry{
				Keys:        m.formatKeysForHelp(e.Action),
				Description: m.helpDescriptionWithIcon(e, pad),
			})
		}
		if sec.Name == "Search" {
			entries = append(entries,
				overlay.HelpEntry{Keys: "↑ / Ctrl+P", Description: pad + "recall previous search query (in search prompt)"},
				overlay.HelpEntry{Keys: "↓ / Ctrl+N", Description: pad + "recall next search query / clear (in search prompt)"},
			)
		}
		result = append(result, overlay.HelpSection{Title: sec.Name, Entries: entries})
	}
	return overlay.HelpSpec{Sections: result}
}

// helpIconPad returns the indent that keeps a section's description column straight
// once some of its rows carry a glyph. a section with no glyph rows gets none.
func (m Model) helpIconPad(sec keymap.HelpSection) string {
	for _, e := range sec.Entries {
		if _, ok := statusIconForAction[e.Action]; ok {
			return "  "
		}
	}
	return ""
}

func (m Model) helpDescriptionWithIcon(e keymap.HelpEntryWithKeys, pad string) string {
	if icon, ok := statusIconForAction[e.Action]; ok {
		return icon + " " + e.Description
	}
	return pad + e.Description
}

// handleDiscardQuit handles the Q key press for discard-and-quit.
func (m Model) handleDiscardQuit() (tea.Model, tea.Cmd) {
	if m.store.Count() == 0 || m.session.noConfirmDiscard || m.cfg.noStatusBar {
		m.discarded = true
		return m, tea.Quit
	}
	m.inConfirmDiscard = true
	return m, nil
}

// handleFileAnnotateKey starts file-level annotation from diff pane only.
func (m Model) handleFileAnnotateKey() (tea.Model, tea.Cmd) {
	if m.layout.focus != paneDiff || m.file.name == "" {
		return m, nil
	}
	cmd := m.startFileAnnotation()
	m.layout.viewport.SetContent(m.renderDiff())
	return m, cmd
}

// handleEscKey clears active search results on esc.
func (m Model) handleEscKey() (tea.Model, tea.Cmd) {
	if len(m.search.matches) > 0 {
		m.clearSearch()
		m.layout.viewport.SetContent(m.renderDiff())
	}
	return m, nil
}

// handleEnterKey handles enter key based on current pane focus.
func (m Model) handleEnterKey() (tea.Model, tea.Cmd) {
	switch m.layout.focus {
	case paneTree:
		if m.file.name != "" {
			m.layout.focus = paneDiff
		}
		return m, nil
	case paneDiff:
		var cmd tea.Cmd
		if m.cursorOnFileAnnotationLine() {
			cmd = m.startFileAnnotation()
		} else {
			cmd = m.startAnnotation()
		}
		m.layout.viewport.SetContent(m.renderDiff())
		return m, cmd
	}
	return m, nil
}

// handleConfirmDiscardKey handles keys during discard confirmation prompt.
func (m Model) handleConfirmDiscardKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Q":
		m.discarded = true
		return m, tea.Quit
	case "n", "esc":
		m.inConfirmDiscard = false
		return m, nil
	}
	return m, nil
}

// handleFilterToggle toggles the annotated files filter.
// no-op in single-file mode (tree pane is hidden).
func (m Model) handleFilterToggle() (tea.Model, tea.Cmd) {
	if m.file.singleFile {
		return m, nil
	}
	annotated := m.annotatedFiles()
	if len(annotated) > 0 || m.tree.FilterActive() {
		m.pendingAnnotJump = nil    // clear pending annotation jump on manual navigation
		m.nav.pendingHunkJump = nil // clear pending hunk jump on manual navigation
		m.tree.ToggleFilter(annotated)
		m.tree.EnsureVisible(m.treePageSize())
		return m.loadSelectedIfChanged()
	}
	return m, nil
}

// handleUnreviewedFilterToggle toggles the sidebar between all files and files
// still awaiting review. It is unavailable in single-file mode.
func (m Model) handleUnreviewedFilterToggle() (tea.Model, tea.Cmd) {
	if m.file.singleFile {
		return m, nil
	}
	m.pendingAnnotJump = nil
	m.nav.pendingHunkJump = nil
	m.tree.ToggleUnreviewedFilter()
	m.tree.EnsureVisible(m.treePageSize())
	return m.loadSelectedIfChanged()
}

// handleMarkReviewed toggles the reviewed state of the focused file.
// tree focus uses the selected row. diff focus uses the displayed file.
func (m Model) handleMarkReviewed() (tea.Model, tea.Cmd) {
	file := m.file.name
	if m.layout.focus == paneTree {
		file = m.tree.SelectedFile()
	}
	if file == "" {
		file = m.tree.SelectedFile()
	}
	if file == "" {
		return m, nil
	}
	if m.tree.IsReviewed(file) {
		m.tree.Unreview(file)
		delete(m.reviewed.pending, file)
		return m.loadSelectedIfChanged()
	}
	if _, pending := m.reviewed.pending[file]; pending {
		delete(m.reviewed.pending, file)
		return m, nil
	}
	if file == m.file.name {
		entry := git.FileEntry{Path: file, OldPath: m.tree.OldPath(file), Status: m.tree.FileStatus(file)}
		fingerprint := git.FileFingerprint(entry, m.file.lines)
		m.reviewed.cache[file] = fingerprint
		m.tree.SetReviewed(file, fingerprint)
		return m.loadSelectedIfChanged()
	}
	if fingerprint := m.reviewed.cache[file]; fingerprint != "" {
		m.tree.SetReviewed(file, fingerprint)
		return m.loadSelectedIfChanged()
	}

	m.reviewed.loadSeq++
	seq := m.reviewed.loadSeq
	m.reviewed.pending[file] = seq
	entry := git.FileEntry{Path: file, OldPath: m.tree.OldPath(file), Status: m.tree.FileStatus(file)}
	return m, m.loadReviewFingerprint(entry, seq)
}

// handleFileOrSearchNav handles next/prev item navigation: navigates search matches when a search
// is active, otherwise steps through the files (no-op in single-file mode).
func (m Model) handleFileOrSearchNav(forward bool) (tea.Model, tea.Cmd) {
	if len(m.search.matches) > 0 {
		if forward {
			m.nextSearchMatch()
		} else {
			m.prevSearchMatch()
		}
		m.layout.viewport.SetContent(m.renderDiff())
		return m, nil
	}
	if !m.file.singleFile {
		m.pendingAnnotJump = nil    // clear pending annotation jump on manual navigation
		m.nav.pendingHunkJump = nil // clear pending hunk jump on manual navigation
		if forward {
			m.tree.StepFile(sidepane.DirectionNext)
		} else {
			m.tree.StepFile(sidepane.DirectionPrev)
		}
		return m.loadSelectedIfChanged()
	}
	return m, nil
}

// annotatedFiles returns a set of files that have annotations.
func (m Model) annotatedFiles() map[string]bool {
	result := make(map[string]bool)
	for _, f := range m.store.Files() {
		result[f] = true
	}
	for f := range m.remoteNotes {
		result[f] = true
	}
	return result
}
