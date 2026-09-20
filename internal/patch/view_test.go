package patch

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestViewLines_numbering(t *testing.T) {
	p := MustParse(twoHunksWithMoreAdditionsThanRemovals)
	vl := p.ViewLines()
	require.Len(t, vl, p.LineCount())

	// header rows carry no numbers and no hunk
	for i := range 4 {
		assert.Equal(t, KindHeader, vl[i].Kind)
		assert.Equal(t, -1, vl[i].HunkIdx)
		assert.Equal(t, i, vl[i].PatchIdx)
	}
	assert.Equal(t, KindHunkHeader, vl[4].Kind)
	assert.Equal(t, "@@ -1,5 +1,6 @@", vl[4].Content)
	assert.Equal(t, 0, vl[4].HunkIdx)

	// body: " apple" is old 1 / new 1, "-grape" old 2, "+orange" new 2, "+kiwi" new 3, " ..." old 3 / new 4
	assert.Equal(t, ViewLine{PatchIdx: 5, Kind: KindContext, OldNum: 1, NewNum: 1, Content: "apple", HunkIdx: 0}, vl[5])
	assert.Equal(t, ViewLine{PatchIdx: 6, Kind: KindDeletion, OldNum: 2, Content: "grape", HunkIdx: 0}, vl[6])
	assert.Equal(t, ViewLine{PatchIdx: 7, Kind: KindAddition, NewNum: 2, Content: "orange", HunkIdx: 0}, vl[7])
	assert.Equal(t, ViewLine{PatchIdx: 8, Kind: KindAddition, NewNum: 3, Content: "kiwi", HunkIdx: 0}, vl[8])
	assert.Equal(t, ViewLine{PatchIdx: 9, Kind: KindContext, OldNum: 3, NewNum: 4, Content: "...", HunkIdx: 0}, vl[9])

	// second hunk restarts numbering from its header
	assert.Equal(t, KindHunkHeader, vl[12].Kind)
	assert.Equal(t, 1, vl[12].HunkIdx)
	assert.Equal(t, ViewLine{PatchIdx: 13, Kind: KindContext, OldNum: 8, NewNum: 9, Content: "...", HunkIdx: 1}, vl[13])
	assert.Equal(t, ViewLine{PatchIdx: 16, Kind: KindAddition, NewNum: 12, Content: "pear", HunkIdx: 1}, vl[16])

	// every view row maps back to the same patch line
	lines := p.Lines()
	for _, v := range vl {
		assert.Equal(t, lines[v.PatchIdx].Kind, v.Kind)
	}
}

func TestViewLines_noNewlineMarkerAndEmptyBody(t *testing.T) {
	vl := MustParse(removeNewlinefromEndOfFile).ViewLines()
	last := vl[len(vl)-1]
	assert.Equal(t, KindNoNewline, last.Kind)
	assert.Equal(t, 0, last.OldNum)
	assert.Equal(t, 0, last.NewNum)
	assert.Equal(t, " No newline at end of file", last.Content)

	p, err := Parse("--- a/f\n+++ b/f\n@@ -1,2 +1,2 @@\n a\n\n")
	require.NoError(t, err)
	body := p.ViewLines()
	assert.Empty(t, body[len(body)-1].Content, "an empty body line has no marker to strip")
}

func TestViewLines_selectionRoundTrip(t *testing.T) {
	// pick the display rows for "+pear" and "+lemon" and stage exactly those via PatchIdx
	p := MustParse(twoHunks)
	var idxs []int
	for _, v := range p.ViewLines() {
		if v.Kind == KindAddition && (v.Content == "pear" || v.Content == "lemon") {
			idxs = append(idxs, v.PatchIdx)
		}
	}
	got := p.Transform(TransformOpts{FileNameOverride: "filename", IncludedLineIndices: idxs}).FormatPlain()
	assert.Equal(t, `--- a/filename
+++ b/filename
@@ -8,6 +8,8 @@ grape
 ...
 ...
 ...
+pear
+lemon
 ...
 ...
 ...
`, got)
}
