package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/yousysadmin/igit/internal/git"
	"github.com/yousysadmin/igit/internal/gitops"
	"github.com/yousysadmin/igit/internal/keymap"
	"github.com/yousysadmin/igit/internal/tui/overlay"
	"github.com/yousysadmin/igit/internal/tui/sidepane"
)

// commitTab is the side pane's content.
type commitTab int

const (
	tabFiles commitTab = iota
	tabBranches
	tabLog
	tabStash
	tabCount
)

func (t commitTab) String() string {
	switch t {
	case tabFiles:
		return "Files"
	case tabBranches:
		return "Branches"
	case tabLog:
		return "Log"
	case tabStash:
		return "Stash"
	case tabCount:
	}
	return "?"
}

// handleTabAction switches tabs for the tab_*, next_tab and prev_tab actions.
func (c *CommitModel) handleTabAction(action keymap.Action) tea.Cmd {
	switch action { //nolint:exhaustive // only the tab actions reach here
	case keymap.ActionNextTab:
		return c.switchTab((c.tab + 1) % tabCount)
	case keymap.ActionPrevTab:
		return c.switchTab((c.tab + tabCount - 1) % tabCount)
	}
	return c.switchTab(tabForAction(action))
}

// tabForAction maps the tab_* keymap actions to a tab.
func tabForAction(a keymap.Action) commitTab {
	switch a { //nolint:exhaustive // only the tab actions reach here
	case keymap.ActionTabBranches:
		return tabBranches
	case keymap.ActionTabLog:
		return tabLog
	case keymap.ActionTabStash:
		return tabStash
	}
	return tabFiles
}

// historyState holds the branches, log and stash tabs and the drilled-in
// file list of a commit or stash entry.
type historyState struct {
	branches *sidepane.List
	commits  *sidepane.List
	stashes  *sidepane.List
	files    *sidepane.List // detail view: files of the selected commit/stash

	branchData []gitops.Branch
	commitData []gitops.Commit
	stashData  []gitops.StashEntry
	loaded     [tabCount]bool
	seq        [tabCount]uint64

	detail     *detailView // non-nil while the side pane shows an entry's files
	detailSeq  uint64
	diffSeq    uint64 // ref-diff loads
	pendingRef string // ref whose files the detail list currently holds
	note       string // commit or stash message shown under the diff header
}

// detailView is a commit or stash entry whose files are listed in the side pane.
type detailView struct {
	tab   commitTab
	title string
	ref   string // A..B ref for FileDiffRequest
}

type commitBranchesMsg struct {
	seq      uint64
	branches []gitops.Branch
	err      error
}

type commitLogMsg struct {
	seq     uint64
	commits []gitops.Commit
	err     error
}

type commitStashesMsg struct {
	seq     uint64
	stashes []gitops.StashEntry
	err     error
}

type commitEntryDiffMsg struct {
	seq   uint64
	title string
	raw   string
	err   error
}

type commitDetailFilesMsg struct {
	seq   uint64
	ref   string
	files []git.FileEntry
	err   error
}

type commitRefDiffMsg struct {
	seq   uint64
	ref   string
	path  string
	orig  string
	lines []git.DiffLine
	err   error
}

func newHistoryState() historyState {
	return historyState{branches: sidepane.NewList(), commits: sidepane.NewList(), stashes: sidepane.NewList(), files: sidepane.NewList()}
}

// activeList returns the list the side pane currently shows.
func (c *CommitModel) activeList() *sidepane.List {
	if c.hist.detail != nil {
		return c.hist.files
	}
	switch c.tab {
	case tabBranches:
		return c.hist.branches
	case tabLog:
		return c.hist.commits
	case tabStash:
		return c.hist.stashes
	case tabFiles, tabCount:
	}
	return c.list
}

// switchTab shows another tab, loading its data on first use, and clears any
// drilled-in detail view.
func (c *CommitModel) switchTab(t commitTab) tea.Cmd {
	if t < 0 || t >= tabCount {
		return nil
	}
	c.tab = t
	c.hist.detail = nil
	c.focus = focusSide
	c.clearSelection()
	if t == tabFiles {
		return c.loadDiffForCursor()
	}
	if c.hist.loaded[t] {
		return c.loadDiffForHistoryCursor()
	}
	return c.loadTab(t)
}

