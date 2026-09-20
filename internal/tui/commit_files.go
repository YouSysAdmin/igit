package tui

import (
	"context"
	"fmt"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/yousysadmin/igit/internal/gitops"
	"github.com/yousysadmin/igit/internal/keymap"
	"github.com/yousysadmin/igit/internal/tui/overlay"
	"github.com/yousysadmin/igit/internal/tui/sidepane"
	"github.com/yousysadmin/igit/internal/tui/style"
)

// fileRow is the side-pane row payload for one file in one section.
type fileRow struct {
	file   gitops.StatusEntry
	staged bool // row lives in the Staged section
}

// rebuildRows regenerates the files list from the current status, keeping the
// cursor on the same section/path when it still exists.
func (c *CommitModel) rebuildRows() {
	var rows []sidepane.ListRow
	add := func(title, prefix string, files []gitops.StatusEntry, staged bool) {
		if len(files) == 0 {
			return
		}
		rows = append(rows, sidepane.ListRow{Text: fmt.Sprintf("%s (%d)", title, len(files)), Section: true})
		for _, f := range files {
			mark := f.ShortStatus()
			rows = append(rows, sidepane.ListRow{
				Key:         prefix + f.Path,
				Text:        f.Path,
				Prefix:      c.statusMark(f, staged),
				PlainPrefix: mark + " ",
				Meta:        fileRow{file: f, staged: staged},
			})
		}
	}
	add("Conflicts", "c:", c.scoped(c.status.Conflicted()), false)
	add("Staged", "s:", c.scoped(c.status.Staged()), true)
	add("Unstaged", "u:", c.scoped(c.status.Unstaged()), false)
	c.list.SetRows(rows)
}

// scoped drops the files the session's file filter (--include, --exclude,
// --only) rejects. without a filter it returns files unchanged.
func (c *CommitModel) scoped(files []gitops.StatusEntry) []gitops.StatusEntry {
	if c.filter == nil {
		return files
	}
	kept := make([]gitops.StatusEntry, 0, len(files))
	for _, f := range files {
		if c.filter(f.Path) {
			kept = append(kept, f)
		}
	}
	return kept
}

// statusMark renders the two-letter status in a section color: staged in the
// add color, untracked muted, conflicts in the remove color, unstaged in the
// modify color.
func (c *CommitModel) statusMark(f gitops.StatusEntry, staged bool) string {
	var key style.ColorKey
	switch {
	case f.Conflict:
		key = style.ColorKeyRemoveLineFg
	case f.Untracked:
		key = style.ColorKeyMutedFg
	case staged:
		key = style.ColorKeyAddLineFg
	default:
		key = style.ColorKeyModifyLineFg
	}
	fg := c.diff.resolver.Color(key)
	if fg == "" {
		return f.ShortStatus() + " "
	}
	return string(fg) + f.ShortStatus() + string(style.ResetFg) + " "
}

// cursorFile returns the file under the side-pane cursor.
func (c *CommitModel) cursorFile() (fileRow, bool) {
	row, ok := c.list.Cursor()
	if !ok {
		return fileRow{}, false
	}
	fr, ok := row.Meta.(fileRow)
	return fr, ok
}

// specFor returns the diff request for a row: staged rows show the index
// diff, untracked files a diff against nothing, everything else the worktree
// diff.
func specFor(fr fileRow) gitops.DiffSpec {
	spec := gitops.DiffSpec{Path: fr.file.Path}
	switch {
	case fr.staged:
		spec.Cached = true
		spec.OrigPath = fr.file.OrigPath
	case fr.file.Untracked:
		spec.Untracked = true
	}
	return spec
}

