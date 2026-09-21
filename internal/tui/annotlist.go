package tui

import (
	"cmp"
	"maps"
	"slices"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/yousysadmin/igit/internal/annot"
	"github.com/yousysadmin/igit/internal/git"
	"github.com/yousysadmin/igit/internal/tui/overlay"
)

// annotListItem is one row of the annotation list: the session's own
// annotation, or a comment the request already carries. Both are jump targets,
// only the first is the session's to edit. A request comment numbers its line
// on one side of the diff, which the jump needs to find the row.
type annotListItem struct {
	annot.Annotation
	remote bool
	side   string // LEFT or RIGHT for a request comment, empty for our own
}

// buildAnnotListItems builds a flat list of everything attached to the review:
// the session's annotations and the comments the request already carries.
// Items are ordered by file name then line number, with file-level entries
// (Line=0) first within each file and our own annotation ahead of a request
// comment on the same line. This combined ordering is load-bearing: both the @
// popup and the }/{ walker in annotnav.go iterate this exact sequence, so
// changes here must keep the two consumers in sync.
func (m *Model) buildAnnotListItems() []annotListItem {
	items := make([]annotListItem, 0, m.store.Count())
	for _, f := range slices.Sorted(maps.Keys(m.annotatedFiles())) {
		start := len(items)
		for _, a := range m.store.Get(f) {
			items = append(items, annotListItem{Annotation: a})
		}
		for _, n := range m.remoteNotes[f] {
			items = append(items, annotListItem{
				Annotation: annot.Annotation{File: f, Line: n.Line, Type: remoteChangeType(n.Side), Comment: n.text()},
				remote:     true,
				side:       cmp.Or(n.Side, "RIGHT"),
			})
		}
		// stable, so our own annotations keep their place ahead of the
		// request's comments on the same line
		slices.SortStableFunc(items[start:], func(a, b annotListItem) int {
			return cmp.Compare(a.Line, b.Line)
		})
	}
	return items
}

// remoteChangeType is the diff side a request comment reads as: removed lines
// are numbered on the old file, everything else on the new one.
func remoteChangeType(side string) string {
	if side == "LEFT" {
		return "-"
	}
	return "+"
}

// buildAnnotListSpec builds an overlay.AnnotListSpec from the annotation list.
func (m Model) buildAnnotListSpec() overlay.AnnotListSpec {
	annots := m.buildAnnotListItems()
	items := make([]overlay.AnnotationItem, len(annots))
	for i, a := range annots {
		items[i] = overlay.AnnotationItem{
			AnnotationTarget: overlay.AnnotationTarget{File: a.File, ChangeType: a.Type, Line: a.Line, Side: a.side},
			Comment:          a.Comment,
		}
	}
	return overlay.AnnotListSpec{Items: items}
}

// jumpToAnnotationTarget jumps to an annotation target returned by the overlay
// manager. Existing entry point used by the @ popup and the mouse handler.
// preserves the original silent-fail-on-unreachable behavior.
func (m Model) jumpToAnnotationTarget(target *overlay.AnnotationTarget) (tea.Model, tea.Cmd) {
	model, cmd, _ := m.tryJumpToAnnotationTarget(target)
	return model, cmd
}

// tryJumpToAnnotationTarget attempts to jump and reports whether the jump
// was actually issued. ok=false is returned when the target cannot be reached:
// cross-file path is not present in the file tree (filtered, hidden by
// --include/--exclude, or untracked-with-toggle-off), or same-file
// non-file-level line is not in the loaded diff (e.g. compact mode shrank
// context away). Used by the }/{ navigator to skip non-jumpable targets and
// keep walking instead of getting trapped on a target that silently no-ops.
// Single-file mode always rejects cross-file targets - there is nowhere to go.
func (m Model) tryJumpToAnnotationTarget(target *overlay.AnnotationTarget) (tea.Model, tea.Cmd, bool) {
	if target == nil {
		return m, nil, false
	}
	j := annotJump{
		Annotation: annot.Annotation{File: target.File, Line: target.Line, Type: target.ChangeType},
		side:       target.Side,
	}

	if j.File == m.file.name {
		if j.Line != 0 && m.resolveJumpIndex(j) < 0 {
			return m, nil, false
		}
		m.positionOnAnnotation(j)
		return m, nil, true
	}

	if m.file.singleFile {
		return m, nil, false
	}
	if !m.tree.SelectByPath(j.File) {
		return m, nil, false
	}
	m.pendingAnnotJump = &j
	model, cmd := m.loadSelectedIfChanged()
	return model, cmd, true
}