// loadTab fetches the data behind a history tab.
func (c *CommitModel) loadTab(t commitTab) tea.Cmd {
	c.hist.seq[t]++
	seq := c.hist.seq[t]
	repo := c.repo
	switch t {
	case tabBranches:
		return func() tea.Msg {
			bs, err := repo.Branches(context.Background())
			return commitBranchesMsg{seq: seq, branches: bs, err: err}
		}
	case tabLog:
		return func() tea.Msg {
			cs, err := repo.Log(context.Background(), "", 300)
			return commitLogMsg{seq: seq, commits: cs, err: err}
		}
	case tabStash:
		return func() tea.Msg {
			ss, err := repo.Stashes(context.Background())
			return commitStashesMsg{seq: seq, stashes: ss, err: err}
		}
	case tabFiles, tabCount:
	}
	return nil
}

// staleHistoryTabs drops the cached Log and Branches data when HEAD has moved
// since the last status. Committing, amending, checking out, finishing a rebase
// and anything done outside igit all show up here, and refreshTab only reloads
// the tab that happens to be open, so without this the log stays as it was.
func (c *CommitModel) staleHistoryTabs(next gitops.BranchHead) {
	if !c.statusLoaded {
		return // the first status has nothing to compare against
	}
	prev := c.status.Head
	if prev.OID == next.OID && prev.Name == next.Name && prev.Detached == next.Detached {
		return
	}
	c.hist.loaded[tabLog] = false
	c.hist.loaded[tabBranches] = false
}

// refreshTab reloads the active history tab (after a write) keeping the cursor.
func (c *CommitModel) refreshTab() tea.Cmd {
	if c.tab == tabFiles {
		return nil
	}
	return c.loadTab(c.tab)
}

func (c *CommitModel) handleBranchesLoaded(msg commitBranchesMsg) tea.Cmd {
	if msg.seq != c.hist.seq[tabBranches] {
		return nil
	}
	if msg.err != nil {
		c.showError("branches failed", msg.err)
		return nil
	}
	c.hist.branchData = msg.branches
	c.hist.loaded[tabBranches] = true
	rows := make([]sidepane.ListRow, 0, len(msg.branches)+2)
	var local, remote []gitops.Branch
	for _, b := range msg.branches {
		if b.Remote {
			remote = append(remote, b)
		} else {
			local = append(local, b)
		}
	}
	add := func(title, prefix string, bs []gitops.Branch) {
		if len(bs) == 0 {
			return
		}
		rows = append(rows, sidepane.ListRow{Text: fmt.Sprintf("%s (%d)", title, len(bs)), Section: true})
		for _, b := range bs {
			mark := "  "
			if b.Head {
				mark = "* "
			}
			text := b.Name
			if b.Ahead > 0 || b.Behind > 0 {
				text += fmt.Sprintf(" ↑%d ↓%d", b.Ahead, b.Behind)
			}
			if b.UpstreamGone {
				text += " (gone)"
			}
			rows = append(rows, sidepane.ListRow{Key: prefix + b.Name, Text: text, Prefix: mark, PlainPrefix: mark, TailCut: true, Accent: b.Head, Meta: b})
		}
	}
	add("Local", "l:", local)
	add("Remote", "r:", remote)
	c.hist.branches.SetRows(rows)
	if c.tab == tabBranches && c.hist.detail == nil {
		c.clearDiff()
	}
	return nil
}

func (c *CommitModel) handleLogLoaded(msg commitLogMsg) tea.Cmd {
	if msg.seq != c.hist.seq[tabLog] {
		return nil
	}
	if msg.err != nil {
		c.showError("log failed", msg.err)
		return nil
	}
	c.hist.commitData = msg.commits
	c.hist.loaded[tabLog] = true
	rows := make([]sidepane.ListRow, 0, len(msg.commits))
	for _, cm := range msg.commits {
		text := cm.Subject
		if len(cm.Refs) > 0 {
			text = "(" + strings.Join(cm.Refs, ", ") + ") " + text
		}
		short := cm.ShortHash
		if len(short) > 7 {
			short = short[:7]
		}
		rows = append(rows, sidepane.ListRow{Key: cm.Hash, Text: text, Prefix: short + " ", PlainPrefix: short + " ", TailCut: true, Meta: cm})
	}
	c.hist.commits.SetRows(rows)
	if c.tab == tabLog && c.hist.detail == nil {
		return c.loadDiffForHistoryCursor()
	}
	return nil
}

