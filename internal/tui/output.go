package tui

import (
	"fmt"
	"log"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/yousysadmin/igit/internal/keymap"
)

// outputState holds the in-session annotation output controls: the transient
// hint shown after O (flush) or ctrl+s (save-as), the save-as prompt, and the
// path the session settled on. hint is cleared on the next key press,
// mirroring reloadState.hint.
type outputState struct {
	hint   string          // transient status-bar message, cleared on next key press
	saving bool            // true while the save-as path prompt is open
	input  textinput.Model // save-as path input
	path   string          // path chosen in-session, wins over the --output flag
}

// handleOutputAction routes the two in-session output actions.
func (m Model) handleOutputAction(action keymap.Action) (tea.Model, tea.Cmd) {
	if action == keymap.ActionSaveAs {
		return m.startSaveAs()
	}
	return m.handleFlushOutput()
}

// effectiveOutputPath returns the file the next flush writes to: the path chosen
// in-session, then --output, then the --output-dir destination. Empty when none
// of the three is configured.
func (m Model) effectiveOutputPath() string {
	switch {
	case m.output.path != "":
		return m.output.path
	case m.session.outputPath != "":
		return m.session.outputPath
	case m.defaultOutputPath != nil:
		return m.defaultOutputPath()
	}
	return ""
}

// saveAsPrefill returns the text the save-as prompt opens with: the effective
// output path, or the composition root's suggestion when nothing is configured.
func (m Model) saveAsPrefill() string {
	if p := m.effectiveOutputPath(); p != "" {
		return p
	}
	if m.saveAsPath != nil {
		return m.saveAsPath()
	}
	return ""
}

// startSaveAs opens the save-as prompt (ctrl+s). With the status bar hidden the
// prompt would be invisible, so the file is written straight to the prefill
// path instead, mirroring the reload confirmation's --no-status-bar behavior.
func (m Model) startSaveAs() (tea.Model, tea.Cmd) {
	if m.store.Count() == 0 {
		m.output.hint = "No annotations to save"
		return m, nil
	}
	prefill := m.saveAsPrefill()
	if m.cfg.noStatusBar {
		return m.writeAnnotationsTo(prefill), nil
	}
	m.clearPendingInputState()
	ti := textinput.New()
	ti.Placeholder = "path/to/annotations.md"
	cmd := ti.Focus()
	ti.CharLimit = 4096
	ti.Width = max(10, m.layout.width-20)
	ti.SetValue(prefill)
	ti.CursorEnd()
	m.output.input = ti
	m.output.saving = true
	return m, cmd
}

// handleSaveAsKey drives the save-as prompt: enter writes to the typed path and
// remembers it for later flushes and for the exit handoff, esc cancels, every
// other key edits the path.
func (m Model) handleSaveAsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type { //nolint:exhaustive // only enter and esc are prompt controls, the rest edits text
	case tea.KeyEnter:
		path := strings.TrimSpace(m.output.input.Value())
		m.output.saving = false
		if path == "" {
			m.output.hint = "Save canceled"
			return m, nil
		}
		return m.writeAnnotationsTo(path), nil
	case tea.KeyEsc:
		m.output.saving = false
		m.output.hint = "Save canceled"
		return m, nil
	}
	var cmd tea.Cmd
	m.output.input, cmd = m.output.input.Update(msg)
	return m, cmd
}

// writeAnnotationsTo writes the store to path and records it as the session's
// output path on success. Feedback goes to output.hint.
func (m Model) writeAnnotationsTo(path string) Model {
	if path == "" {
		m.output.hint = "No output path configured"
		return m
	}
	n := m.store.Count()
	if _, err := m.store.WriteFile(path); err != nil {
		log.Printf("[WARN] save annotations: %v", err)
		m.output.hint = "Save failed: " + err.Error()
		return m
	}
	m.output.path = path
	m.output.hint = fmt.Sprintf("Wrote %d %s to %s", n, pluralAnnotations(n), path)
	return m
}

func pluralAnnotations(n int) string {
	if n == 1 {
		return "annotation"
	}
	return "annotations"
}

// handleFlushOutput writes the current annotations to the effective output
// file without exiting. The store is never mutated, so annotations persist
// in-session and can be re-flushed. Feedback is reported through output.hint.
func (m Model) handleFlushOutput() (tea.Model, tea.Cmd) {
	n := m.store.Count()
	if n == 0 {
		m.output.hint = "No annotations to flush"
		return m, nil
	}
	outputPath := m.effectiveOutputPath()
	if outputPath == "" {
		m.output.hint = "Output flush requires -o/--output or --output-dir"
		return m, nil
	}
	if _, err := m.store.WriteFile(outputPath); err != nil {
		log.Printf("[WARN] flush annotations to output: %v", err)
		m.output.hint = "Flush failed"
		return m, nil
	}
	// pin the destination so later flushes and the exit handoff reuse the
	// same file instead of minting a new timestamped name each time.
	m.output.path = outputPath
	m.output.hint = fmt.Sprintf("Wrote %d %s to output file", n, pluralAnnotations(n))
	return m, nil
}
