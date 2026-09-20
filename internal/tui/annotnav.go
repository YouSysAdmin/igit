package tui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/yousysadmin/igit/internal/annot"
	"github.com/yousysadmin/igit/internal/git"
	"github.com/yousysadmin/igit/internal/tui/overlay"
)

// handleAnnotNav jumps to the next or previous annotation across files, in
// the order of the @ popup, and does nothing at the boundaries. A target that
// cannot be displayed (filtered out of the tree, or missing from the loaded
// diff in compact mode) is skipped and the walk continues in the same
// direction, examining each annotation at most once.
func (m Model) handleAnnotNav(forward bool) (tea.Model, tea.Cmd) {
	flat := m.buildAnnotListItems()
	if len(flat) == 0 {
		return m, nil
	}
	cur := m.currentAnnotKey()
	step := 1
	if !forward {
		step = -1
	}
	for idx := startingFlatIndex(flat, cur, forward); idx >= 0 && idx < len(flat); idx += step {
		target := flat[idx]
		nextModel, cmd, jumped := m.tryJumpToAnnotationTarget(&overlay.AnnotationTarget{
			File:       target.File,
			ChangeType: target.Type,
			Line:       target.Line,
		})
		if jumped {
			return nextModel, cmd
		}
	}
	return m, nil
}

// cursorAnnotKey is the cursor's position in annotation space. onAnnot is
// true when the cursor's diff position matches an annotation in the store
// (file, line, AND type), regardless of whether the cursor visually sits
// on the diff line or on the annotation comment sub-row - both point to
// the same annotation. In that case navigation steps by index in the flat
// list, otherwise it uses an insertion-point fallback.
type cursorAnnotKey struct {
	file    string
	line    int
	typ     string
	onAnnot bool
}

// currentAnnotKey returns the cursor position in annotation space. The
// file-level row maps to line 0, an out-of-range cursor to line -1 so every
// annotation of the file counts as later, and a divider row inherits the
// position of the nearest line above it (or -1 when there is none).
func (m Model) currentAnnotKey() cursorAnnotKey {
	file := m.file.name
	if m.nav.diffCursor == -1 {
		return cursorAnnotKey{file: file, line: 0, typ: "", onAnnot: m.hasFileAnnotation()}
	}
	if m.nav.diffCursor < 0 || m.nav.diffCursor >= len(m.file.lines) {
		return cursorAnnotKey{file: file, line: -1, typ: "", onAnnot: false}
	}
	dl := m.file.lines[m.nav.diffCursor]
	if dl.ChangeType == git.ChangeDivider {
		return m.dividerAnnotKey(file)
	}
	line := m.diffLineNum(dl)
	typ := string(dl.ChangeType)
	return cursorAnnotKey{file: file, line: line, typ: typ, onAnnot: m.store.Has(file, line, typ)}
}

// dividerAnnotKey returns the cursor's annotation-space key when the cursor
// sits on a ChangeDivider row. Walks back to the nearest preceding
// non-divider line and uses its line number, so forward navigation from a
// middle/trailing divider reaches the next annotation strictly after the
// divider's logical position rather than re-entering at the file-level
// annotation. A leading divider (no prior non-divider line) falls back to
// line=-1 so the file-level annotation stays reachable.
func (m Model) dividerAnnotKey(file string) cursorAnnotKey {
	for i := m.nav.diffCursor - 1; i >= 0; i-- {
		prev := m.file.lines[i]
		if prev.ChangeType == git.ChangeDivider {
			continue
		}
		return cursorAnnotKey{file: file, line: m.diffLineNum(prev), typ: string(prev.ChangeType), onAnnot: false}
	}
	return cursorAnnotKey{file: file, line: -1, typ: "", onAnnot: false}
}

// startingFlatIndex returns the index in flat from which the walker should
// begin attempting jumps. Forward: first index strictly after the cursor,
// or one past an exact-match index. Backward: mirror. Returns an
// out-of-range index (-1 or len(flat)) when the cursor is at the
// corresponding boundary - the loop exits immediately in that case.
func startingFlatIndex(flat []annot.Annotation, cur cursorAnnotKey, forward bool) int {
	if idx, ok := exactAnnotIndex(flat, cur); ok {
		if forward {
			return idx + 1
		}
		return idx - 1
	}
	insIdx := annotInsertionPoint(flat, cur)
	if forward {
		return insIdx
	}
	return insIdx - 1
}

// exactAnnotIndex returns the flat-list index of an annotation that exactly
// matches the cursor (file, line, type). When cur.onAnnot is false it
// short-circuits without scanning. ok=false means "use insertion-point
// fallback instead."
func exactAnnotIndex(flat []annot.Annotation, cur cursorAnnotKey) (int, bool) {
	if !cur.onAnnot {
		return 0, false
	}
	for i, a := range flat {
		if a.File == cur.file && a.Line == cur.line && a.Type == cur.typ {
			return i, true
		}
	}
	return 0, false
}

// annotInsertionPoint returns the index where the cursor would be inserted
// in the flat list under (file, line) ordering - i.e. the index of the
// first annotation strictly after the cursor, or len(flat) if all entries
// are at or before the cursor.
func annotInsertionPoint(flat []annot.Annotation, cur cursorAnnotKey) int {
	for i, a := range flat {
		if compareAnnotPos(a.File, a.Line, cur.file, cur.line) > 0 {
			return i
		}
	}
	return len(flat)
}

// compareAnnotPos compares two (file, line) annotation positions using the
// same ordering as the flat annotation list: alphabetical by file, then
// ascending by line within a file. Returns -1, 0, or 1.
func compareAnnotPos(aFile string, aLine int, bFile string, bLine int) int {
	if aFile != bFile {
		if aFile < bFile {
			return -1
		}
		return 1
	}
	if aLine < bLine {
		return -1
	}
	if aLine > bLine {
		return 1
	}
	return 0
}
