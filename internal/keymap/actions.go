package keymap

import (
	"log"
	"sync"
)

// The action names a key can be bound to: first the ones review mode offers,
// which include everything both modes share, then commit mode's own. A name
// means the same thing in either mode, so a binding file reads the same way
// whichever section it is in.

// Action represents a named action that a key can trigger.
type Action string

// action constants for all mappable actions.
const (
	ActionDown                   Action = "down"
	ActionUp                     Action = "up"
	ActionPageDown               Action = "page_down"
	ActionPageUp                 Action = "page_up"
	ActionHalfPageDown           Action = "half_page_down"
	ActionHalfPageUp             Action = "half_page_up"
	ActionHome                   Action = "home"
	ActionEnd                    Action = "end"
	ActionScrollLeft             Action = "scroll_left"
	ActionScrollRight            Action = "scroll_right"
	ActionScrollCenter           Action = "scroll_center"
	ActionScrollTop              Action = "scroll_top"
	ActionScrollBottom           Action = "scroll_bottom"
	ActionScrollDiffDown         Action = "scroll_diff_down"
	ActionScrollDiffUp           Action = "scroll_diff_up"
	ActionScrollDiffPageDown     Action = "scroll_diff_page_down"
	ActionScrollDiffPageUp       Action = "scroll_diff_page_up"
	ActionScrollDiffHalfPageDown Action = "scroll_diff_half_page_down"
	ActionScrollDiffHalfPageUp   Action = "scroll_diff_half_page_up"
	ActionNextItem               Action = "next_item"
	ActionPrevItem               Action = "prev_item"
	ActionJumpFile               Action = "jump_file"
	ActionNextHunk               Action = "next_hunk"
	ActionPrevHunk               Action = "prev_hunk"
	ActionTogglePane             Action = "toggle_pane"
	ActionFocusTree              Action = "focus_tree"
	ActionFocusDiff              Action = "focus_diff"
	ActionSearch                 Action = "search"
	ActionConfirm                Action = "confirm"
	ActionAnnotateFile           Action = "annotate_file"
	ActionDeleteAnnotation       Action = "delete_annotation"
	ActionAnnotList              Action = "annot_list"
	ActionNextAnnotation         Action = "next_annotation"
	ActionPrevAnnotation         Action = "prev_annotation"
	ActionToggleCollapsed        Action = "toggle_collapsed"
	ActionToggleCompact          Action = "toggle_compact"
	ActionToggleWrap             Action = "toggle_wrap"
	ActionToggleTree             Action = "toggle_tree"
	ActionToggleLineNums         Action = "toggle_line_numbers"
	ActionToggleBlame            Action = "toggle_blame"
	ActionToggleWordDiff         Action = "toggle_word_diff"
	ActionToggleHunk             Action = "toggle_hunk"
	ActionToggleUntracked        Action = "toggle_untracked"
	ActionMarkReviewed           Action = "mark_reviewed"
	ActionFilterUnreviewed       Action = "filter_unreviewed"
	ActionFilter                 Action = "filter"
	ActionQuit                   Action = "quit"
	ActionQuitDiscarding         Action = "quit_discarding"
	ActionHelp                   Action = "help"
	ActionDismiss                Action = "dismiss"
	ActionThemeSelect            Action = "theme_select"
	ActionInfo                   Action = "info"
	ActionReload                 Action = "reload"
	ActionOpenEditor             Action = "open_editor"
	ActionAnnotationNewline      Action = "annotation_newline"
	ActionOpenFileInEditor       Action = "open_file_in_editor"
	ActionFlushOutput            Action = "flush_output"
	ActionSaveAs                 Action = "save_as"
	ActionToggleMode             Action = "toggle_mode"
	ActionStageMark              Action = "stage_mark"
	ActionStageMarkFile          Action = "stage_mark_file"
	ActionCommitWithPlan         Action = "commit_with_plan"
)

// commit-mode actions. Navigation, quit, help, theme and view-toggle actions
// are shared with the review keymap by name. the constants below exist only
// in commit mode.
const (
	ActionStageToggle      Action = "stage_toggle"
	ActionStageHunk        Action = "stage_hunk"
	ActionStageAll         Action = "stage_all"
	ActionUnstageAll       Action = "unstage_all"
	ActionDiscardChanges   Action = "discard_changes"
	ActionCommit           Action = "commit"
	ActionCommitEditor     Action = "commit_editor"
	ActionAmend            Action = "amend"
	ActionToggleStagedView Action = "toggle_staged_view"
	ActionSelectExtendDown Action = "select_extend_down"
	ActionSelectExtendUp   Action = "select_extend_up"
	ActionVisualRange      Action = "visual_range"
	ActionHunkMode         Action = "hunk_mode"
	ActionStash            Action = "stash"
	ActionPush             Action = "push"
	ActionPull             Action = "pull"
	ActionFetch            Action = "fetch"
	ActionCreate           Action = "create"
	ActionRename           Action = "rename"
	ActionMerge            Action = "merge"
	ActionRebase           Action = "rebase"
	ActionReset            Action = "reset"
	ActionSetUpstream      Action = "set_upstream"
	ActionAbortOrContinue  Action = "abort_or_continue"
	ActionTabFiles         Action = "tab_files"
	ActionTabBranches      Action = "tab_branches"
	ActionTabLog           Action = "tab_log"
	ActionTabStash         Action = "tab_stash"
	ActionNextTab          Action = "next_tab"
	ActionPrevTab          Action = "prev_tab"
)