func (c *CommitModel) handleStashesLoaded(msg commitStashesMsg) tea.Cmd {
	if msg.seq != c.hist.seq[tabStash] {
		return nil
	}
	if msg.err != nil {
		c.showError("stash list failed", msg.err)
		return nil
	}
	c.hist.stashData = msg.stashes
	c.hist.loaded[tabStash] = true
	rows := make([]sidepane.ListRow, 0, len(msg.stashes))
	for _, s := range msg.stashes {
		prefix := fmt.Sprintf("{%d} ", s.Index)
		rows = append(rows, sidepane.ListRow{Key: s.Hash, Text: s.Message, Prefix: prefix, PlainPrefix: prefix, TailCut: true, Meta: s})
	}
	c.hist.stashes.SetRows(rows)
	if c.tab == tabStash && c.hist.detail == nil {
		return c.loadDiffForHistoryCursor()
	}
	return nil
}

// loadDiffForHistoryCursor shows the diff behind the history cursor: the
// first file of the selected commit or stash, or the selected detail file.
func (c *CommitModel) loadDiffForHistoryCursor() tea.Cmd {
	if c.hist.detail != nil {
		row, ok := c.hist.files.Cursor()
		if !ok {
			c.clearDiff()
			return nil
		}
		fe, _ := row.Meta.(git.FileEntry)
		return c.loadRefDiff(c.hist.detail.ref, fe)
	}
	switch c.tab {
	case tabLog:
		row, ok := c.hist.commits.Cursor()
		if !ok {
			c.clearDiff()
			return nil
		}
		cm, _ := row.Meta.(gitops.Commit)
		c.hist.note = cm.Message()
		return c.loadEntryDiff(cm.DiffRef(), cm.ShortHash+" "+cm.Subject)
	case tabStash:
		row, ok := c.hist.stashes.Cursor()
		if !ok {
			c.clearDiff()
			return nil
		}
		s, _ := row.Meta.(gitops.StashEntry)
		c.hist.note = s.Message
		return c.loadEntryDiff(gitops.StashDiffRef(s.Index), s.Ref()+" "+s.Message)
	case tabBranches, tabFiles, tabCount:
		c.clearDiff()
	}
	return nil
}

// maxEntryDiffLines caps the rows of a whole-entry diff. It is the backstop
// behind the file cap: a handful of generated files can still be enormous.
const maxEntryDiffLines = 20000

// loadEntryDiff reads the complete diff of a commit or stash entry.
func (c *CommitModel) loadEntryDiff(ref, title string) tea.Cmd {
	c.hist.diffSeq++
	seq := c.hist.diffSeq
	repo := c.repo
	return func() tea.Msg {
		raw, err := repo.RangeDiff(context.Background(), ref, 3)
		return commitEntryDiffMsg{seq: seq, title: title, raw: raw, err: err}
	}
}

// handleEntryDiff shows every file of the entry in one diff, each preceded by
// a divider naming it.
func (c *CommitModel) handleEntryDiff(msg commitEntryDiffMsg) tea.Cmd {
	if msg.seq != c.hist.diffSeq {
		return nil
	}
	if msg.err != nil {
		c.hint = "diff failed: " + msg.err.Error()
		return nil
	}
	lines, highlighted := c.entryDiffLines(splitPatchFiles(msg.raw))
	c.staging = stagingState{seq: c.staging.seq, restoreRow: -1} // no line operations on history diffs
	c.diff.setPaneAnnotationsHidden(true)                        // historical line numbers, not worktree ones
	c.installDiffLines(msg.title, "", lines)
	if len(highlighted) == len(lines) {
		c.diff.setPaneHighlighted(highlighted)
		c.diff.invalidateRenderCaches()
		c.diff.syncViewportToCursor()
	}
	c.diff.setHeaderNote(c.hist.note)
	return nil
}

