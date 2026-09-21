package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yousysadmin/igit/internal/annot"
	"github.com/yousysadmin/igit/internal/git"
	"github.com/yousysadmin/igit/internal/tui/overlay"
)

func TestModel_BuildAnnotListItems(t *testing.T) {
	t.Run("multiple files and annotations sorted by file then line", func(t *testing.T) {
		m := testModel([]string{"b.go", "a.go"}, nil)
		m.store.Add(annot.Annotation{File: "b.go", Line: 10, Type: "+", Comment: "fix this"})
		m.store.Add(annot.Annotation{File: "b.go", Line: 5, Type: "-", Comment: "remove"})
		m.store.Add(annot.Annotation{File: "a.go", Line: 3, Type: "+", Comment: "add here"})
		m.store.Add(annot.Annotation{File: "a.go", Line: 0, Type: "", Comment: "file note"})

		items := m.buildAnnotListItems()
		require.Len(t, items, 4)

		// a.go comes first (alphabetical), file-level (line 0) before line 3
		assert.Equal(t, "a.go", items[0].File)
		assert.Equal(t, 0, items[0].Line)
		assert.Equal(t, "a.go", items[1].File)
		assert.Equal(t, 3, items[1].Line)

		// b.go second, line 5 before line 10
		assert.Equal(t, "b.go", items[2].File)
		assert.Equal(t, 5, items[2].Line)
		assert.Equal(t, "b.go", items[3].File)
		assert.Equal(t, 10, items[3].Line)
	})

	t.Run("empty store returns empty slice", func(t *testing.T) {
		m := testModel(nil, nil)
		items := m.buildAnnotListItems()
		assert.Empty(t, items)
	})

	t.Run("single annotation", func(t *testing.T) {
		m := testModel([]string{"x.go"}, nil)
		m.store.Add(annot.Annotation{File: "x.go", Line: 42, Type: "+", Comment: "check"})

		items := m.buildAnnotListItems()
		require.Len(t, items, 1)
		assert.Equal(t, "x.go", items[0].File)
		assert.Equal(t, 42, items[0].Line)
	})
}

func TestModel_BuildAnnotListSpec(t *testing.T) {
	m := testModel([]string{"a.go"}, nil)
	m.store.Add(annot.Annotation{File: "a.go", Line: 5, Type: "+", Comment: "note"})
	m.store.Add(annot.Annotation{File: "a.go", Line: 0, Type: "", Comment: "file note"})

	spec := m.buildAnnotListSpec()
	require.Len(t, spec.Items, 2)
	assert.Equal(t, "a.go", spec.Items[0].File)
	assert.Equal(t, 0, spec.Items[0].Line)
	assert.Equal(t, "a.go", spec.Items[1].File)
	assert.Equal(t, 5, spec.Items[1].Line)
	assert.Equal(t, "+", spec.Items[1].ChangeType)
}

func TestModel_AnnotListOpenClose(t *testing.T) {
	t.Run("@ opens annotation list overlay", func(t *testing.T) {
		m := testModel([]string{"a.go"}, nil)
		m.store.Add(annot.Annotation{File: "a.go", Line: 5, Type: "+", Comment: "note"})

		result, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'@'}})
		model := result.(Model)
		assert.True(t, model.overlay.Active())
		assert.Equal(t, overlay.KindAnnotList, model.overlay.Kind())
	})

	t.Run("@ closes annotation list when already open", func(t *testing.T) {
		m := testModel([]string{"a.go"}, nil)
		m.overlay.OpenAnnotList(m.buildAnnotListSpec())

		result, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'@'}})
		model := result.(Model)
		assert.False(t, model.overlay.Active())
	})

	t.Run("esc closes annotation list", func(t *testing.T) {
		m := testModel([]string{"a.go"}, nil)
		m.overlay.OpenAnnotList(m.buildAnnotListSpec())

		result, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
		model := result.(Model)
		assert.False(t, model.overlay.Active())
	})
}