// commitActions lists the commit-only action names for validation.
var commitActions = map[Action]bool{
	ActionStageToggle: true, ActionStageHunk: true, ActionStageAll: true, ActionUnstageAll: true, ActionDiscardChanges: true,
	ActionCommit: true, ActionCommitEditor: true, ActionAmend: true, ActionToggleStagedView: true,
	ActionSelectExtendDown: true, ActionSelectExtendUp: true, ActionVisualRange: true, ActionHunkMode: true,
	ActionStash: true, ActionPush: true, ActionPull: true, ActionFetch: true,
	ActionCreate: true, ActionRename: true, ActionMerge: true, ActionRebase: true, ActionReset: true, ActionSetUpstream: true,
	ActionAbortOrContinue: true,
	ActionTabFiles:        true, ActionTabBranches: true, ActionTabLog: true, ActionTabStash: true,
	ActionNextTab: true, ActionPrevTab: true,
}

// SectionPane is the help section name for pane-related keybindings.
const SectionPane = "Pane"

// SectionMode is the help section name for the review/commit mode switch.
const SectionMode = "Mode"

// SectionStage is the help section name for the review-mode stage plan.
const SectionStage = "Stage"

// validActions contains all known action names for validation.
var validActions = map[Action]bool{
	ActionDown: true, ActionUp: true, ActionPageDown: true, ActionPageUp: true,
	ActionHalfPageDown: true, ActionHalfPageUp: true, ActionHome: true, ActionEnd: true,
	ActionScrollLeft: true, ActionScrollRight: true,
	ActionScrollCenter: true, ActionScrollTop: true, ActionScrollBottom: true,
	ActionScrollDiffDown: true, ActionScrollDiffUp: true,
	ActionScrollDiffPageDown: true, ActionScrollDiffPageUp: true,
	ActionScrollDiffHalfPageDown: true, ActionScrollDiffHalfPageUp: true,
	ActionNextItem: true, ActionPrevItem: true, ActionJumpFile: true,
	ActionNextHunk: true, ActionPrevHunk: true,
	ActionTogglePane: true, ActionFocusTree: true, ActionFocusDiff: true,
	ActionSearch:  true,
	ActionConfirm: true, ActionAnnotateFile: true, ActionDeleteAnnotation: true, ActionAnnotList: true,
	ActionNextAnnotation: true, ActionPrevAnnotation: true,
	ActionToggleCollapsed: true, ActionToggleCompact: true, ActionToggleWrap: true, ActionToggleTree: true,
	ActionToggleLineNums: true, ActionToggleBlame: true, ActionToggleWordDiff: true, ActionToggleHunk: true,
	ActionMarkReviewed: true, ActionFilterUnreviewed: true, ActionFilter: true, ActionToggleUntracked: true,
	ActionQuit: true, ActionQuitDiscarding: true, ActionHelp: true, ActionDismiss: true, ActionThemeSelect: true,
	ActionInfo:              true,
	ActionReload:            true,
	ActionOpenEditor:        true,
	ActionAnnotationNewline: true,
	ActionOpenFileInEditor:  true,
	ActionFlushOutput:       true,
	ActionSaveAs:            true,
	ActionToggleMode:        true,
	ActionStageMark:         true,
	ActionStageMarkFile:     true,
	ActionCommitWithPlan:    true,
}

// deprecatedActionAliases maps obsolete action names parsed from user
// keybinding files onto their canonical replacement. The action was renamed
// from "commit_info" to "info" when the popup expanded to cover description
// and aggregate stats. honoring the old name lets pre-existing
// ~/.config/igit/keybindings files keep working without manual edits.
// The parser surfaces a single [WARN] per deprecated alias for the lifetime
// of the process (see warnOnceDeprecatedAlias) so that a file with several
// "map ... commit_info" lines does not spam the log.
var deprecatedActionAliases = map[Action]Action{
	"commit_info": ActionInfo,
}

// IsValidAction returns true if the action name is recognized. Deprecated
// aliases also report true so the parser accepts them. resolveAction performs
// the rewrite to the canonical name before storage.
func IsValidAction(a Action) bool {
	if validActions[a] || commitActions[a] {
		return true
	}
	_, ok := deprecatedActionAliases[a]
	return ok
}

// resolveAction returns the canonical Action for a, rewriting any deprecated
// alias to its replacement. ok is false when a is neither a valid action nor a
// known alias. Returns the canonical action plus a deprecated flag so callers
// can surface a one-time warning to the user.
func resolveAction(a Action) (canonical Action, deprecated, ok bool) {
	if validActions[a] || commitActions[a] {
		return a, false, true
	}
	if alias, found := deprecatedActionAliases[a]; found {
		return alias, true, true
	}
	return "", false, false
}

// loggedDeprecatedAliases tracks which deprecated aliases have already
// surfaced a [WARN] line during the program's lifetime. Process-wide so a
// keybindings file with several occurrences of "map i commit_info" produces
// exactly one warning instead of one per line. mirrors the behavior promised
// by the PR introducing the alias.
var loggedDeprecatedAliases sync.Map

// warnOnceDeprecatedAlias logs a deprecation warning the first time alias is
// observed in the running process. Subsequent calls with the same alias are
// no-ops. Called from parse() when resolveAction reports deprecated=true.
func warnOnceDeprecatedAlias(alias, canonical Action) {
	if _, loaded := loggedDeprecatedAliases.LoadOrStore(string(alias), struct{}{}); loaded {
		return
	}
	log.Printf("[WARN] keybindings: action %q is deprecated, use %q", alias, canonical)
}