// loadDiffForCursor (re)loads the diff pane for the current row. an empty list
// clears the pane.
func (c *CommitModel) loadDiffForCursor() tea.Cmd {
	fr, ok := c.cursorFile()
	if !ok {
		c.clearDiff()
		c.focus = focusSide
		return nil
	}
	spec := specFor(fr)
	if c.staging.spec.Path == spec.Path && c.staging.spec.Cached == spec.Cached && c.staging.spec.Untracked == spec.Untracked {
		// same file and side. keep the user's view mode (tab) but refresh content
		spec = c.staging.spec
	}
	return c.loadDiff(spec)
}

func (c *CommitModel) handleKey(msg tea.KeyMsg) tea.Cmd {
	c.hint = ""
	action := c.keymap.Resolve(msg.String())
	if c.overlay.Active() {
		if c.overlay.Kind() == overlay.KindThemeSelect {
			return c.forwardToDiff(msg) // the theme selector is driven by the embedded review model
		}
		return c.handleOverlayKey(msg, action)
	}
	if c.diff.paneSearching() {
		return c.forwardToDiff(msg) // the search prompt owns every key, as in review mode
	}
	if cmd, ok := c.handleGlobalNav(action); ok {
		return cmd
	}
	switch action {
	case keymap.ActionQuit:
		return tea.Quit
	case keymap.ActionHelp:
		c.overlay.OpenHelp(c.buildHelpSpec())
		return nil
	case keymap.ActionReload:
		c.hint = "refreshing"
		return c.Refresh()
	case keymap.ActionStageAll, keymap.ActionUnstageAll:
		return c.stageAll(action == keymap.ActionStageAll)
	case keymap.ActionOpenFileInEditor:
		return c.openInEditor()
	case keymap.ActionToggleStagedView, keymap.ActionTogglePane, keymap.ActionSearch, keymap.ActionToggleTree,
		keymap.ActionThemeSelect, keymap.ActionFocusTree, keymap.ActionFocusDiff,
		keymap.ActionToggleWrap, keymap.ActionToggleLineNums, keymap.ActionToggleWordDiff:
		return c.handlePaneAction(action)
	case keymap.ActionCommit, keymap.ActionCommitEditor, keymap.ActionAmend, keymap.ActionStash,
		keymap.ActionPush, keymap.ActionPull, keymap.ActionFetch, keymap.ActionAbortOrContinue:
		return c.handleRepoAction(action)
	case keymap.ActionTabFiles, keymap.ActionTabBranches, keymap.ActionTabLog, keymap.ActionTabStash,
		keymap.ActionNextTab, keymap.ActionPrevTab:
		return c.handleTabAction(action)
	default: // pane-specific actions below
	}
	if c.focus == focusDiff {
		return c.handleDiffKey(action)
	}
	if c.tab != tabFiles {
		return c.handleHistoryKey(action)
	}
	return c.handleFilesKey(action)
}

// handlePaneAction covers focus, side pane visibility, the diff side, search,
// the theme selector and the display toggles: the view actions shared with
// review mode, all answered whichever pane has the focus.
func (c *CommitModel) handlePaneAction(action keymap.Action) tea.Cmd {
	switch action { //nolint:exhaustive // only the pane actions reach here
	case keymap.ActionToggleStagedView:
		return c.toggleStagedView()
	case keymap.ActionTogglePane:
		return c.togglePane()
	case keymap.ActionSearch:
		return c.startSearch()
	case keymap.ActionToggleTree:
		c.toggleSidePane()
	case keymap.ActionThemeSelect:
		c.diff.openThemeSelector()
	case keymap.ActionFocusTree:
		c.showSidePane()
		c.focus = focusSide
		c.clearSelection()
	case keymap.ActionFocusDiff:
		return c.focusDiffPane()
	case keymap.ActionToggleWrap, keymap.ActionToggleLineNums, keymap.ActionToggleWordDiff:
		c.applyDiffViewToggle(action)
	}
	return nil
}

