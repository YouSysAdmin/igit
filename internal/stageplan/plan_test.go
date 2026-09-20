package stageplan

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yousysadmin/igit/internal/git"
)

func block() []git.DiffLine {
	return []git.DiffLine{
		{OldNum: 4, NewNum: 4, Content: "ctx", ChangeType: git.ChangeContext},
		{OldNum: 5, Content: "old a", ChangeType: git.ChangeRemove},
		{OldNum: 6, Content: "old b", ChangeType: git.ChangeRemove},
		{NewNum: 5, Content: "new a", ChangeType: git.ChangeAdd},
		{ChangeType: git.ChangeDivider, Content: "..."},
	}
}

func TestNewRangeAndCovers(t *testing.T) {
	r := NewRange(block())
	assert.Equal(t, 5, r.OldStart)
	assert.Equal(t, 6, r.OldEnd)
	assert.Equal(t, 5, r.NewStart)
	assert.Equal(t, 5, r.NewEnd)
	assert.Len(t, r.Hash, 64)
	assert.Equal(t, HashLines(block()[1:4]), r.Hash, "context and divider rows do not affect the hash")

	assert.True(t, r.Covers(5, 0))
	assert.True(t, r.Covers(6, 0))
	assert.False(t, r.Covers(7, 0))
	assert.True(t, r.Covers(0, 5))
	assert.False(t, r.Covers(0, 6))
	assert.False(t, r.Covers(4, 4), "context line inside the block is not a change")

	addOnly := NewRange([]git.DiffLine{{NewNum: 9, Content: "x", ChangeType: git.ChangeAdd}})
	assert.Equal(t, 0, addOnly.OldStart)
	assert.False(t, addOnly.Covers(9, 0), "no old side")
	assert.True(t, addOnly.Covers(0, 9))

	changed := block()
	changed[3].Content = "edited"
	assert.NotEqual(t, r.Hash, NewRange(changed).Hash)
}

func TestPlan_toggleFileAndRange(t *testing.T) {
	p := New()
	assert.True(t, p.Empty())
	assert.Equal(t, uint64(0), p.Version())

	assert.True(t, p.ToggleFile("a.go"))
	assert.True(t, p.Has("a.go"))
	assert.True(t, p.IsWhole("a.go"))
	assert.True(t, p.Covers("a.go", 1, 0))
	assert.Equal(t, 1, p.Count())
	assert.False(t, p.ToggleFile("a.go"), "second toggle clears the file")
	assert.False(t, p.Has("a.go"))

	r1 := NewRange(block())
	r2 := NewRange([]git.DiffLine{{NewNum: 20, Content: "z", ChangeType: git.ChangeAdd}})
	assert.True(t, p.ToggleRange("b.go", r2))
	assert.True(t, p.ToggleRange("b.go", r1))
	marks, ok := p.Marks("b.go")
	require.True(t, ok)
	assert.False(t, marks.Whole)
	assert.Equal(t, []Range{r1, r2}, marks.Ranges, "ranges are kept sorted by position")
	assert.True(t, p.Covers("b.go", 5, 0))
	assert.True(t, p.Covers("b.go", 0, 20))
	assert.False(t, p.Covers("b.go", 0, 7))
	assert.False(t, p.Covers("nope", 0, 20))

	assert.False(t, p.ToggleRange("b.go", r1), "toggling an identical range removes it")
	marks, _ = p.Marks("b.go")
	assert.Equal(t, []Range{r2}, marks.Ranges)
	assert.False(t, p.ToggleRange("b.go", r2))
	assert.False(t, p.Has("b.go"), "last range gone: file leaves the plan")

	// whole-file mark is replaced by a range mark
	p.ToggleFile("c.go")
	assert.True(t, p.ToggleRange("c.go", r1))
	assert.False(t, p.IsWhole("c.go"))
	marks, _ = p.Marks("c.go")
	assert.Equal(t, []Range{r1}, marks.Ranges)

	assert.Equal(t, []string{"c.go"}, p.Files())
	p.Remove("c.go")
	p.Remove("c.go") // no-op
	assert.True(t, p.Empty())
	_, ok = p.Marks("c.go")
	assert.False(t, ok)
	assert.Positive(t, p.Version())
}
