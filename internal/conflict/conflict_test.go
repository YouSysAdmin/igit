package conflict

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const mergeStyle = `one
<<<<<<< HEAD
OURS-2
=======
THEIRS-2
>>>>>>> side
three
<<<<<<< HEAD
OURS-4
=======
THEIRS-4
>>>>>>> side
five
`

const diff3Style = `one
<<<<<<< HEAD
OURS-2
||||||| e5f9d54
two
=======
THEIRS-2
>>>>>>> side
three
`

func TestParse_mergeStyle(t *testing.T) {
	got := Parse(mergeStyle)
	require.Len(t, got, 2, "both blocks are found")

	first := got[0]
	assert.Equal(t, 2, first.Start, "the opening marker line")
	assert.Equal(t, 6, first.End, "the closing marker line")
	assert.Equal(t, "HEAD", first.OursLabel)
	assert.Equal(t, "side", first.TheirsLabel)
	assert.Equal(t, []string{"OURS-2"}, first.Ours)
	assert.Equal(t, []string{"THEIRS-2"}, first.Theirs)
	assert.False(t, first.HasBase, "the merge style carries no base")

	assert.Equal(t, 8, got[1].Start)
	assert.Equal(t, 12, got[1].End)
}

func TestParse_diff3Style(t *testing.T) {
	got := Parse(diff3Style)
	require.Len(t, got, 1)
	r := got[0]
	assert.True(t, r.HasBase)
	assert.Equal(t, "e5f9d54", r.BaseLabel)
	assert.Equal(t, []string{"two"}, r.Base)
	assert.Equal(t, []string{"OURS-2"}, r.Ours, "the base section does not leak into ours")
	assert.Equal(t, []string{"THEIRS-2"}, r.Theirs)
}

func TestParse_toleratesBrokenBlocks(t *testing.T) {
	assert.Empty(t, Parse("no conflict here\n"))
	assert.Empty(t, Parse("<<<<<<< HEAD\nours\n=======\ntheirs\n"), "a block that never closes is not reported")
	assert.Empty(t, Parse("<<<<<<< HEAD\nours\n<<<<<<< HEAD\n"), "a block reopening inside itself is not reported")
	assert.Empty(t, Parse("<<<<<<<<< not a marker\n=======\n>>>>>>> x\n"), "nine angle brackets are text")

	// a broken block does not hide a good one after it
	got := Parse("<<<<<<< HEAD\nstray\n" + mergeStyle)
	assert.Len(t, got, 2)
}

func TestParse_markerWithoutLabel(t *testing.T) {
	got := Parse("<<<<<<<\nours\n=======\ntheirs\n>>>>>>>\n")
	require.Len(t, got, 1)
	assert.Empty(t, got[0].OursLabel)
	assert.Equal(t, []string{"ours"}, got[0].Ours)
}

func TestRegion_Lines(t *testing.T) {
	r := Parse(mergeStyle)[0]
	assert.Equal(t, []string{"OURS-2"}, r.Lines(Ours))
	assert.Equal(t, []string{"THEIRS-2"}, r.Lines(Theirs))
	assert.Equal(t, []string{"OURS-2", "THEIRS-2"}, r.Lines(Both), "both sides, ours first")
	assert.Nil(t, r.Lines(Choice("nonsense")))
}

func TestAt(t *testing.T) {
	regions := Parse(mergeStyle)
	for _, line := range []int{2, 3, 4, 5, 6} {
		_, idx, ok := At(regions, line)
		require.True(t, ok, "line %d sits in the first block", line)
		assert.Equal(t, 0, idx)
	}
	_, idx, ok := At(regions, 9)
	require.True(t, ok)
	assert.Equal(t, 1, idx, "line 9 sits in the second block")

	_, _, ok = At(regions, 1)
	assert.False(t, ok, "a line outside every block")
	_, _, ok = At(regions, 7)
	assert.False(t, ok)
}

func TestResolve(t *testing.T) {
	// resolving one block leaves the other alone
	got, err := Resolve(mergeStyle, 0, Ours)
	require.NoError(t, err)
	assert.Equal(t, "one\nOURS-2\nthree\n<<<<<<< HEAD\nOURS-4\n=======\nTHEIRS-4\n>>>>>>> side\nfive\n", got)

	got, err = Resolve(mergeStyle, 1, Theirs)
	require.NoError(t, err)
	assert.Contains(t, got, "THEIRS-4\nfive\n")
	assert.Contains(t, got, "<<<<<<< HEAD\nOURS-2", "the first block is untouched")

	got, err = Resolve(mergeStyle, 0, Both)
	require.NoError(t, err)
	assert.Equal(t, "one\nOURS-2\nTHEIRS-2\nthree\n", strings.Split(got, "<<<<<<<")[0])

	// the base section is dropped whichever side wins
	got, err = Resolve(diff3Style, 0, Theirs)
	require.NoError(t, err)
	assert.Equal(t, "one\nTHEIRS-2\nthree\n", got)

	_, err = Resolve(mergeStyle, 5, Ours)
	require.ErrorContains(t, err, "is not in the file")
	_, err = Resolve(mergeStyle, -1, Ours)
	require.ErrorContains(t, err, "is not in the file")
}

func TestResolve_keepsLineEndingsAndTrailer(t *testing.T) {
	crlf := "one\r\n<<<<<<< HEAD\r\nOURS\r\n=======\r\nTHEIRS\r\n>>>>>>> side\r\nlast\r\n"
	got, err := Resolve(crlf, 0, Ours)
	require.NoError(t, err)
	assert.Equal(t, "one\r\nOURS\r\nlast\r\n", got, "CRLF survives")

	noTrailer := "<<<<<<< HEAD\nOURS\n=======\nTHEIRS\n>>>>>>> side"
	got, err = Resolve(noTrailer, 0, Theirs)
	require.NoError(t, err)
	assert.Equal(t, "THEIRS", got, "a file without a final newline keeps none")
}