// applyDiffViewToggle flips a display mode of the diff pane. Review mode honors
// these from either pane, so commit mode does too and the status lamp follows
// wherever the focus is.
func (c *CommitModel) applyDiffViewToggle(action keymap.Action) {
	next, _ := c.diff.handleViewToggle(action)
	if m, ok := next.(Model); ok {
		c.diff = m
	}
}

// handleRepoAction routes the repository-wide actions: commit flows, the
// stash menu and sync.
func (c *CommitModel) handleRepoAction(action keymap.Action) tea.Cmd {
	switch action { //nolint:exhaustive // only the repo actions reach here
	case keymap.ActionCommit:
		return c.prepareCommitPrompt(false)
	case keymap.ActionCommitEditor:
		return c.commitInEditor("")
	case keymap.ActionAmend:
		c.openAmendMenu()
		return nil
	case keymap.ActionStash:
		c.openStashMenu()
		return nil
	}
	return c.handleSyncAction(action)
}

// forwardToDiff hands a key to the embedded review model's own key handler.
// Used while a popup owned by that model (the theme selector) is open.
func (c *CommitModel) forwardToDiff(msg tea.KeyMsg) tea.Cmd {
	next, cmd := c.diff.handleKey(msg)
	if m, ok := next.(Model); ok {
		c.diff = m
	}
	return cmd
}

// stageAll stages (or unstages) every visible file: git add -A / git reset
// without a file filter, the visible paths when --include/--exclude/--only
// narrow the Files tab.
func (c *CommitModel) stageAll(stage bool) tea.Cmd {
	if c.tab != tabFiles {
		c.hint = "staging works on the Files tab"
		return nil
	}
	if !stage && c.status.InProgress.Active() {
		// `git reset` drops every merge stage and the MERGE_HEAD with them,
		// leaving a tree that can be neither resolved nor aborted
		c.hint = "unstage all would destroy the " + string(c.status.InProgress.State) + " state, undo one file at a time or abort with " + legendKey(c.keymap, keymap.ActionAbortOrContinue)
		return nil
	}
	unborn := c.status.Head.Unborn
	if c.filter == nil {
		if stage {
			return c.runOp("stage all", func(ctx context.Context) error { return c.repo.StageAll(ctx) })
		}
		return c.runOp("unstage all", func(ctx context.Context) error { return c.repo.UnstageAll(ctx, unborn) })
	}
	var paths []string
	if stage {
		for _, f := range c.scoped(append(c.status.Conflicted(), c.status.Unstaged()...)) {
			paths = append(paths, f.Path)
		}
	} else {
		for _, f := range c.scoped(c.status.Staged()) {
			paths = append(paths, f.Path)
		}
	}
	if len(paths) == 0 {
		c.hint = "nothing to stage in the current file scope"
		if !stage {
			c.hint = "nothing to unstage in the current file scope"
		}
		return nil
	}
	if stage {
		return c.runOp("stage all", func(ctx context.Context) error { return c.repo.StageFiles(ctx, paths) })
	}
	return c.runOp("unstage all", func(ctx context.Context) error { return c.repo.UnstageFiles(ctx, paths, unborn) })
}

// handleGlobalNav covers the navigation that works whatever pane has focus,
// as in review mode: hunk jumps and diff scrolling act on the diff pane,
// next/prev item moves the side pane list. ok is false for other actions.
func (c *CommitModel) handleGlobalNav(action keymap.Action) (tea.Cmd, bool) {
	switch action {
	case keymap.ActionNextHunk, keymap.ActionPrevHunk:
		return c.hunkNav(action == keymap.ActionNextHunk), true
	case keymap.ActionScrollDiffDown:
		c.diff.scrollDiffViewportLine(wheelStep)
		return nil, true
	case keymap.ActionScrollDiffUp:
		c.diff.scrollDiffViewportLine(-wheelStep)
		return nil, true
	case keymap.ActionNextItem, keymap.ActionPrevItem:
		if len(c.diff.paneSearchMatches()) > 0 {
			// with an active search n/N walk the matches, as in review mode
			next, _ := c.diff.handleFileOrSearchNav(action == keymap.ActionNextItem)
			if m, ok := next.(Model); ok {
				c.diff = m
			}
			return nil, true
		}
		move := keymap.ActionDown
		if action == keymap.ActionPrevItem {
			move = keymap.ActionUp
		}
		if c.tab != tabFiles || c.hist.detail != nil {
			return c.moveHistoryList(move), true
		}
		return c.moveList(move), true
	default:
		return nil, false
	}
}

