package tui

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yousysadmin/igit/internal/annot"
	"github.com/yousysadmin/igit/internal/git"
)

// decoratedModel loads one file with a line annotation and a file-level one.
func decoratedModel(t *testing.T) Model {
	t.Helper()
	lines := []git.DiffLine{
		{OldNum: 1, NewNum: 1, Content: "ctx", ChangeType: git.ChangeContext},
		{NewNum: 2, Content: "added", ChangeType: git.ChangeAdd},
	}
	m := testModel([]string{"a.go"}, map[string][]git.DiffLine{"a.go": lines})
	m.file.name = "a.go"
	m.file.lines = lines
	m.store.Add(annot.Annotation{File: "a.go", Line: 2, Type: "+", Comment: "on the added line"})
	m.store.Add(annot.Annotation{File: "a.go", Line: 0, Comment: "about the file"})
	return m
}

func TestDecorations_collectsLineAndFileAnnotations(t *testing.T) {
	d := decoratedModel(t).decorations()

	require.Len(t, d.comments, 1, "the file-level annotation is not a line annotation")
	assert.Equal(t, "on the added line", d.comments[annotLineKey{line: 2, changeType: git.ChangeAdd}])
	assert.True(t, d.hasFile)
	assert.Equal(t, "about the file", d.fileComment)
	assert.False(t, d.empty())
}

func TestDecorations_emptyForAHostThatHidesThem(t *testing.T) {
	m := decoratedModel(t)
	m.setPaneAnnotationsHidden(true)

	d := m.decorations()
	assert.Empty(t, d.comments)
	assert.False(t, d.hasFile, "a hidden file annotation must not reserve a row")
	assert.Empty(t, d.fileComment)
	assert.True(t, d.empty())
}

func TestDecorationsFor(t *testing.T) {
	m := decoratedModel(t)
	d := m.decorations()

	comment, has, _ := m.decorationsFor(d, m.file.lines[1])
	assert.True(t, has)
	assert.Equal(t, "on the added line", comment)

	_, has, _ = m.decorationsFor(d, m.file.lines[0])
	assert.False(t, has, "an unannotated line carries nothing")

	_, has, remotes := m.decorationsFor(d, git.DiffLine{ChangeType: git.ChangeDivider})
	assert.False(t, has, "a divider is never annotated")
	assert.Empty(t, remotes)
}

// A file annotated in review mode must not bleed into the staged diff commit
// mode renders for the same path: those line numbers address a different text.
func TestDecorations_hiddenFileAnnotationDrawsNoRow(t *testing.T) {
	m := decoratedModel(t)
	m.layout.width, m.layout.height = 100, 20
	m.ready = true

	shown := m.renderDiff()
	require.Contains(t, shown, "about the file", "review mode draws the file annotation")

	m.setPaneAnnotationsHidden(true)
	hidden := m.renderDiff()
	assert.NotContains(t, hidden, "about the file")
	assert.NotContains(t, hidden, "on the added line")
	assert.NotContains(t, hidden, m.annotFilePrefix(), "no empty marker row is left behind")
	assert.Len(t, strings.Split(hidden, "\n"), len(strings.Split(shown, "\n"))-2,
		"both annotation rows are gone, not blanked")
}
