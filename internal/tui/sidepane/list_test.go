package sidepane

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yousysadmin/igit/internal/tui/style"
)

func sampleRows() []ListRow {
	return []ListRow{
		{Text: "Staged (2)", Section: true},
		{Key: "s:a.go", Text: "a.go", Prefix: "M  ", PlainPrefix: "M  "},
		{Key: "s:b.go", Text: "b.go", Prefix: "A  ", PlainPrefix: "A  "},
		{Text: "Unstaged (1)", Section: true},
		{Key: "u:c.go", Text: "c.go", Prefix: "?? ", PlainPrefix: "?? "},
	}
}

func TestList_MoveSkipsSections(t *testing.T) {
	l := NewList()
	l.SetRows(sampleRows())
	row, ok := l.Cursor()
	require.True(t, ok)
	assert.Equal(t, "s:a.go", row.Key, "cursor starts on the first item, not the header")

	l.Move(MotionDown)
	l.Move(MotionDown)
	row, _ = l.Cursor()
	assert.Equal(t, "u:c.go", row.Key, "section row is skipped")
	l.Move(MotionDown)
	row, _ = l.Cursor()
	assert.Equal(t, "u:c.go", row.Key, "stops at the end")
	l.Move(MotionUp)
	row, _ = l.Cursor()
	assert.Equal(t, "s:b.go", row.Key)
	l.Move(MotionFirst)
	assert.Equal(t, 1, l.CursorIndex())
	l.Move(MotionLast)
	assert.Equal(t, 4, l.CursorIndex())
	l.Move(MotionPageUp, 10)
	assert.Equal(t, 1, l.CursorIndex())
	l.Move(MotionPageDown, 2)
	assert.Equal(t, 4, l.CursorIndex(), "page down lands past the header on the next item")
	l.Move(MotionUnknown)
	assert.Equal(t, 4, l.CursorIndex())
}

func TestList_SetRowsKeepsCursorByKey(t *testing.T) {
	l := NewList()
	l.SetRows(sampleRows())
	require.True(t, l.SelectByKey("s:b.go"))
	// b.go moves to the unstaged section. cursor follows it
	l.SetRows([]ListRow{
		{Text: "Staged (1)", Section: true},
		{Key: "s:a.go", Text: "a.go"},
		{Text: "Unstaged (2)", Section: true},
		{Key: "u:b.go", Text: "b.go"},
		{Key: "s:b.go", Text: "b.go"},
	})
	assert.Equal(t, 4, l.CursorIndex())
	// key gone: cursor stays at the same index when that is an item
	l.SetRows([]ListRow{
		{Text: "Staged (1)", Section: true},
		{Key: "s:a.go", Text: "a.go"},
		{Text: "Unstaged (1)", Section: true},
		{Key: "u:z.go", Text: "z.go"},
	})
	assert.Equal(t, 3, l.CursorIndex())
	// shrinking list clamps to the last item
	l.SetRows([]ListRow{{Text: "Staged (1)", Section: true}, {Key: "s:a.go", Text: "a.go"}})
	assert.Equal(t, 1, l.CursorIndex())
	// empty list
	l.SetRows(nil)
	_, ok := l.Cursor()
	assert.False(t, ok)
	assert.Equal(t, 0, l.Len())
	assert.False(t, l.SelectByKey("nope"))
	assert.False(t, l.SelectIndex(0))
}

func TestList_SectionsOnlyHasNoCursor(t *testing.T) {
	l := NewList()
	l.SetRows([]ListRow{{Text: "Staged (0)", Section: true}})
	_, ok := l.Cursor()
	assert.False(t, ok)
	l.Move(MotionDown)
	assert.Equal(t, 0, l.CursorIndex())
}

