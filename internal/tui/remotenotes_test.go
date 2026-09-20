package tui

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yousysadmin/igit/internal/annot"
	"github.com/yousysadmin/igit/internal/git"
)

// remoteNoteModel loads a file with one local annotation and a spread of remote
// comments, indexing them the way handleFileLoaded does.
func remoteNoteModel(t *testing.T) (Model, []git.DiffLine) {
	t.Helper()
	lines := []git.DiffLine{
		{OldNum: 1, NewNum: 1, Content: "ctx", ChangeType: git.ChangeContext},
		{OldNum: 2, Content: "gone", ChangeType: git.ChangeRemove},
		{NewNum: 2, Content: "new", ChangeType: git.ChangeAdd},
		{OldNum: 3, NewNum: 3, Content: "tail", ChangeType: git.ChangeContext},
	}
	store := annot.NewStore()
	store.Add(annot.Annotation{File: "a.go", Line: 2, Type: "+", Comment: "mine"})
	m := testNewModel(t, plainRenderer(), store, noopHighlighter(), ModelConfig{RemoteNotes: []RemoteNote{
		{File: "a.go", Line: 2, Side: "RIGHT", Author: "alice", Body: "theirs on the added line"},
		{File: "a.go", Line: 2, Side: "LEFT", Author: "bob", Body: "on the removed line"},
		{File: "a.go", Line: 3, Side: "LEFT", Author: "carol", Body: "left side of a context line"},
		{File: "a.go", Line: 99, Side: "RIGHT", Author: "dan", Body: "outside"},
		{File: "other.go", Line: 1, Side: "RIGHT", Author: "eve", Body: "other file"},
	}})
	m.file.name = "a.go"
	m.file.lines = lines
	m.remoteByKey = m.indexRemoteNotes()
	return m, lines
}

func TestModel_RemoteNotesAnchorToTheirLines(t *testing.T) {
	m, lines := remoteNoteModel(t)

	assert.Len(t, m.remoteByKey, 3, "unanchored and other-file notes are ignored")
	require.Len(t, m.remoteByKey["2:+"], 1)
	assert.Equal(t, "alice", m.remoteByKey["2:+"][0].Author)
	require.Len(t, m.remoteByKey["2:-"], 1)
	assert.Equal(t, "bob", m.remoteByKey["2:-"][0].Author)
	require.Len(t, m.remoteByKey["3: "], 1)
	assert.Equal(t, "carol", m.remoteByKey["3: "][0].Author, "a LEFT note on a context line lands on that context row")

	assert.Equal(t, map[annotLineKey]string{{line: 2, changeType: git.ChangeAdd}: "mine"}, m.decorations().comments,
		"the annotation map stays the session's own work")

	files := m.annotatedFiles()
	assert.True(t, files["a.go"])
	assert.True(t, files["other.go"], "files with remote comments are marked in the tree")
	assert.Equal(t, 1, m.store.Count(), "remote notes never enter the store")
	assert.False(t, m.hasAnnotation(lines[1]), "navigation only knows local annotations")
}

func TestModel_RemoteNotesRenderAsTheirOwnBlock(t *testing.T) {
	m, _ := remoteNoteModel(t)
	m.layout.viewport.Width = 100

	// a line carrying both: the local annotation keeps its marker, the remote
	// comment gets its own instead of reading as a second line of ours
	segments := m.annotationSegments(true, m.annotPrefix(), "mine", m.remoteByKey["2:+"])
	require.Len(t, segments, 2)
	assert.Equal(t, annotSegment{prefix: m.annotPrefix(), body: "mine"}, segments[0])
	assert.Equal(t, annotSegment{prefix: m.remoteNotePrefix(), body: "@alice: theirs on the added line"}, segments[1])
	assert.NotEqual(t, m.annotPrefix(), m.remoteNotePrefix(), "the two markers must differ")

	// a line carrying only a remote comment has no local block
	segments = m.annotationSegments(false, m.annotPrefix(), "", m.remoteByKey["2:-"])
	require.Len(t, segments, 1)
	assert.Equal(t, m.remoteNotePrefix(), segments[0].prefix)

	assert.Empty(t, m.annotationSegments(false, m.annotPrefix(), "", nil), "nothing to paint")
}

func TestModel_RemoteNotesCountTowardsLineHeight(t *testing.T) {
	m, _ := remoteNoteModel(t)
	m.layout.viewport.Width = 100

	both := m.wrappedAnnotationLineCount("2:+")
	assert.Equal(t, 2, both, "the local annotation and the remote comment each take a row")

	remoteOnly := m.wrappedAnnotationLineCount("2:-")
	assert.Equal(t, 1, remoteOnly, "a remote comment alone still occupies its row")

	assert.Equal(t, 1, m.wrappedAnnotationLineCount("1: "), "a bare line reports the legacy single row")

	set := m.buildAnnotationSet()
	assert.True(t, set["2:+"], "a line with a local annotation is measured")
	assert.True(t, set["2:-"], "a line with only remote comments is measured too")
	assert.True(t, set["3: "])
}

func TestRemoteNote_text(t *testing.T) {
	assert.Equal(t, "@a: b", RemoteNote{Author: "a", Body: "b"}.text())
	assert.Equal(t, "b", RemoteNote{Body: "b"}.text())
	assert.Nil(t, groupRemoteNotes(nil))
}
