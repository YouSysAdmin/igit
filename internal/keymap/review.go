package keymap

// Review mode's defaults: which actions it offers, in which help section, and
// the keys they start out on.

// defaultDescriptions returns the ordered help entries grouped by section.
func defaultDescriptions() []HelpEntry {
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
		{ActionScrollRight, "scroll right / focus diff", "Navigation"},
		{ActionScrollCenter, "center viewport on cursor", "Navigation"},
		{ActionScrollTop, "align viewport top", "Navigation"},
		{ActionScrollBottom, "align viewport bottom", "Navigation"},
		{ActionScrollDiffDown, "scroll diff down", "Navigation"},
		{ActionScrollDiffUp, "scroll diff up", "Navigation"},
		{ActionScrollDiffPageDown, "scroll diff one page down", "Navigation"},
		{ActionScrollDiffPageUp, "scroll diff one page up", "Navigation"},
		{ActionScrollDiffHalfPageDown, "scroll diff half a page down", "Navigation"},
		{ActionScrollDiffHalfPageUp, "scroll diff half a page up", "Navigation"},

		// file/hunk
		{ActionNextItem, "next file / search match", "File/Hunk"},
		{ActionPrevItem, "prev file / search match", "File/Hunk"},
		{ActionJumpFile, "jump to file", "File/Hunk"},
		{ActionNextHunk, "next hunk", "File/Hunk"},
		{ActionPrevHunk, "prev hunk", "File/Hunk"},
		{ActionOpenFileInEditor, "open focused file in $EDITOR", "File/Hunk"},

		// pane
		{ActionTogglePane, "toggle pane focus", SectionPane},
		{ActionFocusTree, "focus tree pane", SectionPane},
		{ActionFocusDiff, "focus diff pane", SectionPane},

		// search
		{ActionSearch, "search in diff", "Search"},

		// annotations
		{ActionConfirm, "annotate line / select file", "Annotations"},
		{ActionAnnotateFile, "annotate file", "Annotations"},
		{ActionDeleteAnnotation, "delete annotation", "Annotations"},
		{ActionAnnotList, "annotation list", "Annotations"},
		{ActionOpenEditor, "open annotation in $EDITOR", "Annotations"},
		{ActionAnnotationNewline, "line break while typing an annotation", "Annotations"},
		{ActionNextAnnotation, "next annotation (across files)", "Annotations"},
		{ActionPrevAnnotation, "previous annotation (across files)", "Annotations"},
		{ActionFlushOutput, "flush annotations to the output file", "Annotations"},
		{ActionSaveAs, "save annotations to a file (prompts for path)", "Annotations"},

		// view toggles
		{ActionToggleCollapsed, "toggle collapsed view", "View"},
		{ActionToggleCompact, "toggle compact diff view", "View"},
		{ActionToggleWrap, "toggle word wrap", "View"},
		{ActionToggleTree, "toggle tree pane", "View"},
		{ActionToggleLineNums, "toggle line numbers", "View"},
		{ActionToggleBlame, "toggle blame gutter", "View"},
		{ActionToggleWordDiff, "toggle word-diff highlighting", "View"},
		{ActionToggleHunk, "toggle hunk in collapsed", "View"},
		{ActionToggleUntracked, "untracked files: show or hide", "View"},
		{ActionMarkReviewed, "mark file as reviewed", "View"},
		{ActionFilterUnreviewed, "show unreviewed files", "View"},
		{ActionFilter, "filter files", "View"},
		{ActionThemeSelect, "theme selector", "View"},
		{ActionInfo, "show review info popup", "View"},
		{ActionReload, "reload diff from VCS", "View"},

		// stage plan
		{ActionStageMark, "mark hunk / selected lines / file for the commit", SectionStage},
		{ActionStageMarkFile, "mark whole file for the commit", SectionStage},
		{ActionVisualRange, "toggle line range selection", SectionStage},
		{ActionCommitWithPlan, "stage marked changes and open commit mode", SectionStage},

		// mode
		{ActionToggleMode, "switch between review and commit mode", SectionMode},

		// quit
		{ActionQuit, "quit", "Quit"},
		{ActionQuitDiscarding, "quit, discarding the annotations", "Quit"},
		{ActionHelp, "show help", "Quit"},
		{ActionDismiss, "dismiss / cancel", "Quit"},
	}
}

// defaultBindings returns the default key-to-action mapping.
func defaultBindings() map[string]Action {
	return map[string]Action{
		"j":         ActionDown,
		"k":         ActionUp,
		"down":      ActionDown,
		"up":        ActionUp,
		"pgdown":    ActionPageDown,
		"pgup":      ActionPageUp,
		"ctrl+d":    ActionHalfPageDown,
		"ctrl+u":    ActionHalfPageUp,
		"home":      ActionHome,
		"end":       ActionEnd,
		"left":      ActionScrollLeft,
		"right":     ActionScrollRight,
		"J":         ActionScrollDiffDown,
		"K":         ActionScrollDiffUp,
		"n":         ActionNextItem,
		"N":         ActionPrevItem,
		"p":         ActionPrevItem,
		"P":         ActionJumpFile,
		"]":         ActionNextHunk,
		"[":         ActionPrevHunk,
		"e":         ActionOpenFileInEditor,
		"tab":       ActionTogglePane,
		"h":         ActionFocusTree,
		"l":         ActionFocusDiff,
		"/":         ActionSearch,
		"a":         ActionConfirm,
		"enter":     ActionConfirm,
		"A":         ActionAnnotateFile,
		"d":         ActionDeleteAnnotation,
		"@":         ActionAnnotList,
		"ctrl+e":    ActionOpenEditor,
		"alt+enter": ActionAnnotationNewline,
		"}":         ActionNextAnnotation,
		"{":         ActionPrevAnnotation,
		"O":         ActionFlushOutput,
		"ctrl+s":    ActionSaveAs,
		"alt+g":     ActionToggleMode,
		"s":         ActionStageMark,
		"S":         ActionStageMarkFile,
		"V":         ActionVisualRange,
		"c":         ActionCommitWithPlan,
		"v":         ActionToggleCollapsed,
		"C":         ActionToggleCompact,
		"w":         ActionToggleWrap,
		"t":         ActionToggleTree,
		"L":         ActionToggleLineNums,
		"B":         ActionToggleBlame,
		"W":         ActionToggleWordDiff,
		".":         ActionToggleHunk,
		" ":         ActionMarkReviewed,
		"F":         ActionFilterUnreviewed,
		"u":         ActionToggleUntracked,
		"f":         ActionFilter,
		"q":         ActionQuit,
		"Q":         ActionQuitDiscarding,
		"?":         ActionHelp,
		"T":         ActionThemeSelect,
		"i":         ActionInfo,
		"R":         ActionReload,
		"esc":       ActionDismiss,
	}
}

// Default returns a Keymap with all default bindings.
func Default() *Keymap {
	return &Keymap{
		bindings:     defaultBindings(),
		descriptions: defaultDescriptions(),
	}
}
