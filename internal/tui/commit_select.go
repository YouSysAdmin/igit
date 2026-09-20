package tui

import (
	"context"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/yousysadmin/igit/internal/git"
	"github.com/yousysadmin/igit/internal/gitops"
	"github.com/yousysadmin/igit/internal/keymap"
	"github.com/yousysadmin/igit/internal/tui/overlay"
)

// selectMode is how the diff-pane cursor selects lines for a line operation.
type selectMode int

const (
	selectLine  selectMode = iota // the cursor line only
	selectRange                   // anchor..cursor (sticky until cleared)
	selectHunk                    // the contiguous change block under the cursor
)

// selectedRows returns the inclusive display-row range the next line
// operation acts on.
func (c *CommitModel) selectedRows() (first, last int) {
	cur := c.diff.paneCursor()
	switch c.staging.mode {
	case selectRange:
		return min(c.staging.anchor, cur), max(c.staging.anchor, cur)
	case selectHunk:
		if f, l, ok := c.changeBlock(cur); ok {
			return f, l
		}
	case selectLine:
	}
	return cur, cur
}

// changeBlock returns the contiguous run of added/removed rows containing idx.
func (c *CommitModel) changeBlock(idx int) (first, last int, ok bool) {
	lines := c.diff.paneLines()
	if idx < 0 || idx >= len(lines) || !isChangeRow(lines[idx]) {
		return 0, 0, false
	}
	first, last = idx, idx
	for first > 0 && isChangeRow(lines[first-1]) {
		first--
	}
	for last+1 < len(lines) && isChangeRow(lines[last+1]) {
		last++
	}
	return first, last, true
}

func isChangeRow(dl git.DiffLine) bool {
	return dl.ChangeType == git.ChangeAdd || dl.ChangeType == git.ChangeRemove
}

// isRowSelected is the review model's selection hook: true for rows in the
// active range or hunk selection (the cursor row itself is drawn as cursor).
func (c *CommitModel) isRowSelected(idx int) bool {
	if c.focus != focusDiff || c.staging.mode == selectLine {
		return false
	}
	first, last := c.selectedRows()
	return idx >= first && idx <= last
}

// selectedPatchIndices maps the selected added/removed display rows to patch
// line indices. Context and divider rows are skipped so a selection that holds
// no change yields nothing instead of a no-op git apply.
func (c *CommitModel) selectedPatchIndices() []int {
	first, last := c.selectedRows()
	var out []int
	lines := c.diff.paneLines()
	for i := max(first, 0); i <= last && i < len(c.staging.viewToPatch) && i < len(lines); i++ {
		if isChangeRow(lines[i]) && c.staging.viewToPatch[i] >= 0 {
			out = append(out, c.staging.viewToPatch[i])
		}
	}
	return out
}

// handleSelectionKey covers the diff-pane line operations. ok is false when
// the action is not one of them.
func (c *CommitModel) handleSelectionKey(action keymap.Action) (tea.Cmd, bool) {
	switch action {
	case keymap.ActionVisualRange:
		c.toggleSelectMode(selectRange)
	case keymap.ActionHunkMode:
		c.toggleSelectMode(selectHunk)
	case keymap.ActionSelectExtendDown, keymap.ActionSelectExtendUp:
		if c.staging.mode != selectRange {
			c.staging.mode = selectRange
			c.staging.anchor = c.diff.paneCursor()
		}
		if action == keymap.ActionSelectExtendDown {
			c.diff.moveDiffCursorDown()
		} else {
			c.diff.moveDiffCursorUp()
		}
		c.diff.syncViewportToCursor()
	case keymap.ActionStageToggle:
		if c.conflictedPath(c.staging.spec.Path) {
			// a conflict is settled block by block, staging lines of it is
			// something git refuses anyway
			c.openRegionMenu()
			return nil, true
		}
		return c.applySelectedLines(), true
	case keymap.ActionStageHunk:
		// like s in review mode: the change block under the cursor, unless a
		// range or block is already selected
		if c.staging.mode == selectLine {
			c.staging.mode = selectHunk
		}
		return c.applySelectedLines(), true
	case keymap.ActionDiscardChanges:
		c.confirmDiscardLines()
	default:
		return nil, false
	}
	return nil, true
}