// entryDiffLines turns the file sections of a patch into diff rows, each under
// a divider with its path, and highlights every section by its own path.
func (c *CommitModel) entryDiffLines(files []patchFile) (lines []git.DiffLine, highlighted []string) {
	for i, f := range files {
		// the file cap is checked before the parsing and highlighting below,
		// which is what a refactoring commit touching hundreds of files makes
		// expensive. the line cap behind it catches a few huge files
		if (c.logDiffFiles > 0 && i >= c.logDiffFiles) || len(lines) >= maxEntryDiffLines {
			lines = append(lines, git.DiffLine{ChangeType: git.ChangeDivider, IsFileHeader: true, Content: fmt.Sprintf("%d more files, press enter to browse them", len(files)-i)})
			highlighted = append(highlighted, lines[len(lines)-1].Content)
			break
		}
		if i > 0 {
			// blank row so the files read as separate blocks
			lines = append(lines, git.DiffLine{ChangeType: git.ChangeDivider})
			highlighted = append(highlighted, "")
		}
		lines = append(lines, git.DiffLine{ChangeType: git.ChangeDivider, IsFileHeader: true, Content: f.display})
		highlighted = append(highlighted, f.display)
		seg, _, _, _ := rawToDiffLines(f.raw)
		lines = append(lines, seg...)
		hl := c.diff.highlighter.HighlightLines(f.path, seg)
		for j, dl := range seg {
			if j < len(hl) {
				highlighted = append(highlighted, hl[j])
				continue
			}
			highlighted = append(highlighted, dl.Content)
		}
	}
	return lines, highlighted
}

// loadDetailFiles fetches the file list of a commit (hash) or stash (index).
// the first file's diff is shown, and Enter drills into the list.
func (c *CommitModel) loadDetailFiles(t commitTab, ref, hash string, stashIndex int) tea.Cmd {
	c.hist.detailSeq++
	seq := c.hist.detailSeq
	repo := c.repo
	return func() tea.Msg {
		var files []git.FileEntry
		var err error
		if t == tabStash {
			files, err = repo.StashFiles(context.Background(), stashIndex)
		} else {
			files, err = repo.CommitFiles(context.Background(), hash)
		}
		return commitDetailFilesMsg{seq: seq, ref: ref, files: files, err: err}
	}
}

func (c *CommitModel) handleDetailFiles(msg commitDetailFilesMsg) tea.Cmd {
	if msg.seq != c.hist.detailSeq {
		return nil
	}
	if msg.err != nil {
		c.showError("commit files failed", msg.err)
		return nil
	}
	rows := make([]sidepane.ListRow, 0, len(msg.files))
	for _, f := range msg.files {
		mark := string(f.Status) + " "
		rows = append(rows, sidepane.ListRow{Key: f.Path, Text: f.Path, Prefix: c.diff.renderer.FileStatusMark(f.Status), PlainPrefix: mark, Meta: f})
	}
	c.hist.files.SetRows(rows)
	c.hist.files.Move(sidepane.MotionFirst)
	c.hist.pendingRef = msg.ref
	if len(msg.files) == 0 {
		// an empty commit still has a message worth showing
		c.staging = stagingState{seq: c.staging.seq, restoreRow: -1}
		c.installDiffLines("", "", nil)
		c.diff.setHeaderNote(c.hist.note)
		return nil
	}
	return c.loadRefDiff(msg.ref, msg.files[0])
}

// loadRefDiff reads one file's diff between two refs through the review diff source.
func (c *CommitModel) loadRefDiff(ref string, fe git.FileEntry) tea.Cmd {
	c.hist.diffSeq++
	seq := c.hist.diffSeq
	src := c.diff.diffSource
	return func() tea.Msg {
		lines, err := src.FileDiff(git.FileDiffRequest{Ref: ref, Path: fe.Path, OldPath: fe.OldPath, ContextLines: 3})
		return commitRefDiffMsg{seq: seq, ref: ref, path: fe.Path, orig: fe.OldPath, lines: lines, err: err}
	}
}

func (c *CommitModel) handleRefDiff(msg commitRefDiffMsg) tea.Cmd {
	if msg.seq != c.hist.diffSeq {
		return nil
	}
	if msg.err != nil {
		c.hint = "diff failed: " + msg.err.Error()
		return nil
	}
	c.staging = stagingState{seq: c.staging.seq, restoreRow: -1} // no line operations on history diffs
	c.diff.setPaneAnnotationsHidden(true)                        // historical line numbers, not worktree ones
	c.installDiffLines(msg.path, msg.orig, msg.lines)
	c.diff.setHeaderNote(c.hist.note)
	return nil
}

