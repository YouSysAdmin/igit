package overlay

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/yousysadmin/igit/internal/keymap"
	"github.com/yousysadmin/igit/internal/tui/style"
)

const (
	promptMaxWidth  = 90
	promptMinWidth  = 30
	promptMargin    = 8
	promptBorderPad = 4
	promptCharLimit = 4000
)

// PromptSpec describes a single-line text prompt. History entries are
// recalled with up/down (ctrl+p/ctrl+n). Editor labels the ctrl+o key that
// hands the current text to an external editor (empty hides the hint).
type PromptSpec struct {
	ID          string
	Title       string
	Placeholder string
	Initial     string
	History     []string
	Editor      string
	AllowEmpty  bool // enter submits an empty value instead of being ignored
}

type promptOverlay struct {
	spec       PromptSpec
	input      textinput.Model
	histBack   int    // how many entries back into History (newest first) the input shows, 0 = fresh line
	draft      string // text typed before recalling history
	popupWidth int
}

func (p *promptOverlay) open(spec PromptSpec) {
	p.spec = spec
	ti := textinput.New()
	ti.Placeholder = spec.Placeholder
	ti.CharLimit = promptCharLimit
	ti.Prompt = "> "
	ti.SetValue(spec.Initial)
	ti.CursorEnd()
	ti.Focus()
	p.input = ti
	p.histBack = 0
	p.draft = ""
}

// Value returns the current prompt text.
func (p *promptOverlay) value() string { return p.input.Value() }

// handleKey: enter submits, esc cancels, ctrl+o hands off to the editor,
// up/down (ctrl+p/ctrl+n) walk the history, everything else edits the text.
func (p *promptOverlay) handleKey(msg tea.KeyMsg, _ keymap.Action) Outcome {
	switch msg.Type { //nolint:exhaustive // the remaining key types are text input
	case tea.KeyEnter:
		if strings.TrimSpace(p.input.Value()) == "" && !p.spec.AllowEmpty {
			return Outcome{Kind: OutcomeNone}
		}
		return Outcome{Kind: OutcomePromptSubmitted, PromptID: p.spec.ID, PromptValue: p.input.Value()}
	case tea.KeyEsc:
		return Outcome{Kind: OutcomeClosed}
	case tea.KeyCtrlO:
		if p.spec.Editor == "" {
			return Outcome{Kind: OutcomeNone}
		}
		return Outcome{Kind: OutcomePromptEditor, PromptID: p.spec.ID, PromptValue: p.input.Value()}
	case tea.KeyUp, tea.KeyCtrlP:
		p.recall(1)
		return Outcome{Kind: OutcomeNone}
	case tea.KeyDown, tea.KeyCtrlN:
		p.recall(-1)
		return Outcome{Kind: OutcomeNone}
	}
	p.input, _ = p.input.Update(msg)
	return Outcome{Kind: OutcomeNone}
}

// recall steps through History (newest first): up goes further back, down
// returns toward the fresh line. The first line of a multi-line entry is what
// the single-line input shows. leaving history restores the typed draft.
func (p *promptOverlay) recall(step int) {
	n := len(p.spec.History)
	if n == 0 {
		return
	}
	if p.histBack == 0 {
		p.draft = p.input.Value()
	}
	next := min(max(p.histBack+step, 0), n)
	if next == p.histBack {
		return
	}
	p.histBack = next
	if next == 0 {
		p.input.SetValue(p.draft)
	} else {
		p.input.SetValue(firstLine(p.spec.History[next-1]))
	}
	p.input.CursorEnd()
}

func firstLine(s string) string {
	head, _, _ := strings.Cut(s, "\n")
	return head
}

// handleMouse swallows events. the prompt is keyboard only.
func (p *promptOverlay) handleMouse(_ tea.MouseMsg) Outcome { return Outcome{Kind: OutcomeNone} }

func (p *promptOverlay) render(ctx RenderCtx, mgr *Manager) string {
	p.popupWidth = max(min(ctx.Width-promptMargin, promptMaxWidth), promptMinWidth)
	p.input.Width = max(p.popupWidth-promptBorderPad-4, 10)
	muted := string(ctx.Resolver.Color(style.ColorKeyMutedFg))
	reset := string(style.ResetFg)
	hints := []string{"[enter] confirm", "[esc] cancel"}
	if len(p.spec.History) > 0 {
		hints = append(hints, "[↑/↓] history")
	}
	if p.spec.Editor != "" {
		hints = append(hints, "[ctrl+o] "+p.spec.Editor)
	}
	parts := []string{p.input.View(), "", muted + strings.Join(hints, "  ") + reset}
	box := ctx.Resolver.Style(style.StyleKeyInfoBox).Width(p.popupWidth).Render(strings.Join(parts, "\n"))
	title := p.spec.Title
	if title == "" {
		title = "input"
	}
	return mgr.injectBorderTitle(box, " "+title+" ", borderEdgeText{
		popupWidth: p.popupWidth,
		accentFg:   string(ctx.Resolver.Color(style.ColorKeyAccentFg)),
		paneBg:     string(ctx.Resolver.Color(style.ColorKeyDiffPaneBg)),
	})
}