func TestModel_FindDiffLineIndex(t *testing.T) {
	diffs := map[string][]git.DiffLine{
		"a.go": {
			{ChangeType: git.ChangeContext, Content: "ctx1", OldNum: 1, NewNum: 1},
			{ChangeType: git.ChangeRemove, Content: "old", OldNum: 2, NewNum: 0},
			{ChangeType: git.ChangeAdd, Content: "new", OldNum: 0, NewNum: 2},
			{ChangeType: git.ChangeContext, Content: "ctx2", OldNum: 3, NewNum: 3},
		},
	}
	m := testModel([]string{"a.go"}, diffs)
	m.file.lines = diffs["a.go"]

	t.Run("find add line by NewNum", func(t *testing.T) {
		idx := m.findDiffLineIndex(2, "+")
		assert.Equal(t, 2, idx)
	})

	t.Run("find remove line by OldNum", func(t *testing.T) {
		idx := m.findDiffLineIndex(2, "-")
		assert.Equal(t, 1, idx)
	})

	t.Run("find context line by NewNum", func(t *testing.T) {
		idx := m.findDiffLineIndex(3, " ")
		assert.Equal(t, 3, idx)
	})

	t.Run("not found returns -1", func(t *testing.T) {
		idx := m.findDiffLineIndex(99, "+")
		assert.Equal(t, -1, idx)
	})

	t.Run("wrong change type returns -1", func(t *testing.T) {
		idx := m.findDiffLineIndex(2, " ")
		assert.Equal(t, -1, idx)
	})
}

func TestModel_JumpToAnnotationTarget_SameFile(t *testing.T) {
	diffs := map[string][]git.DiffLine{
		"a.go": {
			{ChangeType: git.ChangeContext, Content: "line1", OldNum: 1, NewNum: 1},
			{ChangeType: git.ChangeAdd, Content: "new", OldNum: 0, NewNum: 2},
			{ChangeType: git.ChangeContext, Content: "line3", OldNum: 2, NewNum: 3},
		},
	}
	m := testModel([]string{"a.go"}, diffs)

	// simulate file load
	result, _ := m.Update(filesLoadedMsg{entries: []git.FileEntry{{Path: "a.go"}}})
	m = result.(Model)
	msg := m.loadFileDiff("a.go")()
	result, _ = m.Update(msg)
	m = result.(Model)

	t.Run("same-file jump positions cursor", func(t *testing.T) {
		target := &overlay.AnnotationTarget{File: "a.go", ChangeType: "+", Line: 2}
		result, _ := m.jumpToAnnotationTarget(target)
		model := result.(Model)
		assert.Equal(t, 1, model.nav.diffCursor)
		assert.Equal(t, paneDiff, model.layout.focus)
	})

	t.Run("file-level annotation sets cursor to -1", func(t *testing.T) {
		target := &overlay.AnnotationTarget{File: "a.go", ChangeType: "", Line: 0}
		result, _ := m.jumpToAnnotationTarget(target)
		model := result.(Model)
		assert.Equal(t, -1, model.nav.diffCursor)
	})

	t.Run("nil target is no-op", func(t *testing.T) {
		result, _ := m.jumpToAnnotationTarget(nil)
		model := result.(Model)
		assert.Equal(t, m.nav.diffCursor, model.nav.diffCursor)
	})
}

func TestModel_JumpToAnnotationTarget_CrossFile(t *testing.T) {
	diffs := map[string][]git.DiffLine{
		"a.go": {{ChangeType: git.ChangeContext, Content: "line1", OldNum: 1, NewNum: 1}},
		"b.go": {
			{ChangeType: git.ChangeContext, Content: "ctx", OldNum: 1, NewNum: 1},
			{ChangeType: git.ChangeAdd, Content: "added", OldNum: 0, NewNum: 2},
		},
	}
	m := testModel([]string{"a.go", "b.go"}, diffs)

	// load files and first file
	result, _ := m.Update(filesLoadedMsg{entries: []git.FileEntry{{Path: "a.go"}, {Path: "b.go"}}})
	m = result.(Model)
	msg := m.loadFileDiff("a.go")()
	result, _ = m.Update(msg)
	m = result.(Model)
	assert.Equal(t, "a.go", m.file.name)

	t.Run("cross-file jump sets pending and triggers load", func(t *testing.T) {
		target := &overlay.AnnotationTarget{File: "b.go", ChangeType: "+", Line: 2}
		result, cmd := m.jumpToAnnotationTarget(target)
		model := result.(Model)
		assert.NotNil(t, model.pendingAnnotJump)
		assert.Equal(t, "b.go", model.pendingAnnotJump.File)
		require.NotNil(t, cmd)

		// simulate file loaded
		loadMsg := cmd()
		result, _ = model.Update(loadMsg)
		model = result.(Model)
		assert.Equal(t, "b.go", model.file.name)
		assert.Nil(t, model.pendingAnnotJump)
		assert.Equal(t, 1, model.nav.diffCursor)
		assert.Equal(t, paneDiff, model.layout.focus)
	})
}

