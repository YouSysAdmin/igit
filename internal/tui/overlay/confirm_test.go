package overlay

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yousysadmin/igit/internal/keymap"
	"github.com/yousysadmin/igit/internal/tui/style"
)

func popupCtx() RenderCtx {
	return RenderCtx{Width: 100, Height: 30, Resolver: style.PlainResolver()}
}

func runes(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

func TestConfirmOverlay_keys(t *testing.T) {
	cases := []struct {
		name string
		msg  tea.KeyMsg
		want OutcomeKind
	}{
		{"y confirms", runes("y"), OutcomeConfirmed},
		{"Y confirms", runes("Y"), OutcomeConfirmed},
		{"enter confirms", tea.KeyMsg{Type: tea.KeyEnter}, OutcomeConfirmed},
		{"n cancels", runes("n"), OutcomeClosed},
		{"q cancels", runes("q"), OutcomeClosed},
		{"esc cancels", tea.KeyMsg{Type: tea.KeyEsc}, OutcomeClosed},
		{"other key ignored", runes("x"), OutcomeNone},
		{"multi-rune ignored", runes("yy"), OutcomeNone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mgr := NewManager()
			mgr.OpenConfirm(ConfirmSpec{ID: "discard:a.go", Title: "Discard", Body: "Throw away changes to a.go?", Danger: true})
			require.True(t, mgr.Active())
			assert.Equal(t, KindConfirm, mgr.Kind())
			out := mgr.HandleKey(tc.msg, "")
			assert.Equal(t, tc.want, out.Kind)
			if tc.want == OutcomeConfirmed {
				assert.Equal(t, "discard:a.go", out.ConfirmID)
			}
			assert.Equal(t, tc.want == OutcomeNone, mgr.Active(), "overlay closes on every terminal outcome")
		})
	}
	mgr := NewManager()
	mgr.OpenConfirm(ConfirmSpec{ID: "x"})
	assert.Equal(t, OutcomeClosed, mgr.HandleKey(runes("z"), keymap.ActionDismiss).Kind, "dismiss action closes")
}

func TestConfirmOverlay_renderAndMouse(t *testing.T) {
	mgr := NewManager()
	mgr.OpenConfirm(ConfirmSpec{ID: "x", Title: "Discard", Body: "Throw away every change to a very long file name that wraps across the popup width for sure?", Yes: "d"})
	view := mgr.Compose(strings.Repeat(strings.Repeat(" ", 100)+"\n", 29), popupCtx())
	assert.Contains(t, view, "Discard")
	assert.Contains(t, view, "[d] confirm")
	assert.Contains(t, view, "Throw away")
	out := mgr.HandleMouse(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft, X: 50, Y: 15})
	assert.Equal(t, OutcomeNone, out.Kind)
	assert.True(t, mgr.Active(), "clicks never dismiss a confirmation")

	mgr.OpenConfirm(ConfirmSpec{})
	view = mgr.Compose(strings.Repeat(strings.Repeat(" ", 100)+"\n", 29), popupCtx())
	assert.Contains(t, view, "confirm")
	assert.Contains(t, view, "[y] confirm")
}

func TestWrapLines(t *testing.T) {
	assert.Equal(t, []string{"aaa bbb", "ccc"}, wrapLines("aaa bbb ccc", 8))
	assert.Equal(t, []string{"one", "two"}, wrapLines("one\ntwo", 20))
	assert.Equal(t, []string{"toolongword", "x"}, wrapLines("toolongword x", 5))
	assert.Equal(t, []string{"a b"}, wrapLines("a b", 0))
	assert.Equal(t, []string{""}, wrapLines("", 10))
}