func TestList_RenderAndScroll(t *testing.T) {
	l := NewList()
	l.SetRows(sampleRows())
	res := style.PlainResolver()
	out := l.Render(ListRender{Width: 20, Height: 10, Resolver: res})
	lines := strings.Split(out, "\n")
	require.Len(t, lines, 5)
	assert.Contains(t, lines[0], "Staged (2)")
	assert.Contains(t, lines[1], "M  a.go")
	assert.Contains(t, lines[4], "?? c.go")

	// window of 2 rows follows the cursor
	l.Move(MotionLast)
	out = l.Render(ListRender{Width: 20, Height: 2, Resolver: res})
	lines = strings.Split(out, "\n")
	require.Len(t, lines, 2)
	assert.Contains(t, lines[1], "c.go")
	assert.Equal(t, ScrollState{Total: 5, Offset: 3}, l.ScrollState())

	// empty list message
	empty := NewList()
	assert.Equal(t, "  nothing here", empty.Render(ListRender{Width: 20, Height: 5, Resolver: res}))
	assert.Equal(t, "  clean", empty.Render(ListRender{Width: 20, Height: 5, Empty: "  clean", Resolver: res}))
}

func TestList_RenderTruncates(t *testing.T) {
	l := NewList()
	l.SetRows([]ListRow{
		{Text: "A very long section title that overflows", Section: true},
		{Key: "k", Text: "some/deeply/nested/directory/file.go", Prefix: "M  ", PlainPrefix: "M  "},
	})
	res := style.PlainResolver()
	out := l.Render(ListRender{Width: 16, Height: 5, Resolver: res})
	lines := strings.Split(out, "\n")
	require.Len(t, lines, 2)
	assert.Contains(t, lines[0], "…")
	assert.Contains(t, lines[1], "file.go", "paths keep their tail")
	assert.LessOrEqual(t, len([]rune(strings.TrimRight(lines[1], " "))), 16)
}

func TestFitRowAndTruncateRight(t *testing.T) {
	assert.Equal(t, "M abc", fitRow("M ", "abc", 10, false))
	assert.Equal(t, "M ", fitRow("M ", "abc", 2, false))
	assert.Equal(t, "M …e", fitRow("M ", "abcde", 4, false))
	assert.Equal(t, "M a…", fitRow("M ", "abcde", 4, true), "tail-cut rows keep their start")
	assert.Equal(t, "abc", TruncateRight("abc", 3))
	assert.Equal(t, "ab…", TruncateRight("abcdef", 3))
	assert.Equal(t, "…", TruncateRight("abcdef", 1))
	assert.Empty(t, TruncateRight("abc", 0))
}

func TestList_AccentRowStandsOut(t *testing.T) {
	// the checked-out branch is the one entry the list is built around, a star
	// alone is easy to miss in a long list
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor) // tests run without a TTY, where colors would be stripped
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })
	res := style.NewResolver(style.Colors{Accent: "#5f87ff", Normal: "#d0d0d0", SelectedFg: "#ffffaf", SelectedBg: "#303030"})
	var l List
	l.SetRows([]ListRow{
		{Key: "l:main", Text: "main", Prefix: "* ", PlainPrefix: "* ", Accent: true},
		{Key: "l:feature", Text: "feature", Prefix: "  ", PlainPrefix: "  "},
		{Key: "l:spike", Text: "spike", Prefix: "  ", PlainPrefix: "  "},
	})
	l.SelectByKey("l:feature") // keep the cursor off both rows under test

	lines := strings.Split(l.Render(ListRender{Width: 30, Height: 5, Resolver: res}), "\n")
	require.Len(t, lines, 3)

	// lipgloss folds the attributes into one sequence, so match the color itself
	const (
		accent   = "38;2;95;135;255"
		normal   = "38;2;208;208;208"
		selected = "38;2;255;255;175"
	)
	assert.Contains(t, lines[0], accent, "the checked-out branch is painted in the accent color")
	assert.NotContains(t, lines[2], accent, "an ordinary branch is not")
	assert.Contains(t, lines[2], normal)
	assert.Contains(t, lines[1], selected, "the cursor row is the cursor row")

	// the cursor keeps its own styling wherever it lands
	l.SelectByKey("l:main")
	lines = strings.Split(l.Render(ListRender{Width: 30, Height: 5, Resolver: res}), "\n")
	assert.Contains(t, lines[0], selected, "the cursor row wins over the accent")
	assert.Contains(t, lines[1], normal, "the row the cursor left goes back to normal")
	assert.NotContains(t, lines[1], accent)
}
