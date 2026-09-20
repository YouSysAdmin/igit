package sidepane

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/yousysadmin/igit/internal/tui/style"
)

// ListRow is one line of a List: either a section header (Section=true, not
// selectable) or an item. Key identifies an item across rebuilds so the cursor
// can stay on the same entry after a refresh. Prefix is pre-rendered ANSI text
// (a status mark, for example) drawn before Text. it is replaced by PlainPrefix
// on the selected row, whose background would otherwise swallow the colors.
type ListRow struct {
	Key         string
	Text        string
	Prefix      string
	PlainPrefix string
	Section     bool
	TailCut     bool // truncate the end of a long Text (commit subjects) instead of its start (paths)
	// Accent lifts the row out of the list in the accent color, for the one
	// entry the list is built around such as the checked-out branch. The cursor
	// row keeps its own styling.
	Accent bool
	Meta   any
}

// List is a flat, scrollable list with section headers, used by the commit
// mode side pane (files, branches, log, stash). The cursor skips section rows.
type List struct {
	rows   []ListRow
	cursor int
	offset int
}

// NewList returns an empty list.
func NewList() *List { return &List{} }

// SetRows replaces the rows and keeps the cursor on the row with the same Key
// when it still exists, otherwise on the same index (or the nearest item).
func (l *List) SetRows(rows []ListRow) {
	prevKey := ""
	if r, ok := l.Cursor(); ok {
		prevKey = r.Key
	}
	l.rows = rows
	if prevKey != "" && l.SelectByKey(prevKey) {
		return
	}
	l.cursor = l.nearestItem(min(l.cursor, len(rows)-1))
}

// Len returns the number of rows including section headers.
func (l *List) Len() int { return len(l.rows) }

// Rows returns the current rows.
func (l *List) Rows() []ListRow { return l.rows }

// Cursor returns the row under the cursor. ok is false when the list has no items.
func (l *List) Cursor() (ListRow, bool) {
	if l.cursor < 0 || l.cursor >= len(l.rows) || l.rows[l.cursor].Section {
		return ListRow{}, false
	}
	return l.rows[l.cursor], true
}

// CursorIndex returns the cursor row index (may point at a section when the
// list has no items).
func (l *List) CursorIndex() int { return l.cursor }

// SelectByKey moves the cursor to the item with key. false when absent.
func (l *List) SelectByKey(key string) bool {
	for i, r := range l.rows {
		if !r.Section && r.Key == key {
			l.cursor = i
			return true
		}
	}
	return false
}

// SelectIndex moves the cursor to row idx when it is an item.
func (l *List) SelectIndex(idx int) bool {
	if idx < 0 || idx >= len(l.rows) || l.rows[idx].Section {
		return false
	}
	l.cursor = idx
	return true
}

// Move applies a cursor motion. page motions take the page size as count.
func (l *List) Move(m Motion, count ...int) {
	n := 1
	if len(count) > 0 {
		n = max(count[0], 1)
	}
	switch m {
	case MotionUp:
		l.step(-1)
	case MotionDown:
		l.step(1)
	case MotionPageUp:
		l.cursor = l.nearestItemBackward(max(l.cursor-n, 0))
	case MotionPageDown:
		l.cursor = l.nearestItem(min(l.cursor+n, len(l.rows)-1))
	case MotionFirst:
		l.cursor = l.nearestItem(0)
	case MotionLast:
		l.cursor = l.nearestItemBackward(len(l.rows) - 1)
	case MotionUnknown:
	}
}

// step moves one item in direction dir, skipping section rows and stopping at the ends.
func (l *List) step(dir int) {
	for i := l.cursor + dir; i >= 0 && i < len(l.rows); i += dir {
		if !l.rows[i].Section {
			l.cursor = i
			return
		}
	}
}

// nearestItem returns the first item index at or after idx, else the last item
// before it, else 0.
func (l *List) nearestItem(idx int) int {
	idx = max(idx, 0)
	for i := idx; i < len(l.rows); i++ {
		if !l.rows[i].Section {
			return i
		}
	}
	return l.nearestItemBackward(idx)
}

// nearestItemBackward returns the first item index at or before idx, else the
// first item after it, else 0.
func (l *List) nearestItemBackward(idx int) int {
	for i := min(idx, len(l.rows)-1); i >= 0; i-- {
		if !l.rows[i].Section {
			return i
		}
	}
	for i := idx + 1; i < len(l.rows); i++ {
		if !l.rows[i].Section {
			return i
		}
	}
	return 0
}

// EnsureVisible scrolls so the cursor row is inside a window of height rows.
func (l *List) EnsureVisible(height int) {
	ensureVisible(&l.cursor, &l.offset, len(l.rows), height)
}

// ScrollState reports the visible window for the scrollbar.
func (l *List) ScrollState() ScrollState {
	return ScrollState{Total: len(l.rows), Offset: l.offset}
}

// ListRender holds parameters for List.Render.
type ListRender struct {
	Width    int
	Height   int
	Focused  bool
	Empty    string // shown when there are no rows
	Resolver Resolver
}

// Render draws the visible window. Section rows use the directory style, the
// cursor row the selected-file style, other rows the file-entry style.
func (l *List) Render(r ListRender) string {
	if len(l.rows) == 0 {
		empty := r.Empty
		if empty == "" {
			empty = "  nothing here"
		}
		return empty
	}
	l.EnsureVisible(r.Height)
	end := min(l.offset+r.Height, len(l.rows))
	maxWidth := max(r.Width-2, 1)
	var b strings.Builder
	for idx := l.offset; idx < end; idx++ {
		row := l.rows[idx]
		switch {
		case row.Section:
			b.WriteString(r.Resolver.Style(style.StyleKeyDirEntry).Render(" " + TruncateRight(row.Text, maxWidth-1)))
		case idx == l.cursor:
			text := fitRow(row.PlainPrefix, row.Text, maxWidth, row.TailCut)
			b.WriteString(r.Resolver.Style(style.StyleKeyFileSelected).Width(maxWidth).Render(text))
		case row.Accent:
			text := fitRow(row.PlainPrefix, row.Text, maxWidth, row.TailCut)
			b.WriteString(r.Resolver.Style(style.StyleKeyDirEntry).Render(text))
		default:
			text := fitRow(row.Prefix, row.Text, maxWidth, row.TailCut)
			b.WriteString(r.Resolver.Style(style.StyleKeyFileEntry).Render(text))
		}
		if idx < end-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

// fitRow joins prefix and text so the row fits in width. Paths are truncated
// from the left (their tail matters). tailCut rows keep their start.
func fitRow(prefix, text string, width int, tailCut bool) string {
	pw := lipgloss.Width(prefix)
	budget := width - pw
	if budget <= 0 {
		return prefix
	}
	if lipgloss.Width(text) > budget {
		if tailCut {
			text = TruncateRight(text, budget)
		} else {
			text = style.TruncateLeftToWidth(text, budget)
		}
	}
	return prefix + text
}

// TruncateRight cuts text to width cells, appending an ellipsis when cut.
func TruncateRight(text string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(text) <= width {
		return text
	}
	if width == 1 {
		return "…"
	}
	runes := []rune(text)
	out := make([]rune, 0, len(runes))
	w := 0
	for _, r := range runes {
		rw := lipgloss.Width(string(r))
		if w+rw > width-1 {
			break
		}
		out = append(out, r)
		w += rw
	}
	return string(out) + "…"
}