// hunkNav jumps to the next or previous hunk of the diff pane. With
// --cross-file-hunks a jump that cannot move (last hunk, or an empty diff)
// steps the side list to the adjacent file and lands on its first (or last)
// hunk once the diff arrives, as review mode does across the file tree. Entry
// lists (commits, branches, stashes) are never stepped this way.
func (c *CommitModel) hunkNav(forward bool) tea.Cmd {
	before := c.diff.paneCursor()
	if c.diff.paneRowCount() > 0 {
		next, _ := c.diff.handleHunkNav(forward)
		if m, ok := next.(Model); ok {
			c.diff = m
		}
		if c.diff.paneCursor() != before {
			return nil
		}
	}
	if !c.crossFileHunks || (c.tab != tabFiles && c.hist.detail == nil) {
		return nil
	}
	move := keymap.ActionDown
	if !forward {
		move = keymap.ActionUp
	}
	jump := forward
	c.diff.setPaneHunkJump(&jump)
	var cmd tea.Cmd
	if c.hist.detail != nil {
		cmd = c.moveHistoryList(move)
	} else {
		cmd = c.moveList(move)
	}
	if cmd == nil { // already on the last (first) file
		c.diff.setPaneHunkJump(nil)
	}
	return cmd
}

// handleFilesKey dispatches actions while the side pane is focused.
func (c *CommitModel) handleFilesKey(action keymap.Action) tea.Cmd {
	switch action {
	case keymap.ActionDown, keymap.ActionUp, keymap.ActionPageDown, keymap.ActionPageUp,
		keymap.ActionHalfPageDown, keymap.ActionHalfPageUp, keymap.ActionHome, keymap.ActionEnd:
		return c.moveList(action)
	case keymap.ActionConfirm, keymap.ActionScrollRight:
		return c.focusDiffPane()
	case keymap.ActionStageToggle, keymap.ActionStageHunk:
		return c.toggleStageFile()
	case keymap.ActionDiscardChanges:
		c.openDiscardMenu()
		return nil
	case keymap.ActionDismiss, "":
		return nil
	default:
		c.hint = unavailableHere(action)
		return nil
	}
}

// unavailableHere explains a key that does nothing where it was pressed. An
// action the branches tab owns says so instead of claiming it does not exist.
func unavailableHere(action keymap.Action) string {
	if branchTabActions[action] {
		return string(action) + " works on the branches tab"
	}
	return "commit mode: " + string(action) + " is not implemented yet"
}

// branchTabActions are the actions only the branches tab serves.
var branchTabActions = map[keymap.Action]bool{
	keymap.ActionRename:      true,
	keymap.ActionMerge:       true,
	keymap.ActionRebase:      true,
	keymap.ActionReset:       true,
	keymap.ActionSetUpstream: true,
}

// handleDiffKey dispatches actions while the diff pane is focused. Movement
// reuses the review model's cursor logic.
func (c *CommitModel) handleDiffKey(action keymap.Action) tea.Cmd {
	if cmd, ok := c.handleSelectionKey(action); ok {
		return cmd
	}
	if c.diff.handleDiffMovement(action) {
		return nil
	}
	switch action {
	case keymap.ActionScrollLeft:
		c.diff.handleHorizontalScroll(-1)
	case keymap.ActionScrollRight:
		c.diff.handleHorizontalScroll(1)
	case keymap.ActionDismiss:
		if c.staging.mode != selectLine {
			c.clearSelection()
			return nil
		}
		c.showSidePane()
		c.focus = focusSide
		c.diff.syncViewportToCursor()
	case "":
	default:
		c.hint = unavailableHere(action)
	}
	return nil
}