// toggleSelectMode enters mode, or returns to single-line selection when it is
// already active.
func (c *CommitModel) toggleSelectMode(mode selectMode) {
	if c.staging.mode == mode {
		c.staging.mode = selectLine
	} else {
		c.staging.mode = mode
		c.staging.anchor = c.diff.paneCursor()
	}
	c.diff.syncViewportToCursor()
}

// clearSelection returns to single-line selection and repaints.
func (c *CommitModel) clearSelection() {
	if c.staging.mode == selectLine {
		return
	}
	c.staging.mode = selectLine
	c.diff.syncViewportToCursor()
}

// lineOpAvailable checks that the diff pane holds a patch that line operations
// can work on.
func (c *CommitModel) lineOpAvailable() bool {
	switch {
	case c.tab != tabFiles:
		c.hint = "line staging works on the Files tab"
	case c.staging.spec.Path == "" || c.diff.paneRowCount() == 0:
		c.hint = "no diff loaded"
	case c.staging.binary:
		c.hint = "binary file: stage or unstage the whole file"
	case c.conflictedPath(c.staging.spec.Path):
		// git apply refuses an unmerged path, resolving is a whole-file decision
		c.hint = "conflict: resolve the whole file with space on the Files list"
	case c.staging.hunks == 0:
		c.hint = "nothing to stage here"
	default:
		return true
	}
	return false
}

// conflictedPath reports whether the status still lists path as unmerged.
func (c *CommitModel) conflictedPath(path string) bool {
	for _, f := range c.status.Files {
		if f.Path == path {
			return f.Conflict
		}
	}
	return false
}

// applySelectedLines stages the selection (unstaged view) or unstages it
// (staged view) and remembers the cursor row for after the reload.
func (c *CommitModel) applySelectedLines() tea.Cmd {
	if !c.lineOpAvailable() {
		return nil
	}
	indices := c.selectedPatchIndices()
	if len(indices) == 0 {
		c.hint = "select changed lines first"
		return nil
	}
	op := gitops.StageLines
	name := "stage lines"
	if c.staging.spec.Cached {
		op = gitops.UnstageLines
		name = "unstage lines"
	}
	req := gitops.LineRequest{Raw: c.staging.raw, Path: c.staging.spec.Path, Indices: indices, Op: op}
	c.staging.restoreRow = c.diff.paneCursor()
	c.staging.mode = selectLine
	return c.runOp(name, func(ctx context.Context) error { return c.repo.ApplyLines(ctx, req) })
}

// confirmDiscardLines asks before reverting the selected worktree lines.
func (c *CommitModel) confirmDiscardLines() {
	if !c.lineOpAvailable() {
		return
	}
	if c.staging.spec.Cached {
		c.hint = "discard works on the unstaged diff, press " + legendKey(c.keymap, keymap.ActionToggleStagedView) + " to switch"
		return
	}
	indices := c.selectedPatchIndices()
	if len(indices) == 0 {
		c.hint = "select changed lines first"
		return
	}
	first, last := c.selectedRows()
	c.pending = pendingAction{id: "discard-lines", indices: indices}
	body := fmt.Sprintf("Discard the selected change in %s? This rewrites the working tree file.", c.staging.spec.Path)
	if last > first {
		body = fmt.Sprintf("Discard %d selected lines in %s? This rewrites the working tree file.", last-first+1, c.staging.spec.Path)
	}
	c.overlay.OpenConfirm(overlay.ConfirmSpec{ID: "discard-lines", Title: "discard lines", Body: body, Danger: true})
}

// discardSelectedLines runs the confirmed line discard.
func (c *CommitModel) discardSelectedLines(indices []int) tea.Cmd {
	req := gitops.LineRequest{Raw: c.staging.raw, Path: c.staging.spec.Path, Indices: indices, Op: gitops.DiscardLines}
	c.staging.restoreRow = c.diff.paneCursor()
	c.staging.mode = selectLine
	return c.runOp("discard lines", func(ctx context.Context) error { return c.repo.ApplyLines(ctx, req) })
}

// restoreCursorRow puts the cursor back near where a line operation happened
// once the diff has reloaded. -1 means nothing to restore.
func (c *CommitModel) restoreCursorRow() {
	row := c.staging.restoreRow
	c.staging.restoreRow = -1
	if row < 0 || c.diff.paneRowCount() == 0 {
		return
	}
	c.diff.setPaneCursor(row)
}