func TestModel_JumpToAnnotation_StalePendingGuard(t *testing.T) {
	diffs := map[string][]git.DiffLine{
		"a.go": {{ChangeType: git.ChangeContext, Content: "line1", OldNum: 1, NewNum: 1}},
		"b.go": {{ChangeType: git.ChangeAdd, Content: "added", OldNum: 0, NewNum: 1}},
	}
	m := testModel([]string{"a.go", "b.go"}, diffs)

	// load files and first file
	result, _ := m.Update(filesLoadedMsg{entries: []git.FileEntry{{Path: "a.go"}, {Path: "b.go"}}})
	m = result.(Model)
	msg := m.loadFileDiff("a.go")()
	result, _ = m.Update(msg)
	m = result.(Model)

	t.Run("stale pending jump is ignored when file does not match", func(t *testing.T) {
		// set pending for b.go but simulate a.go being loaded
		m.pendingAnnotJump = &annotJump{Annotation: annot.Annotation{File: "b.go", Line: 1, Type: "+"}}
		m.file.loadSeq++
		loadMsg := fileLoadedMsg{file: "a.go", seq: m.file.loadSeq, lines: diffs["a.go"]}
		result, _ := m.Update(loadMsg)
		model := result.(Model)
		// pending should not be cleared (file mismatch)
		assert.NotNil(t, model.pendingAnnotJump)
		assert.Equal(t, "b.go", model.pendingAnnotJump.File)
	})

	t.Run("n key clears pending jump", func(t *testing.T) {
		m.pendingAnnotJump = &annotJump{Annotation: annot.Annotation{File: "b.go", Line: 1, Type: "+"}}
		m.layout.focus = paneDiff
		result, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
		model := result.(Model)
		assert.Nil(t, model.pendingAnnotJump)
	})

	t.Run("p key clears pending jump", func(t *testing.T) {
		m.pendingAnnotJump = &annotJump{Annotation: annot.Annotation{File: "b.go", Line: 1, Type: "+"}}
		result, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
		model := result.(Model)
		assert.Nil(t, model.pendingAnnotJump)
	})
}

func TestModel_PendingAnnotJump_ClearedByTreeNav(t *testing.T) {
	diffs := map[string][]git.DiffLine{
		"a.go": {{ChangeType: git.ChangeContext, Content: "line1", OldNum: 1, NewNum: 1}},
		"b.go": {{ChangeType: git.ChangeAdd, Content: "added", OldNum: 0, NewNum: 1}},
	}
	m := testModel([]string{"a.go", "b.go"}, diffs)
	result, _ := m.Update(filesLoadedMsg{entries: []git.FileEntry{{Path: "a.go"}, {Path: "b.go"}}})
	m = result.(Model)
	msg := m.loadFileDiff("a.go")()
	result, _ = m.Update(msg)
	m = result.(Model)
	m.layout.focus = paneTree

	t.Run("tree j clears pending jump", func(t *testing.T) {
		m.pendingAnnotJump = &annotJump{Annotation: annot.Annotation{File: "b.go", Line: 1, Type: "+"}}
		result, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
		model := result.(Model)
		assert.Nil(t, model.pendingAnnotJump)
	})

	t.Run("tree k clears pending jump", func(t *testing.T) {
		m.pendingAnnotJump = &annotJump{Annotation: annot.Annotation{File: "b.go", Line: 1, Type: "+"}}
		result, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
		model := result.(Model)
		assert.Nil(t, model.pendingAnnotJump)
	})
}

