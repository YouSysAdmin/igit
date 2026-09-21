package tui

import (
	"strings"
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

	assert.Len(t, m.remoteByKey, 4, "other-file notes are ignored, an unanchored one falls to the file block")
	require.Len(t, m.remoteByKey["2:+"], 1)
	assert.Equal(t, "alice", m.remoteByKey["2:+"][0].Author)
	require.Len(t, m.remoteByKey["2:-"], 1)
	assert.Equal(t, "bob", m.remoteByKey["2:-"][0].Author)
	require.Len(t, m.remoteByKey["3: "], 1)
	assert.Equal(t, "carol", m.remoteByKey["3: "][0].Author, "a LEFT note on a context line lands on that context row")
	require.Len(t, m.remoteByKey[annotKeyFile], 1)
	assert.Equal(t, "dan", m.remoteByKey[annotKeyFile][0].Author, "a note the diff does not show stays readable on the file")
	assert.True(t, m.remoteByKey[annotKeyFile][0].atFile)

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

func TestModel_FileLevelRemoteNotesOccupyTheFileBlock(t *testing.T) {
	m, _ := remoteNoteModel(t)

	assert.True(t, m.hasFileRow(), "a remote note on the file gives the file block its row")
	assert.False(t, m.hasFileAnnotation(), "the store still holds no file-level annotation of our own")

	rows := m.wrappedAnnotationLineCount(annotKeyFile)
	assert.Positive(t, rows, "the height query reserves rows for it")

	var b strings.Builder
	m.renderFileAnnotationHeader(&b, m.decorations())
	assert.Equal(t, rows, strings.Count(b.String(), "\n"), "the painter draws exactly what the height query counts")
	assert.Contains(t, b.String(), "dan", "the note is readable")
}

func TestRemoteNote_textSaysWhereItCameFrom(t *testing.T) {
	live := RemoteNote{File: "a.go", Line: 3, Author: "alice", Body: "looks off"}
	assert.Equal(t, "@alice: looks off", live.text())

	outdated := RemoteNote{File: "a.go", Author: "bob", Body: "needs a guard", Outdated: true, OrigLine: 73}
	assert.Equal(t, "(outdated a.go:73) @bob: needs a guard", outdated.text())

	renamed := RemoteNote{File: "new.go", Author: "carol", Body: "here", Outdated: true, OrigPath: "old.go", OrigLine: 4}
	assert.Equal(t, "(outdated old.go:4) @carol: here", renamed.text())

	detached := RemoteNote{File: "a.go", Line: 99, Author: "dan", Body: "outside", atFile: true}
	assert.Equal(t, "(a.go:99) @dan: outside", detached.text())
}

func TestModel_AnnotationRowsCarryNoCarriageReturn(t *testing.T) {
	m, _ := remoteNoteModel(t)
	rows := m.annotationVisualRows("x ", "one\r\ntwo\rthree")
	require.Len(t, rows, 3, "every line ending starts a row, whatever shape it arrived in")
	for _, r := range rows {
		assert.NotContains(t, r, "\r", "a bare CR would overwrite the row it was drawn on")
	}
}

// TestModel_FileBlockHeightMatchesPaintForMultilineNotes uses the shape that
// broke the pane: a request comment with CRLF endings and several lines. The
// height query and the painter must agree or every row below the block is
// drawn at the wrong offset.
func TestModel_FileBlockHeightMatchesPaintForMultilineNotes(t *testing.T) {
	body := "1. For long arrays of values, a YAML array should be used.\n" +
		"2. A name for an array of values must be used in the plural (s) 'routes:'\n" +
		"3. Values should be enclosed in quotes to indicate to Ansible that they are strings."
	m := testNewModel(t, plainRenderer(), annot.NewStore(), noopHighlighter(), ModelConfig{RemoteNotes: []RemoteNote{
		{File: "a.go", Author: "YouSysAdmin", Body: body, Outdated: true, OrigLine: 31},
	}})
	m.file.name = "a.go"
	m.file.lines = []git.DiffLine{{OldNum: 1, NewNum: 1, Content: "ctx", ChangeType: git.ChangeContext}}
	m.remoteByKey = m.indexRemoteNotes()

	require.Len(t, m.remoteByKey[annotKeyFile], 1, "a note with no line belongs to the file")

	var b strings.Builder
	m.renderFileAnnotationHeader(&b, m.decorations())
	painted := strings.Count(b.String(), "\n")
	assert.Equal(t, m.wrappedAnnotationLineCount(annotKeyFile), painted,
		"the rows the layout reserves are the rows the painter draws")
	assert.GreaterOrEqual(t, painted, 3, "each line of the comment gets its own row")
	assert.NotContains(t, b.String(), "\r")
}