// annotJump is a resolved jump target: an annotation position plus, for a
// request comment, the side its line is numbered on.
type annotJump struct {
	annot.Annotation
	side string // LEFT or RIGHT for a request comment, empty for our own
}

// resolveJumpIndex is the diff row a jump target points at. Our own
// annotations name their change type exactly. A request comment names only a
// side, and the line it numbers is either a changed row or a context row, so
// both are tried in that order, the way the notes themselves are anchored.
func (m Model) resolveJumpIndex(j annotJump) int {
	if j.side == "" {
		return m.findDiffLineIndex(j.Line, j.Type)
	}
	primary, num := git.ChangeAdd, func(dl git.DiffLine) int { return dl.NewNum }
	if j.side == "LEFT" {
		primary, num = git.ChangeRemove, func(dl git.DiffLine) int { return dl.OldNum }
	}
	for _, want := range []git.ChangeType{primary, git.ChangeContext} {
		for i, dl := range m.file.lines {
			if dl.ChangeType == want && num(dl) == j.Line {
				return i
			}
		}
	}
	return -1
}

// positionOnAnnotation moves the cursor to the given annotation's line, re-renders, and centers the viewport.
// In collapsed mode, expands the hunk containing the target line so removed lines are visible.
// For line-level annotations the cursor lands on the annotation comment sub-row (nav.onAnnotationRow),
// matching what `j`/`k` navigation produces when stepping onto an annotated line. File-level annotations
// (Line=0) use diffCursor=-1 which already represents the annotation row directly. Without this flag the
// cursor would land on the diff line above the comment, leaving navigation visually one row off the target.
func (m *Model) positionOnAnnotation(a annotJump) {
	m.nav.onAnnotationRow = false
	if a.Line == 0 {
		m.nav.diffCursor = -1
	} else {
		idx := m.resolveJumpIndex(a)
		if idx >= 0 {
			m.nav.diffCursor = idx
			m.ensureHunkExpanded(idx)
			hunks := m.findHunks()
			if !m.isCollapsedHidden(idx, hunks) && !m.isDeleteOnlyPlaceholder(idx, hunks) {
				m.nav.onAnnotationRow = true
			}
		}
	}
	m.layout.focus = paneDiff
	m.layout.viewport.SetContent(m.renderDiff())
	m.centerViewportOnCursor()
}

// ensureHunkExpanded expands the hunk containing diffLines[idx] when collapsed mode is active.
// this ensures the target line is visible after a jump (e.g., annotation on a removed line).
// also expands delete-only placeholder hunks where the first line is "visible" as a synthetic
// placeholder but annotations are not rendered (renderCollapsedDiff skips them).
func (m *Model) ensureHunkExpanded(idx int) {
	if !m.modes.collapsed.enabled {
		return
	}
	hunks := m.findHunks()
	if m.isCollapsedHidden(idx, hunks) || m.isDeleteOnlyPlaceholder(idx, hunks) {
		hunkStart := m.hunkStartFor(idx, hunks)
		if hunkStart >= 0 {
			m.modes.collapsed.expandedHunks[hunkStart] = true
		}
	}
}

// findDiffLineIndex finds the index into diffLines matching the given line number and change type.
// uses diffLineNum() semantics: compares against OldNum for removes, NewNum for adds/context.
// returns -1 if not found.
func (m Model) findDiffLineIndex(line int, changeType string) int {
	for i, dl := range m.file.lines {
		if string(dl.ChangeType) != changeType {
			continue
		}
		if m.diffLineNum(dl) == line {
			return i
		}
	}
	return -1
}