// enterDetail drills into the files of the selected commit or stash.
func (c *CommitModel) enterDetail() tea.Cmd {
	var title, ref string
	switch c.tab {
	case tabLog:
		row, ok := c.hist.commits.Cursor()
		if !ok {
			return nil
		}
		cm, _ := row.Meta.(gitops.Commit)
		title, ref = cm.ShortHash+" "+cm.Subject, cm.DiffRef()
	case tabStash:
		row, ok := c.hist.stashes.Cursor()
		if !ok {
			return nil
		}
		s, _ := row.Meta.(gitops.StashEntry)
		title, ref = s.Ref()+" "+s.Message, gitops.StashDiffRef(s.Index)
	case tabFiles, tabBranches, tabCount:
		return nil
	}
	c.hist.detail = &detailView{tab: c.tab, title: title, ref: ref}
	if c.hist.pendingRef != ref || c.hist.files.Len() == 0 {
		// the file list belongs to another entry (or never loaded). fetch it
		return c.loadDiffForHistoryCursorEntry()
	}
	return c.loadDiffForHistoryCursor()
}

// loadDiffForHistoryCursorEntry re-fetches the detail file list for the
// current entry (used when entering detail before the list arrived).
func (c *CommitModel) loadDiffForHistoryCursorEntry() tea.Cmd {
	d := c.hist.detail
	if d == nil {
		return nil
	}
	if d.tab == tabStash {
		row, ok := c.hist.stashes.Cursor()
		if !ok {
			return nil
		}
		s, _ := row.Meta.(gitops.StashEntry)
		return c.loadDetailFiles(tabStash, d.ref, "", s.Index)
	}
	row, ok := c.hist.commits.Cursor()
	if !ok {
		return nil
	}
	cm, _ := row.Meta.(gitops.Commit)
	return c.loadDetailFiles(tabLog, d.ref, cm.Hash, 0)
}

// leaveDetail returns from the file list to the entry list.
func (c *CommitModel) leaveDetail() tea.Cmd {
	c.hist.detail = nil
	return c.loadDiffForHistoryCursor()
}

// handleHistoryKey dispatches side-pane actions on the branches, log and
// stash tabs (and inside a detail file list).
func (c *CommitModel) handleHistoryKey(action keymap.Action) tea.Cmd {
	switch action {
	case keymap.ActionDown, keymap.ActionUp, keymap.ActionPageDown, keymap.ActionPageUp,
		keymap.ActionHalfPageDown, keymap.ActionHalfPageUp, keymap.ActionHome, keymap.ActionEnd:
		return c.moveHistoryList(action)
	case keymap.ActionConfirm:
		return c.historyConfirm()
	case keymap.ActionDismiss:
		if c.hist.detail != nil {
			return c.leaveDetail()
		}
	case keymap.ActionScrollRight:
		return c.focusDiffPane()
	case keymap.ActionDiscardChanges:
		c.historyDelete()
	case keymap.ActionCreate:
		return c.historyCreate()
	case keymap.ActionRename:
		c.branchRenamePrompt()
	case keymap.ActionMerge:
		c.branchMergeConfirm()
	case keymap.ActionRebase:
		c.branchRebaseMenu()
	case keymap.ActionReset:
		c.branchResetMenu()
	case keymap.ActionSetUpstream:
		c.branchUpstreamPrompt()
	case "":
	default:
		c.hint = "not available on the " + c.tab.String() + " tab"
	}
	return nil
}

// moveHistoryList moves the active list and refreshes the diff pane when the
// selection changed.
func (c *CommitModel) moveHistoryList(action keymap.Action) tea.Cmd {
	l := c.activeList()
	before, _ := l.Cursor()
	page := max(1, c.sidePaneHeight()-sideHeaderRows)
	switch action {
	case keymap.ActionDown:
		l.Move(sidepane.MotionDown)
	case keymap.ActionUp:
		l.Move(sidepane.MotionUp)
	case keymap.ActionPageDown:
		l.Move(sidepane.MotionPageDown, page)
	case keymap.ActionPageUp:
		l.Move(sidepane.MotionPageUp, page)
	case keymap.ActionHalfPageDown:
		l.Move(sidepane.MotionPageDown, max(1, page/2))
	case keymap.ActionHalfPageUp:
		l.Move(sidepane.MotionPageUp, max(1, page/2))
	case keymap.ActionHome:
		l.Move(sidepane.MotionFirst)
	case keymap.ActionEnd:
		l.Move(sidepane.MotionLast)
	default:
	}
	l.EnsureVisible(page)
	after, ok := l.Cursor()
	if !ok || after.Key == before.Key {
		return nil
	}
	return c.loadDiffForHistoryCursor()
}

