package keymap

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefault(t *testing.T) {
	km := Default()
	require.NotNil(t, km)
	assert.NotEmpty(t, km.bindings)
	assert.NotEmpty(t, km.descriptions)
}

func TestDefault_allExpectedBindings(t *testing.T) {
	km := Default()
	tests := []struct {
		key    string
		action Action
	}{
		{"j", ActionDown}, {"k", ActionUp}, {"down", ActionDown}, {"up", ActionUp},
		{"pgdown", ActionPageDown}, {"pgup", ActionPageUp},
		{"ctrl+d", ActionHalfPageDown}, {"ctrl+u", ActionHalfPageUp},
		{"home", ActionHome}, {"end", ActionEnd},
		{"left", ActionScrollLeft}, {"right", ActionScrollRight},
		{"J", ActionScrollDiffDown}, {"K", ActionScrollDiffUp},
		{"n", ActionNextItem}, {"N", ActionPrevItem}, {"p", ActionPrevItem},
		{"P", ActionJumpFile},
		{"]", ActionNextHunk}, {"[", ActionPrevHunk}, {"e", ActionOpenFileInEditor},
		{"tab", ActionTogglePane}, {"h", ActionFocusTree}, {"l", ActionFocusDiff},
		{"/", ActionSearch},
		{"a", ActionConfirm}, {"enter", ActionConfirm},
		{"A", ActionAnnotateFile}, {"d", ActionDeleteAnnotation}, {"@", ActionAnnotList}, {"ctrl+e", ActionOpenEditor},
		{"alt+enter", ActionAnnotationNewline},
		{"}", ActionNextAnnotation}, {"{", ActionPrevAnnotation}, {"O", ActionFlushOutput},
		{"v", ActionToggleCollapsed}, {"C", ActionToggleCompact}, {"w", ActionToggleWrap}, {"t", ActionToggleTree},
		{"L", ActionToggleLineNums}, {"B", ActionToggleBlame}, {"W", ActionToggleWordDiff},
		{".", ActionToggleHunk}, {" ", ActionMarkReviewed}, {"f", ActionFilter}, {"F", ActionFilterUnreviewed},
		{"u", ActionToggleUntracked},
		{"q", ActionQuit}, {"Q", ActionQuitDiscarding}, {"?", ActionHelp}, {"T", ActionThemeSelect}, {"esc", ActionDismiss},
		{"i", ActionInfo},
		{"R", ActionReload},
		{"ctrl+s", ActionSaveAs}, {"alt+g", ActionToggleMode},
		{"s", ActionStageMark}, {"S", ActionStageMarkFile}, {"V", ActionVisualRange}, {"c", ActionCommitWithPlan},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.action, km.Resolve(tt.key), "key %q should map to %q", tt.key, tt.action)
	}
	// verify binding count matches expected
	assert.Len(t, km.bindings, len(tests), "default keymap should have exactly %d bindings", len(tests))
}

func TestDefault_specialKeysMatchBubbletea(t *testing.T) {
	// verify that our key names match what bubbletea's KeyMsg.String() actually returns
	tests := []struct {
		keyType tea.KeyType
		want    string
	}{
		{tea.KeyPgDown, "pgdown"},
		{tea.KeyPgUp, "pgup"},
		{tea.KeyHome, "home"},
		{tea.KeyEnd, "end"},
		{tea.KeyUp, "up"},
		{tea.KeyDown, "down"},
		{tea.KeyLeft, "left"},
		{tea.KeyRight, "right"},
		{tea.KeyEnter, "enter"},
		{tea.KeyEsc, "esc"},
		{tea.KeyTab, "tab"},
	}

	km := Default()
	for _, tt := range tests {
		msg := tea.KeyMsg{Type: tt.keyType}
		actual := msg.String()
		assert.Equal(t, tt.want, actual, "bubbletea KeyType %d String() should be %q", tt.keyType, tt.want)
		// and verify the default keymap has a binding for it
		action := km.Resolve(actual)
		assert.NotEmpty(t, action, "default keymap should have binding for %q", actual)
	}
}

func TestDefault_ctrlKeysMatchBubbletea(t *testing.T) {
	// ctrl+d and ctrl+u: bubbletea represents these as KeyMsg with specific types
	ctrlD := tea.KeyMsg{Type: tea.KeyCtrlD}
	ctrlU := tea.KeyMsg{Type: tea.KeyCtrlU}

	km := Default()
	assert.Equal(t, ActionHalfPageDown, km.Resolve(ctrlD.String()))
	assert.Equal(t, ActionHalfPageUp, km.Resolve(ctrlU.String()))
}
