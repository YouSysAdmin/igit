package overlay

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/yousysadmin/igit/internal/keymap"
	"github.com/yousysadmin/igit/internal/tui/style"
)

const (
	errorMaxWidth    = 100
	errorMinWidth    = 30
	errorMargin      = 6
	errorBorderPad   = 4
	errorChromeLines = 6
)

// ErrorSpec describes a scrollable message popup: Summary is the headline,
// Detail the full text (a git command's stderr, for example).
type ErrorSpec struct {
	Title   string
	Summary string
	Detail  string
}

type errorOverlay struct {
	spec       ErrorSpec
	offset     int
	viewport   int
	popupWidth int
}

func (e *errorOverlay) open(spec ErrorSpec) {
	e.spec = spec
	e.offset = 0
}

// handleKey scrolls with the navigation actions and closes on any other key.
func (e *errorOverlay) handleKey(msg tea.KeyMsg, action keymap.Action) Outcome {
	page := max(e.viewport, 1)
	switch action {
	case keymap.ActionDown:
		e.offset++
	case keymap.ActionUp:
		e.offset = max(e.offset-1, 0)
	case keymap.ActionPageDown, keymap.ActionHalfPageDown:
		e.offset += page
	case keymap.ActionPageUp, keymap.ActionHalfPageUp:
		e.offset = max(e.offset-page, 0)
	case keymap.ActionHome:
		e.offset = 0
	case keymap.ActionEnd:
		e.offset = scrollEndSentinel
	default:
		if msg.Type == tea.KeyDown {
			e.offset++
			break
		}
		if msg.Type == tea.KeyUp {
			e.offset = max(e.offset-1, 0)
			break
		}
		return Outcome{Kind: OutcomeClosed}
	}
	return Outcome{Kind: OutcomeNone}
}

func (e *errorOverlay) handleMouse(msg tea.MouseMsg) Outcome {
	if msg.Action != tea.MouseActionPress {
		return Outcome{Kind: OutcomeNone}
	}
	switch msg.Button { //nolint:exhaustive // only the wheel scrolls
	case tea.MouseButtonWheelDown:
		e.offset += WheelStep
	case tea.MouseButtonWheelUp:
		e.offset = max(e.offset-WheelStep, 0)
	}
	return Outcome{Kind: OutcomeNone}
}

func (e *errorOverlay) render(ctx RenderCtx, mgr *Manager) string {
	e.popupWidth = max(min(ctx.Width-errorMargin, errorMaxWidth), errorMinWidth)
	width := e.popupWidth - errorBorderPad
	var body []string
	if e.spec.Summary != "" {
		body = append(body, string(ctx.Resolver.Color(style.ColorKeyRemoveLineFg))+e.spec.Summary+string(style.ResetFg), "")
	}
	for line := range strings.SplitSeq(strings.TrimRight(e.spec.Detail, "\n"), "\n") {
		body = append(body, wrapLines(line, width)...)
	}
	e.viewport = max(ctx.Height-errorChromeLines, 1)
	if maxOffset := max(len(body)-e.viewport, 0); e.offset > maxOffset {
		e.offset = maxOffset
	}
	end := min(e.offset+e.viewport, len(body))
	parts := append([]string{}, body[e.offset:end]...)
	muted := string(ctx.Resolver.Color(style.ColorKeyMutedFg))
	footer := "[any key] close"
	if len(body) > e.viewport {
		footer = "↑/↓ scroll · [esc] close"
	}
	parts = append(parts, "", muted+footer+string(style.ResetFg))
	box := ctx.Resolver.Style(style.StyleKeyInfoBox).Width(e.popupWidth).Render(strings.Join(parts, "\n"))
	title := e.spec.Title
	if title == "" {
		title = "error"
	}
	return mgr.injectBorderTitle(box, " "+title+" ", borderEdgeText{
		popupWidth: e.popupWidth,
		accentFg:   string(ctx.Resolver.Color(style.ColorKeyAccentFg)),
		paneBg:     string(ctx.Resolver.Color(style.ColorKeyDiffPaneBg)),
	})
}