// historyConfirm is Enter on a history tab: checkout a branch (with
// confirmation), drill into a commit or stash, or focus the diff of a detail file.
func (c *CommitModel) historyConfirm() tea.Cmd {
	if c.hist.detail != nil {
		return c.focusDiffPane()
	}
	switch c.tab {
	case tabBranches:
		b, ok := c.cursorBranch()
		if !ok || b.Head {
			return nil
		}
		id, body := "checkout", "Check out "+b.Name+"?"
		if b.Remote {
			id, body = "checkout-remote", "Check out "+b.Name+" as a local branch that tracks it?"
		}
		c.pending = pendingAction{id: id, name: b.Name}
		c.overlay.OpenConfirm(overlay.ConfirmSpec{ID: id, Title: "checkout", Body: body})
		return nil
	case tabLog, tabStash:
		return c.enterDetail()
	case tabFiles, tabCount:
	}
	return nil
}

func (c *CommitModel) cursorBranch() (gitops.Branch, bool) {
	row, ok := c.hist.branches.Cursor()
	if !ok {
		return gitops.Branch{}, false
	}
	b, ok := row.Meta.(gitops.Branch)
	return b, ok
}

func (c *CommitModel) cursorStash() (gitops.StashEntry, bool) {
	row, ok := c.hist.stashes.Cursor()
	if !ok {
		return gitops.StashEntry{}, false
	}
	s, ok := row.Meta.(gitops.StashEntry)
	return s, ok
}

// historyDelete is d on a history tab: delete a branch or drop a stash.
func (c *CommitModel) historyDelete() {
	switch c.tab {
	case tabBranches:
		b, ok := c.cursorBranch()
		if !ok {
			return
		}
		if b.Head {
			c.hint = "cannot delete the checked-out branch"
			return
		}
		if b.Remote {
			c.hint = "remote branches are not deleted from here"
			return
		}
		c.pending = pendingAction{id: "branch-delete", name: b.Name}
		c.overlay.OpenMenu(overlay.MenuSpec{Title: "delete " + b.Name, Items: []overlay.MenuItem{
			{Key: 'd', Label: "delete (only if merged)", ID: "branch-delete"},
			{Key: 'D', Label: "force delete", ID: "branch-delete-force"},
		}})
	case tabStash:
		s, ok := c.cursorStash()
		if !ok {
			return
		}
		c.pending = pendingAction{id: "stash-drop", index: s.Index, name: s.Ref()}
		c.overlay.OpenConfirm(overlay.ConfirmSpec{ID: "stash-drop", Title: "drop stash", Body: "Drop " + s.Ref() + " (" + s.Message + ")?", Danger: true})
	case tabFiles, tabLog, tabCount:
	}
}

// historyCreate is n on a history tab: new branch prompt or stash menu.
func (c *CommitModel) historyCreate() tea.Cmd {
	switch c.tab {
	case tabBranches:
		base := ""
		if b, ok := c.cursorBranch(); ok {
			base = b.Name
		}
		c.pending = pendingAction{id: "branch-create", name: base}
		c.overlay.OpenPrompt(overlay.PromptSpec{ID: "branch-create", Title: "new branch from " + orHead(base), Placeholder: "branch name"})
	case tabStash:
		c.openStashMenu()
	case tabFiles, tabLog, tabCount:
	}
	return nil
}

func orHead(s string) string {
	if s == "" {
		return "HEAD"
	}
	return s
}

func (c *CommitModel) branchRenamePrompt() {
	if c.tab != tabBranches {
		return
	}
	b, ok := c.cursorBranch()
	if !ok || b.Remote {
		return
	}
	c.pending = pendingAction{id: "branch-rename", name: b.Name}
	c.overlay.OpenPrompt(overlay.PromptSpec{ID: "branch-rename", Title: "rename " + b.Name, Initial: b.Name})
}

func (c *CommitModel) branchMergeConfirm() {
	if c.tab != tabBranches {
		return
	}
	b, ok := c.cursorBranch()
	if !ok || b.Head {
		return
	}
	c.pending = pendingAction{id: "merge", name: b.Name}
	c.overlay.OpenMenu(overlay.MenuSpec{Title: "merge " + b.Name + " into " + c.status.Head.Name, Items: []overlay.MenuItem{
		{Key: 'm', Label: "merge (fast-forward when possible)", ID: "merge"},
		{Key: 'f', Label: "fast-forward only", ID: "merge-ff"},
		{Key: 'n', Label: "always create a merge commit", ID: "merge-noff"},
	}})
}