func TestModel_PendingAnnotJump_ClearedByFilterToggle(t *testing.T) {
	diffs := map[string][]git.DiffLine{
		"a.go": {{ChangeType: git.ChangeContext, Content: "line1", OldNum: 1, NewNum: 1}},
		"b.go": {{ChangeType: git.ChangeAdd, Content: "added", OldNum: 0, NewNum: 1}},
	}
	m := testModel([]string{"a.go", "b.go"}, diffs)
	result, _ := m.Update(filesLoadedMsg{entries: []git.FileEntry{{Path: "a.go"}, {Path: "b.go"}}})
	m = result.(Model)
	msg := m.loadFileDiff("a.go")()
	result, _ = m.Update(msg)
	m = result.(Model)

	// add annotation so filter has something to toggle
	m.store.Add(annot.Annotation{File: "a.go", Line: 1, Type: "+", Comment: "note"})
	m.pendingAnnotJump = &annotJump{Annotation: annot.Annotation{File: "b.go", Line: 1, Type: "+"}}
	m.layout.focus = paneDiff
	result, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	model := result.(Model)
	assert.Nil(t, model.pendingAnnotJump)
}

func TestModel_PositionOnAnnotation_CollapsedMode(t *testing.T) {
	// set up a file with a remove line inside a hunk
	diffs := map[string][]git.DiffLine{
		"a.go": {
			{ChangeType: git.ChangeDivider, Content: "@@ -1,3 +1,2 @@"},
			{ChangeType: git.ChangeContext, Content: "ctx", OldNum: 1, NewNum: 1},
			{ChangeType: git.ChangeRemove, Content: "old", OldNum: 2, NewNum: 0},
			{ChangeType: git.ChangeAdd, Content: "new", OldNum: 0, NewNum: 2},
		},
	}
	m := testModel([]string{"a.go"}, diffs)
	result, _ := m.Update(filesLoadedMsg{entries: []git.FileEntry{{Path: "a.go"}}})
	m = result.(Model)
	msg := m.loadFileDiff("a.go")()
	result, _ = m.Update(msg)
	m = result.(Model)

	// enable collapsed mode - remove line at index 2 should be hidden
	m.modes.collapsed.enabled = true
	m.modes.collapsed.expandedHunks = make(map[int]bool)

	a := annot.Annotation{File: "a.go", Line: 2, Type: "-"}
	m.positionOnAnnotation(annotJump{Annotation: a})

	// hunk should be expanded so the remove line is visible
	assert.Equal(t, 2, m.nav.diffCursor)
	hunks := m.findHunks()
	assert.False(t, m.isCollapsedHidden(m.nav.diffCursor, hunks), "target line should be visible after jump")
}

func TestModel_PositionOnAnnotation_DeleteOnlyHunk(t *testing.T) {
	// delete-only hunk: first line is a placeholder in collapsed mode,
	// ensureHunkExpanded must expand it so the annotation is visible
	diffs := map[string][]git.DiffLine{
		"a.go": {
			{ChangeType: git.ChangeDivider, Content: "@@ -1,2 +1,0 @@"},
			{ChangeType: git.ChangeRemove, Content: "deleted1", OldNum: 1, NewNum: 0},
			{ChangeType: git.ChangeRemove, Content: "deleted2", OldNum: 2, NewNum: 0},
		},
	}
	m := testModel([]string{"a.go"}, diffs)
	result, _ := m.Update(filesLoadedMsg{entries: []git.FileEntry{{Path: "a.go"}}})
	m = result.(Model)
	msg := m.loadFileDiff("a.go")()
	result, _ = m.Update(msg)
	m = result.(Model)

	// enable collapsed mode - first remove line is a delete-only placeholder
	m.modes.collapsed.enabled = true
	m.modes.collapsed.expandedHunks = make(map[int]bool)

	a := annot.Annotation{File: "a.go", Line: 1, Type: "-"}
	m.positionOnAnnotation(annotJump{Annotation: a})

	// hunk must be expanded so the actual line and annotation are visible
	assert.Equal(t, 1, m.nav.diffCursor)
	hunks := m.findHunks()
	hunkStart := m.hunkStartFor(m.nav.diffCursor, hunks)
	assert.True(t, m.modes.collapsed.expandedHunks[hunkStart], "delete-only hunk should be expanded after jump")
	assert.False(t, m.isDeleteOnlyPlaceholder(m.nav.diffCursor, hunks), "line should not be a placeholder after expansion")
}