// moveList moves the side-pane cursor and loads the newly selected diff.
func (c *CommitModel) moveList(action keymap.Action) tea.Cmd {
	before, _ := c.list.Cursor()
	page := max(1, c.sidePaneHeight()-sideHeaderRows)
	switch action {
	case keymap.ActionDown:
		c.list.Move(sidepane.MotionDown)
	case keymap.ActionUp:
		c.list.Move(sidepane.MotionUp)
	case keymap.ActionPageDown:
		c.list.Move(sidepane.MotionPageDown, page)
	case keymap.ActionPageUp:
		c.list.Move(sidepane.MotionPageUp, page)
	case keymap.ActionHalfPageDown:
		c.list.Move(sidepane.MotionPageDown, max(1, page/2))
	case keymap.ActionHalfPageUp:
		c.list.Move(sidepane.MotionPageUp, max(1, page/2))
	case keymap.ActionHome:
		c.list.Move(sidepane.MotionFirst)
	case keymap.ActionEnd:
		c.list.Move(sidepane.MotionLast)
	default: // not a movement action
	}
	c.list.EnsureVisible(page)
	after, ok := c.list.Cursor()
	if !ok || after.Key == before.Key {
		return nil
	}
	return c.loadDiffForCursor()
}

// togglePane switches focus between the side pane and the diff, as tab does in
// review mode. a hidden side pane is shown again on the way back.
func (c *CommitModel) togglePane() tea.Cmd {
	if c.focus == focusDiff {
		c.showSidePane()
		c.focus = focusSide
		c.clearSelection()
		c.diff.syncViewportToCursor()
		return nil
	}
	return c.focusDiffPane()
}

// startSearch opens the review model's search prompt on the diff pane.
func (c *CommitModel) startSearch() tea.Cmd {
	if c.diff.paneRowCount() == 0 {
		c.hint = "no diff to search"
		return nil
	}
	c.focus = focusDiff
	c.clearSelection()
	return c.diff.startSearch()
}

// focusDiffPane moves keyboard focus to the diff pane when it shows something.
func (c *CommitModel) focusDiffPane() tea.Cmd {
	if c.diff.paneRowCount() == 0 {
		c.hint = "no diff to navigate"
		return nil
	}
	c.focus = focusDiff
	return nil
}

// toggleStageFile stages an unstaged/untracked/conflicted row or unstages a
// staged row.
func (c *CommitModel) toggleStageFile() tea.Cmd {
	fr, ok := c.cursorFile()
	if !ok {
		return nil
	}
	if fr.file.Conflict {
		// staging a conflict marks it resolved, which is a decision rather than
		// a toggle: offer the sides instead of picking one silently
		c.openResolveMenu(fr.file)
		return nil
	}
	paths := []string{fr.file.Path}
	if fr.staged {
		if fr.file.OrigPath != "" && fr.file.OrigPath != fr.file.Path {
			paths = append(paths, fr.file.OrigPath)
		}
		unborn := c.status.Head.Unborn
		if c.status.InProgress.Active() {
			// a file staged during a merge may be a resolved conflict, undoing
			// that should bring the conflict back rather than freeze it
			path := fr.file.Path
			return c.runOp("unstage "+path, func(ctx context.Context) error { return c.repo.UnstageDuringMerge(ctx, path, unborn) })
		}
		return c.runOp("unstage "+fr.file.Path, func(ctx context.Context) error { return c.repo.UnstageFiles(ctx, paths, unborn) })
	}
	return c.runOp("stage "+fr.file.Path, func(ctx context.Context) error { return c.repo.StageFiles(ctx, paths) })
}