// branchRebaseMenu offers to replay the current branch onto the selected one.
// A rebase that hits a conflict lands in the same abort/continue flow a merge
// does, so the menu only picks how a dirty worktree is treated.
func (c *CommitModel) branchRebaseMenu() {
	b, ok := c.branchTarget()
	if !ok {
		return
	}
	c.pending = pendingAction{id: "rebase", name: b.Name}
	c.overlay.OpenMenu(overlay.MenuSpec{Title: "rebase " + c.status.Head.Name + " onto " + b.Name, Items: []overlay.MenuItem{
		{Key: 'r', Label: "rebase", ID: "rebase"},
		{Key: 'a', Label: "rebase, stashing the uncommitted changes first", ID: "rebase-autostash"},
	}})
}

// branchResetMenu offers to move the current branch to the selected one. The
// hard variant throws away uncommitted work, so it is marked as dangerous.
func (c *CommitModel) branchResetMenu() {
	b, ok := c.branchTarget()
	if !ok {
		return
	}
	c.pending = pendingAction{id: "reset", name: b.Name}
	c.overlay.OpenMenu(overlay.MenuSpec{Title: "reset " + c.status.Head.Name + " to " + b.Name, Items: []overlay.MenuItem{
		{Key: 'm', Label: "mixed: keep the changes, unstage them", ID: "reset-mixed"},
		{Key: 's', Label: "soft: keep the changes staged", ID: "reset-soft"},
		{Key: 'h', Label: "hard: discard every uncommitted change", ID: "reset-hard"},
	}})
}

// branchTarget returns the branch under the cursor when it can be the other
// side of a merge, rebase or reset, and explains itself when it cannot.
func (c *CommitModel) branchTarget() (gitops.Branch, bool) {
	b, ok := c.cursorBranch()
	if !ok {
		return gitops.Branch{}, false
	}
	if b.Head {
		c.hint = "that is the current branch"
		return gitops.Branch{}, false
	}
	if c.status.InProgress.Active() {
		c.hint = "finish or abort the " + string(c.status.InProgress.State) + " first"
		return gitops.Branch{}, false
	}
	return b, true
}

func (c *CommitModel) branchUpstreamPrompt() {
	if c.tab != tabBranches {
		return
	}
	b, ok := c.cursorBranch()
	if !ok || b.Remote {
		return
	}
	initial := b.Upstream
	if initial == "" {
		initial = "origin/" + b.Name
	}
	c.pending = pendingAction{id: "upstream", name: b.Name}
	c.overlay.OpenPrompt(overlay.PromptSpec{ID: "upstream", Title: "upstream for " + b.Name, Placeholder: "remote/branch", Initial: initial})
}

// openStashMenu offers the stash push variants. S opens it from any tab.
func (c *CommitModel) openStashMenu() {
	items := []overlay.MenuItem{
		{Key: 'a', Label: "stash all changes", ID: "stash-all"},
		{Key: 'u', Label: "stash all including untracked files", ID: "stash-untracked"},
		{Key: 'k', Label: "stash but keep the index", ID: "stash-keep"},
	}
	if c.repo.StashStagedSupported(context.Background()) {
		items = append(items, overlay.MenuItem{Key: 's', Label: "stash only the staged changes", ID: "stash-staged"})
	}
	if c.tab == tabStash {
		if s, ok := c.cursorStash(); ok {
			items = append(items,
				overlay.MenuItem{Key: 'p', Label: "pop " + s.Ref(), ID: "stash-pop"},
				overlay.MenuItem{Key: 'y', Label: "apply " + s.Ref() + " (keep it)", ID: "stash-apply"},
			)
			c.pending = pendingAction{index: s.Index, name: s.Ref()}
		}
	}
	c.overlay.OpenMenu(overlay.MenuSpec{Title: "stash", Items: items})
}

