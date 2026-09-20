package keymap

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsValidAction(t *testing.T) {
	assert.True(t, IsValidAction(ActionQuit))
	assert.True(t, IsValidAction(ActionDown))
	assert.True(t, IsValidAction(ActionInfo))
	assert.True(t, IsValidAction(Action("commit_info")), "deprecated alias must validate")
	assert.False(t, IsValidAction(Action("nonexistent")))
	assert.False(t, IsValidAction(Action("")))
}

func TestResolveAction_deprecatedCommitInfoAlias(t *testing.T) {
	// pre-v0.27 keybinding files use "commit_info". the action was renamed
	// to "info" when the popup expanded. Existing user configs must keep
	// working - the parser rewrites the alias to the canonical name.
	canonical, deprecated, ok := resolveAction(Action("commit_info"))
	require.True(t, ok)
	assert.True(t, deprecated)
	assert.Equal(t, ActionInfo, canonical)

	// canonical name resolves to itself with no deprecation flag
	canonical, deprecated, ok = resolveAction(ActionInfo)
	require.True(t, ok)
	assert.False(t, deprecated)
	assert.Equal(t, ActionInfo, canonical)

	// unknown action stays unknown
	_, _, ok = resolveAction(Action("totally_made_up"))
	assert.False(t, ok)
}

func TestActionReload_IsValid(t *testing.T) {
	assert.True(t, IsValidAction(ActionReload))
}

func TestActionScrollDiff_IsValid(t *testing.T) {
	assert.True(t, IsValidAction(ActionScrollDiffDown))
	assert.True(t, IsValidAction(ActionScrollDiffUp))
}

func TestActionToggleCompact_IsValid(t *testing.T) {
	assert.True(t, IsValidAction(ActionToggleCompact))
}

func TestActionOpenEditor_IsValid(t *testing.T) {
	assert.True(t, IsValidAction(ActionOpenEditor))
}

func TestActionOpenFileInEditor_IsValid(t *testing.T) {
	assert.True(t, IsValidAction(ActionOpenFileInEditor))
}

func TestActionFlushOutput_IsValid(t *testing.T) {
	assert.True(t, IsValidAction(ActionFlushOutput))
}

func TestActionScrollConstants_InNavigationActions(t *testing.T) {
	// the three scroll-align actions must be recognized as valid so keybindings
	// files can map custom keys to them (e.g. "map z scroll_center").
	assert.True(t, IsValidAction(ActionScrollCenter))
	assert.True(t, IsValidAction(ActionScrollTop))
	assert.True(t, IsValidAction(ActionScrollBottom))
}

func TestScrollDiffPageActions_AreValid(t *testing.T) {
	for _, a := range []Action{
		ActionScrollDiffPageDown, ActionScrollDiffPageUp,
		ActionScrollDiffHalfPageDown, ActionScrollDiffHalfPageUp,
	} {
		assert.True(t, IsValidAction(a), "action %q must validate", a)
	}
}