// toggleStagedView switches the diff pane between the unstaged and staged side
// of the current file when both exist.
func (c *CommitModel) toggleStagedView() tea.Cmd {
	fr, ok := c.cursorFile()
	if !ok || fr.file.Untracked || fr.file.Conflict {
		return nil
	}
	spec := c.staging.spec
	if spec.Path != fr.file.Path {
		spec = specFor(fr)
	}
	switch {
	case spec.Cached && fr.file.HasUnstaged():
		spec.Cached = false
		spec.OrigPath = ""
	case !spec.Cached && fr.file.HasStaged():
		spec.Cached = true
		spec.OrigPath = fr.file.OrigPath
	default:
		c.hint = "file has changes on one side only"
		return nil
	}
	return c.loadDiff(spec)
}

// openDiscardMenu offers the discard scopes that make sense for the row.
func (c *CommitModel) openDiscardMenu() {
	fr, ok := c.cursorFile()
	if !ok {
		return
	}
	var items []overlay.MenuItem
	switch {
	case fr.file.Untracked:
		items = append(items, overlay.MenuItem{Key: 'd', Label: "delete untracked file", ID: "unstaged"})
	case fr.file.Conflict:
		items = append(items, overlay.MenuItem{Key: 'a', Label: "reset file to HEAD (drop both sides)", ID: "all"})
	default:
		if fr.file.HasUnstaged() {
			items = append(items, overlay.MenuItem{Key: 'u', Label: "discard unstaged changes", ID: "unstaged"})
		}
		items = append(items, overlay.MenuItem{Key: 'a', Label: "discard all changes (index and worktree)", ID: "all"})
	}
	c.pending = pendingAction{file: fr.file}
	c.overlay.OpenMenu(overlay.MenuSpec{Title: "discard " + fr.file.Path, Items: items})
}

// openResolveMenu offers the ways to settle one unmerged path.
func (c *CommitModel) openResolveMenu(f gitops.StatusEntry) {
	c.pending = pendingAction{id: "resolve", file: f}
	c.overlay.OpenMenu(overlay.MenuSpec{Title: "resolve " + f.Path, Items: []overlay.MenuItem{
		{Key: 'r', Label: "mark resolved as it stands", ID: "resolve-mark"},
		{Key: 'o', Label: "take our side whole", ID: "resolve-ours"},
		{Key: 't', Label: "take their side whole", ID: "resolve-theirs"},
	}})
}

// handleResolveMenu settles a conflict the way the menu chose.
func (c *CommitModel) handleResolveMenu(choice string) (tea.Cmd, bool) {
	path := c.pending.file.Path
	switch choice {
	case "resolve-mark":
		return c.runOp("resolve "+path, func(ctx context.Context) error { return c.repo.StageFiles(ctx, []string{path}) }), true
	case "resolve-ours":
		return c.runOp("resolve "+path+" as ours", func(ctx context.Context) error { return c.repo.ResolveConflict(ctx, path, true) }), true
	case "resolve-theirs":
		return c.runOp("resolve "+path+" as theirs", func(ctx context.Context) error { return c.repo.ResolveConflict(ctx, path, false) }), true
	}
	return nil, false
}

// handleOverlayKey routes keys into the open overlay and acts on its outcome.
func (c *CommitModel) handleOverlayKey(msg tea.KeyMsg, action keymap.Action) tea.Cmd {
	out := c.overlay.HandleKey(msg, action)
	switch out.Kind {
	case overlay.OutcomeMenuChosen:
		return c.handleMenuChoice(out.MenuChoice)
	case overlay.OutcomeConfirmed:
		return c.handleConfirmed(out.ConfirmID)
	case overlay.OutcomeClosed:
		c.pending = pendingAction{}
	case overlay.OutcomePromptSubmitted:
		return c.handlePromptSubmitted(out.PromptID, out.PromptValue)
	case overlay.OutcomePromptEditor:
		return c.handlePromptEditor(out.PromptID, out.PromptValue)
	case overlay.OutcomeNone, overlay.OutcomeAnnotationChosen, overlay.OutcomeThemePreview,
		overlay.OutcomeThemeConfirmed, overlay.OutcomeThemeCanceled, overlay.OutcomeFileChosen:
	}
	return nil
}

