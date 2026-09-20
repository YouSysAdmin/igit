package keymap

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDefaultCommit_bindings(t *testing.T) {
	km := DefaultCommit()
	tests := []struct {
		key    string
		action Action
	}{
		{" ", ActionStageToggle}, {"a", ActionStageAll}, {"u", ActionUnstageAll}, {"d", ActionDiscardChanges},
		{"c", ActionCommit}, {"C", ActionCommitEditor}, {"A", ActionAmend}, {"tab", ActionTogglePane}, {"I", ActionToggleStagedView},
		{"J", ActionScrollDiffDown}, {"K", ActionScrollDiffUp}, {"shift+down", ActionSelectExtendDown}, {"V", ActionVisualRange}, {"v", ActionHunkMode}, {"s", ActionStageHunk}, {"/", ActionSearch},
		{"S", ActionStash}, {"P", ActionPush}, {"F", ActionPull}, {"f", ActionFetch},
		{"n", ActionNextItem}, {"p", ActionPrevItem}, {"N", ActionPrevItem}, {"b", ActionCreate},
		{"1", ActionTabFiles}, {"2", ActionTabBranches}, {"3", ActionTabLog}, {"4", ActionTabStash},
		{"alt+g", ActionToggleMode}, {"q", ActionQuit}, {"?", ActionHelp}, {"esc", ActionDismiss},
		{"]", ActionNextHunk}, {"[", ActionPrevHunk}, {"j", ActionDown}, {"k", ActionUp},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.action, km.Resolve(tt.key), "key %q", tt.key)
	}
	// review-only actions are not bound in commit mode
	assert.Empty(t, km.KeysFor(ActionMarkReviewed))
	assert.Empty(t, km.KeysFor(ActionAnnotateFile))
}

func TestDefaultCommit_everyBoundActionHasHelpEntry(t *testing.T) {
	km := DefaultCommit()
	described := map[Action]bool{}
	for _, e := range km.descriptions {
		described[e.Action] = true
	}
	for key, a := range km.bindings {
		assert.True(t, described[a], "commit binding %q -> %q lacks a help entry", key, a)
	}
}

func TestIsValidAction_commitActions(t *testing.T) {
	assert.True(t, IsValidAction(ActionStageToggle))
	assert.True(t, IsValidAction(ActionPush))
	assert.True(t, IsValidAction(ActionSaveAs))
	assert.True(t, IsValidAction(ActionToggleMode))
	assert.False(t, IsValidAction("stage_everything"))
}

func TestDefaultCommit_sharesViewToggles(t *testing.T) {
	km := DefaultCommit()
	assert.Equal(t, ActionToggleTree, km.Resolve("t"))
	assert.Equal(t, ActionThemeSelect, km.Resolve("T"))
	assert.Equal(t, ActionToggleWrap, km.Resolve("w"))
}