func TestMenuOverlay(t *testing.T) {
	spec := MenuSpec{Title: "Discard", Items: []MenuItem{
		{Key: 'u', Label: "discard unstaged changes", ID: "unstaged"},
		{Key: 'a', Label: "discard all changes", ID: "all"},
		{Label: "no shortcut", ID: "plain"},
	}}
	mgr := NewManager()
	mgr.OpenMenu(spec)
	assert.Equal(t, KindMenu, mgr.Kind())
	out := mgr.HandleKey(runes("a"), keymap.ActionStageAll)
	assert.Equal(t, OutcomeMenuChosen, out.Kind)
	assert.Equal(t, "all", out.MenuChoice)
	assert.False(t, mgr.Active())

	mgr.OpenMenu(spec)
	mgr.HandleKey(tea.KeyMsg{Type: tea.KeyDown}, keymap.ActionDown)
	mgr.HandleKey(runes("j"), keymap.ActionDown)
	mgr.HandleKey(runes("j"), keymap.ActionDown) // clamps at the end
	out = mgr.HandleKey(tea.KeyMsg{Type: tea.KeyEnter}, "")
	assert.Equal(t, "plain", out.MenuChoice)

	mgr.OpenMenu(spec)
	mgr.HandleMouse(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown})
	mgr.HandleMouse(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelUp})
	mgr.HandleMouse(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelUp})
	assert.Equal(t, 0, mgr.menu.cursor)
	view := mgr.Compose(strings.Repeat(strings.Repeat(" ", 100)+"\n", 29), popupCtx())
	assert.Contains(t, view, "[u] discard unstaged changes")
	assert.Contains(t, view, "Discard")
	assert.Equal(t, OutcomeClosed, mgr.HandleKey(tea.KeyMsg{Type: tea.KeyEsc}, "").Kind)

	mgr.OpenMenu(MenuSpec{})
	assert.Equal(t, OutcomeNone, mgr.HandleKey(tea.KeyMsg{Type: tea.KeyEnter}, "").Kind, "empty menu has nothing to choose")
	assert.Contains(t, mgr.Compose(strings.Repeat(strings.Repeat(" ", 100)+"\n", 29), popupCtx()), "menu")
}

func TestErrorOverlay(t *testing.T) {
	detail := strings.Repeat("line of stderr output\n", 40)
	mgr := NewManager()
	mgr.OpenError(ErrorSpec{Title: "git apply failed", Summary: "git apply --cached -: error: patch failed", Detail: detail})
	assert.Equal(t, KindError, mgr.Kind())
	ctx := popupCtx()
	view := mgr.Compose(strings.Repeat(strings.Repeat(" ", 100)+"\n", 29), ctx)
	assert.Contains(t, view, "git apply failed")
	assert.Contains(t, view, "patch failed")
	assert.Contains(t, view, "scroll")

	// scrolling keeps it open. any other key closes it
	assert.Equal(t, OutcomeNone, mgr.HandleKey(runes("j"), keymap.ActionDown).Kind)
	assert.Equal(t, 1, mgr.errPop.offset)
	assert.Equal(t, OutcomeNone, mgr.HandleKey(tea.KeyMsg{Type: tea.KeyDown}, "").Kind)
	assert.Equal(t, 2, mgr.errPop.offset)
	assert.Equal(t, OutcomeNone, mgr.HandleKey(tea.KeyMsg{Type: tea.KeyUp}, "").Kind)
	assert.Equal(t, OutcomeNone, mgr.HandleKey(runes(" "), keymap.ActionPageDown).Kind)
	assert.Equal(t, OutcomeNone, mgr.HandleKey(runes(" "), keymap.ActionEnd).Kind)
	mgr.Compose(strings.Repeat(strings.Repeat(" ", 100)+"\n", 29), ctx)
	assert.Positive(t, mgr.errPop.offset)
	assert.Equal(t, OutcomeNone, mgr.HandleKey(runes(" "), keymap.ActionHome).Kind)
	assert.Equal(t, 0, mgr.errPop.offset)
	assert.Equal(t, OutcomeNone, mgr.HandleMouse(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown}).Kind)
	assert.Equal(t, WheelStep, mgr.errPop.offset)
	mgr.HandleMouse(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelUp})
	assert.Equal(t, 0, mgr.errPop.offset)
	assert.Equal(t, OutcomeClosed, mgr.HandleKey(runes("x"), "").Kind)
	assert.False(t, mgr.Active())

	mgr.OpenError(ErrorSpec{Detail: "short"})
	view = mgr.Compose(strings.Repeat(strings.Repeat(" ", 100)+"\n", 29), ctx)
	assert.Contains(t, view, "[any key] close")
	assert.Contains(t, view, "error")
}