// handleMenuChoice acts on a menu item: amend variants run directly, discard
// scopes go through a confirmation popup, history menus have their own handler.
func (c *CommitModel) handleMenuChoice(choice string) tea.Cmd {
	if cmd, ok := c.handleHistoryMenu(choice); ok {
		return cmd
	}
	if cmd, ok := c.handlePushMenu(choice); ok {
		return cmd
	}
	if cmd, ok := c.handleResolveMenu(choice); ok {
		return cmd
	}
	if cmd, ok := c.handleRegionMenu(choice); ok {
		return cmd
	}
	if cmd, ok := c.handleInProgressMenu(choice); ok {
		return cmd
	}
	switch choice {
	case "amend-keep":
		c.pending = pendingAction{id: "amend"}
		c.overlay.OpenConfirm(overlay.ConfirmSpec{ID: "amend", Title: "amend",
			Body: "Amend HEAD with the staged changes and keep its message?"})
		return nil
	case "amend-edit":
		return c.prepareCommitPrompt(true)
	}
	if c.pending.file.Path == "" {
		return nil
	}
	scope := gitops.DiscardUnstaged
	body := "Discard the unstaged changes to " + c.pending.file.Path + "?"
	if choice == "all" {
		scope = gitops.DiscardAll
		body = "Discard every change to " + c.pending.file.Path + " (index and worktree)?"
	}
	if c.pending.file.Untracked {
		body = "Delete the untracked file " + c.pending.file.Path + "?"
	}
	c.pending.scope = scope
	c.pending.id = "discard"
	c.overlay.OpenConfirm(overlay.ConfirmSpec{ID: c.pending.id, Title: "discard", Body: body, Danger: true})
	return nil
}

// handleConfirmed runs the action the confirmation was armed for.
func (c *CommitModel) handleConfirmed(id string) tea.Cmd {
	p := c.pending
	c.pending = pendingAction{}
	if id == "discard-lines" && len(p.indices) > 0 {
		return c.discardSelectedLines(p.indices)
	}
	if id == "amend" {
		return c.commitWith("", gitops.CommitOpts{Amend: true, NoEdit: true})
	}
	if cmd, ok := c.handleHistoryConfirmed(id, p); ok {
		return cmd
	}
	if id != "discard" || p.file.Path == "" {
		return nil
	}
	unborn := c.status.Head.Unborn
	return c.runOp("discard "+p.file.Path, func(ctx context.Context) error {
		return c.repo.Discard(ctx, p.file, p.scope, unborn)
	})
}

// openInEditor opens the current file in $EDITOR and refreshes on return.
func (c *CommitModel) openInEditor() tea.Cmd {
	fr, ok := c.cursorFile()
	if !ok {
		return nil
	}
	if c.editor == nil {
		c.hint = "no editor configured"
		return nil
	}
	line := 1
	if c.focus == focusDiff {
		if dl, ok := c.diff.cursorDiffLine(); ok && dl.NewNum > 0 {
			line = dl.NewNum
		}
	}
	path := fr.file.Path
	if c.repoRoot != "" {
		path = filepath.Join(c.repoRoot, path)
	}
	cmd, err := c.editor.SourceCommand(path, line)
	if err != nil {
		c.hint = "cannot open editor: " + err.Error()
		return nil
	}
	restore := c.mouseTracking
	return c.run(cmd, func(runErr error) tea.Msg {
		return commitProcessDoneMsg{name: "editor", err: runErr, restoreMouse: restore}
	})
}