// handleHistoryMenu acts on branch, stash and merge menu items. ok is false
// when the choice belongs to another menu.
func (c *CommitModel) handleHistoryMenu(choice string) (tea.Cmd, bool) {
	p := c.pending
	switch choice {
	case "branch-delete", "branch-delete-force":
		c.pending = pendingAction{}
		force := choice == "branch-delete-force"
		return c.runHistoryOp("delete "+p.name, func(ctx context.Context) error { return c.repo.DeleteBranch(ctx, p.name, force) }), true
	case "merge", "merge-ff", "merge-noff":
		c.pending = pendingAction{}
		opts := gitops.MergeOpts{FFOnly: choice == "merge-ff", NoFF: choice == "merge-noff"}
		return c.runHistoryOp("merge "+p.name, func(ctx context.Context) error { return c.repo.Merge(ctx, p.name, opts) }), true
	case "rebase", "rebase-autostash":
		c.pending = pendingAction{}
		opts := gitops.RebaseOpts{Autostash: choice == "rebase-autostash"}
		return c.runHistoryOp("rebase onto "+p.name, func(ctx context.Context) error { return c.repo.Rebase(ctx, p.name, opts) }), true
	case "reset-mixed", "reset-soft":
		c.pending = pendingAction{}
		mode := gitops.ResetMixed
		if choice == "reset-soft" {
			mode = gitops.ResetSoft
		}
		return c.runHistoryOp("reset to "+p.name, func(ctx context.Context) error { return c.repo.Reset(ctx, p.name, mode) }), true
	case "reset-hard":
		// the only one that destroys uncommitted work, so it asks again
		c.overlay.OpenConfirm(overlay.ConfirmSpec{
			ID: "reset-hard", Title: "reset --hard", Danger: true,
			Body: "reset " + c.status.Head.Name + " to " + p.name + " and discard every uncommitted change?",
		})
		return nil, true
	case "stash-all", "stash-untracked", "stash-keep", "stash-staged":
		c.pending = pendingAction{id: choice}
		c.overlay.OpenPrompt(overlay.PromptSpec{ID: "stash-message", Title: "stash message (optional)", Placeholder: "leave empty for the default", AllowEmpty: true})
		return nil, true
	case "stash-pop":
		c.pending = pendingAction{}
		return c.runHistoryOp("pop "+p.name, func(ctx context.Context) error { return c.repo.StashPop(ctx, p.index) }), true
	case "stash-apply":
		c.pending = pendingAction{}
		return c.runHistoryOp("apply "+p.name, func(ctx context.Context) error { return c.repo.StashApply(ctx, p.index) }), true
	}
	return nil, false
}

// handleHistoryConfirmed acts on confirmed checkout and stash drop.
func (c *CommitModel) handleHistoryConfirmed(id string, p pendingAction) (tea.Cmd, bool) {
	switch id {
	case "checkout":
		return c.runHistoryOp("checkout "+p.name, func(ctx context.Context) error { return c.repo.Checkout(ctx, p.name, false) }), true
	case "checkout-remote":
		return c.runHistoryOp("checkout "+p.name, func(ctx context.Context) error { return c.repo.CheckoutRemote(ctx, p.name, false) }), true
	case "reset-hard":
		return c.runHistoryOp("reset to "+p.name, func(ctx context.Context) error { return c.repo.Reset(ctx, p.name, gitops.ResetHard) }), true
	case "stash-drop":
		return c.runHistoryOp("drop "+p.name, func(ctx context.Context) error { return c.repo.StashDrop(ctx, p.index) }), true
	}
	return nil, false
}

// handleHistoryPrompt acts on submitted branch and stash prompts.
func (c *CommitModel) handleHistoryPrompt(id, value string, p pendingAction) (tea.Cmd, bool) {
	value = strings.TrimSpace(value)
	switch id {
	case "branch-create":
		return c.runHistoryOp("create "+value, func(ctx context.Context) error { return c.repo.CreateBranch(ctx, value, p.name, true) }), true
	case "branch-rename":
		return c.runHistoryOp("rename "+p.name, func(ctx context.Context) error { return c.repo.RenameBranch(ctx, p.name, value) }), true
	case "upstream":
		remote, branch, ok := strings.Cut(value, "/")
		if !ok || remote == "" || branch == "" {
			c.hint = "upstream must be remote/branch"
			return nil, true
		}
		return c.runHistoryOp("set upstream", func(ctx context.Context) error { return c.repo.SetUpstream(ctx, p.name, remote, branch) }), true
	case "stash-message":
		opts := gitops.StashPushOpts{Message: value}
		switch p.id {
		case "stash-untracked":
			opts.IncludeUntracked = true
		case "stash-keep":
			opts.KeepIndex = true
		case "stash-staged":
			opts.Staged = true
		}
		return c.runHistoryOp("stash", func(ctx context.Context) error { return c.repo.StashPush(ctx, opts) }), true
	}
	return nil, false
}

// runHistoryOp runs a write and refreshes the status plus the active tab.
func (c *CommitModel) runHistoryOp(name string, fn func(ctx context.Context) error) tea.Cmd {
	return c.runOp(name, fn)
}