// TestBuildAnnotListItems_includesRequestComments covers the @ popup listing
// everything attached to the review, not just what this session typed.
func TestBuildAnnotListItems_includesRequestComments(t *testing.T) {
	store := annot.NewStore()
	store.Add(annot.Annotation{File: "a.go", Line: 5, Type: "+", Comment: "mine"})
	m := testNewModel(t, plainRenderer(), store, noopHighlighter(), ModelConfig{RemoteNotes: []RemoteNote{
		{File: "a.go", Line: 5, Side: "RIGHT", Author: "alice", Body: "theirs on the same line"},
		{File: "a.go", Line: 2, Side: "LEFT", Author: "bob", Body: "on a removed line"},
		{File: "b.go", Author: "carol", Body: "rewritten since", Outdated: true, OrigLine: 73},
		{File: "z.go", Line: 1, Side: "RIGHT", Author: "dan", Body: "another file"},
	}})

	items := m.buildAnnotListItems()
	got := make([][2]any, len(items))
	for i, it := range items {
		got[i] = [2]any{it.File, it.Line}
	}
	assert.Equal(t, [][2]any{
		{"a.go", 2}, {"a.go", 5}, {"a.go", 5}, {"b.go", 0}, {"z.go", 1},
	}, got, "files alphabetical, lines ascending, file-level first")

	assert.False(t, items[1].remote, "our own annotation comes before the request's on the same line")
	assert.Equal(t, "mine", items[1].Comment)
	assert.True(t, items[2].remote)
	assert.Equal(t, "@alice: theirs on the same line", items[2].Comment)
	assert.Equal(t, "-", items[0].Type, "a comment on a removed line reads on the old side")
	assert.Equal(t, "LEFT", items[0].side)
	assert.Equal(t, "(outdated b.go:73) @carol: rewritten since", items[3].Comment,
		"an outdated comment is listed and says where it was written")

	spec := m.buildAnnotListSpec()
	require.Len(t, spec.Items, 5, "the popup shows every one of them")
	assert.Equal(t, "RIGHT", spec.Items[2].Side, "the jump target carries the side it is numbered on")
}

// TestResolveJumpIndex_requestCommentSides covers a request comment naming only
// a side: its line is either a changed row or a context row.
func TestResolveJumpIndex_requestCommentSides(t *testing.T) {
	m := testNewModel(t, plainRenderer(), annot.NewStore(), noopHighlighter(), ModelConfig{})
	m.file.name = "a.go"
	m.file.lines = []git.DiffLine{
		{OldNum: 1, NewNum: 1, Content: "ctx", ChangeType: git.ChangeContext},
		{OldNum: 2, Content: "gone", ChangeType: git.ChangeRemove},
		{NewNum: 2, Content: "new", ChangeType: git.ChangeAdd},
	}

	add := annotJump{Annotation: annot.Annotation{Line: 2, Type: "+"}, side: "RIGHT"}
	assert.Equal(t, 2, m.resolveJumpIndex(add))

	removed := annotJump{Annotation: annot.Annotation{Line: 2, Type: "-"}, side: "LEFT"}
	assert.Equal(t, 1, m.resolveJumpIndex(removed))

	ctx := annotJump{Annotation: annot.Annotation{Line: 1, Type: "+"}, side: "RIGHT"}
	assert.Equal(t, 0, m.resolveJumpIndex(ctx), "a line the author did not touch is a context row")

	gone := annotJump{Annotation: annot.Annotation{Line: 99, Type: "+"}, side: "RIGHT"}
	assert.Equal(t, -1, m.resolveJumpIndex(gone), "the walker skips what it cannot show")
}
