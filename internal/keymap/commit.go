package keymap

// Commit mode's defaults, the counterpart of review.go. Navigation, quit, help,
// theme and view-toggle actions are shared with review by name, so a rebind
// reads the same in either section.

// defaultCommitDescriptions returns the ordered help entries for commit mode.
func defaultCommitDescriptions() []HelpEntry {
	return []HelpEntry{
		// navigation
		{ActionDown, "move cursor down", "Navigation"},
		{ActionUp, "move cursor up", "Navigation"},
		{ActionPageDown, "page down", "Navigation"},
		{ActionPageUp, "page up", "Navigation"},
		{ActionHalfPageDown, "half page down", "Navigation"},
		{ActionHalfPageUp, "half page up", "Navigation"},
		{ActionHome, "go to top", "Navigation"},
		{ActionEnd, "go to bottom", "Navigation"},
		{ActionScrollLeft, "scroll left", "Navigation"},
		{ActionScrollRight, "scroll right", "Navigation"},
		{ActionScrollDiffDown, "scroll diff down", "Navigation"},
		{ActionScrollDiffUp, "scroll diff up", "Navigation"},
		{ActionNextHunk, "next hunk", "Navigation"},
		{ActionPrevHunk, "prev hunk", "Navigation"},
		{ActionNextItem, "next file / entry in the side pane", "Navigation"},
		{ActionPrevItem, "prev file / entry in the side pane", "Navigation"},

		// pane
		{ActionTogglePane, "toggle pane focus", SectionPane},
		{ActionFocusTree, "focus side pane", SectionPane},
		{ActionFocusDiff, "focus diff pane", SectionPane},
		{ActionTabFiles, "files tab", SectionPane},
		{ActionTabBranches, "branches tab", SectionPane},
		{ActionTabLog, "log tab", SectionPane},
		{ActionTabStash, "stash tab", SectionPane},
		{ActionNextTab, "next tab", SectionPane},
		{ActionPrevTab, "previous tab", SectionPane},

		// staging
		{ActionConfirm, "select / open", "Staging"},
		{ActionStageToggle, "stage or unstage the file, the cursor line or the selected lines (a conflict opens the resolve menu)", "Staging"},
		{ActionStageHunk, "stage or unstage the change block under the cursor (the file in the side pane)", "Staging"},
		{ActionStageAll, "stage all changes", "Staging"},
		{ActionUnstageAll, "unstage all changes", "Staging"},
		{ActionDiscardChanges, "discard changes (asks for confirmation)", "Staging"},
		{ActionToggleStagedView, "switch between unstaged and staged diff", "Staging"},
		{ActionSelectExtendDown, "extend line selection down", "Staging"},
		{ActionSelectExtendUp, "extend line selection up", "Staging"},
		{ActionVisualRange, "start / stop a visual line range", "Staging"},
		{ActionHunkMode, "toggle change-block selection", "Staging"},
		{ActionOpenFileInEditor, "open file in $EDITOR", "Staging"},

		// commit
		{ActionCommit, "commit staged changes", "Commit"},
		{ActionCommitEditor, "commit with $EDITOR message", "Commit"},
		{ActionAmend, "amend last commit", "Commit"},

		// stash
		{ActionStash, "stash menu", "Stash"},

		// branches
		{ActionCreate, "new branch (from the selected one) / new stash", "Branches"},
		{ActionRename, "rename branch", "Branches"},
		{ActionMerge, "merge branch into current", "Branches"},
		{ActionRebase, "rebase current branch onto the selected one", "Branches"},
		{ActionReset, "reset current branch to the selected one", "Branches"},
		{ActionSetUpstream, "set upstream", "Branches"},

		// sync
		{ActionPush, "push", "Sync"},
		{ActionPull, "pull", "Sync"},
		{ActionFetch, "fetch", "Sync"},
		{ActionAbortOrContinue, "abort or continue the merge, rebase or cherry-pick in progress", "Sync"},

		// view
		{ActionSearch, "search in diff", "View"},
		{ActionToggleTree, "toggle side pane", "View"},
		{ActionToggleWrap, "toggle word wrap", "View"},
		{ActionToggleLineNums, "toggle line numbers", "View"},
		{ActionToggleWordDiff, "toggle word-diff highlighting", "View"},
		{ActionThemeSelect, "theme selector", "View"},
		{ActionReload, "refresh status", "View"},

		// mode
		{ActionToggleMode, "switch between review and commit mode", SectionMode},

		// quit
		{ActionQuit, "quit", "Quit"},
		{ActionHelp, "show help", "Quit"},
		{ActionDismiss, "dismiss / cancel", "Quit"},
	}
}

// defaultCommitBindings returns the default key-to-action mapping for commit mode.
func defaultCommitBindings() map[string]Action {
	return map[string]Action{
		"j":          ActionDown,
		"k":          ActionUp,
		"down":       ActionDown,
		"up":         ActionUp,
		"pgdown":     ActionPageDown,
		"pgup":       ActionPageUp,
		"ctrl+d":     ActionHalfPageDown,
		"ctrl+u":     ActionHalfPageUp,
		"home":       ActionHome,
		"end":        ActionEnd,
		"left":       ActionScrollLeft,
		"right":      ActionScrollRight,
		"J":          ActionScrollDiffDown,
		"K":          ActionScrollDiffUp,
		"]":          ActionNextHunk,
		"[":          ActionPrevHunk,
		"n":          ActionNextItem,
		"p":          ActionPrevItem,
		"N":          ActionPrevItem,
		"tab":        ActionTogglePane,
		"I":          ActionToggleStagedView,
		"h":          ActionFocusTree,
		"l":          ActionFocusDiff,
		"1":          ActionTabFiles,
		"2":          ActionTabBranches,
		"3":          ActionTabLog,
		"4":          ActionTabStash,
		">":          ActionNextTab,
		"<":          ActionPrevTab,
		"enter":      ActionConfirm,
		" ":          ActionStageToggle,
		"s":          ActionStageHunk,
		"a":          ActionStageAll,
		"u":          ActionUnstageAll,
		"d":          ActionDiscardChanges,
		"shift+down": ActionSelectExtendDown,
		"shift+up":   ActionSelectExtendUp,
		"V":          ActionVisualRange,
		"v":          ActionHunkMode,
		"/":          ActionSearch,
		"e":          ActionOpenFileInEditor,
		"c":          ActionCommit,
		"C":          ActionCommitEditor,
		"A":          ActionAmend,
		"S":          ActionStash,
		"b":          ActionCreate,
		"r":          ActionRename,
		"m":          ActionMerge,
		"o":          ActionRebase,
		"g":          ActionReset,
		"U":          ActionSetUpstream,
		"P":          ActionPush,
		"F":          ActionPull,
		"f":          ActionFetch,
		"t":          ActionToggleTree,
		"w":          ActionToggleWrap,
		"L":          ActionToggleLineNums,
		"W":          ActionToggleWordDiff,
		"T":          ActionThemeSelect,
		"R":          ActionReload,
		"M":          ActionAbortOrContinue,
		"alt+g":      ActionToggleMode,
		"q":          ActionQuit,
		"?":          ActionHelp,
		"esc":        ActionDismiss,
	}
}

// DefaultCommit returns a Keymap with the default commit-mode bindings.
func DefaultCommit() *Keymap {
	return &Keymap{
		bindings:     defaultCommitBindings(),
		descriptions: defaultCommitDescriptions(),
	}
}
