// Test fixtures and the Transform/Format/LineNumber scenarios are ported from
// lazygit's pkg/commands/patch/patch_test.go (MIT, Jesse Duffield). See NOTICE.

package patch

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const simpleDiff = `diff --git a/filename b/filename
index dcd3485..1ba5540 100644
--- a/filename
+++ b/filename
@@ -1,5 +1,5 @@
 apple
-orange
+grape
 ...
 ...
 ...
`

const renameWithModificationDiff = `diff --git a/oldname b/newname
similarity index 62%
rename from oldname
rename to newname
index dcd3485..1ba5540 100644
--- a/oldname
+++ b/newname
@@ -1,5 +1,5 @@
 apple
-orange
+grape
 ...
 ...
 ...
`

const addNewlineToEndOfFile = `diff --git a/filename b/filename
index 80a73f1..e48a11c 100644
--- a/filename
+++ b/filename
@@ -60,4 +60,4 @@ grape
 ...
 ...
 ...
-last line
\ No newline at end of file
+last line
`

const removeNewlinefromEndOfFile = `diff --git a/filename b/filename
index e48a11c..80a73f1 100644
--- a/filename
+++ b/filename
@@ -60,4 +60,4 @@ grape
 ...
 ...
 ...
-last line
+last line
\ No newline at end of file
`

const twoHunks = `diff --git a/filename b/filename
index e48a11c..b2ab81b 100644
--- a/filename
+++ b/filename
@@ -1,5 +1,5 @@
 apple
-grape
+orange
 ...
 ...
 ...
@@ -8,6 +8,8 @@ grape
 ...
 ...
 ...
+pear
+lemon
 ...
 ...
 ...
`

const twoHunksWithMoreAdditionsThanRemovals = `diff --git a/filename b/filename
index bac359d75..6e5b89f36 100644
--- a/filename
+++ b/filename
@@ -1,5 +1,6 @@
 apple
-grape
+orange
+kiwi
 ...
 ...
 ...
@@ -8,6 +9,8 @@ grape
 ...
 ...
 ...
+pear
+lemon
 ...
 ...
 ...
`

const twoChangesInOneHunk = `diff --git a/filename b/filename
index 9320895..6d79956 100644
--- a/filename
+++ b/filename
@@ -1,5 +1,5 @@
 apple
-grape
+kiwi
 orange
-pear
+banana
 lemon
`

const newFile = `diff --git a/newfile b/newfile
new file mode 100644
index 0000000..4e680cc
--- /dev/null
+++ b/newfile
@@ -0,0 +1,3 @@
+apple
+orange
+grape
`

const deletedFile = `diff --git a/newfile b/newfile
deleted file mode 100644
index 4e680cc1f..000000000
--- a/newfile
+++ /dev/null
@@ -1,3 +0,0 @@
-apple
-orange
-grape
`

const addNewlineToPreviouslyEmptyFile = `diff --git a/newfile b/newfile
index e69de29..c6568ea 100644
--- a/newfile
+++ b/newfile
@@ -0,0 +1 @@
+new line
\ No newline at end of file
`

const exampleHunk = `@@ -1,5 +1,5 @@
 apple
-grape
+orange
...
...
...
`

func TestTransform(t *testing.T) {
	type scenario struct {
		name           string
		filename       string
		diffText       string
		firstLineIndex int
		lastLineIndex  int
		reverse        bool
		stripRename    bool
		expected       string
	}

	scenarios := []scenario{
		{name: "nothing selected", filename: "filename", firstLineIndex: -1, lastLineIndex: -1, diffText: simpleDiff, expected: ""},
		{name: "only context selected", filename: "filename", firstLineIndex: 5, lastLineIndex: 5, diffText: simpleDiff, expected: ""},
		{name: "whole range selected", filename: "filename", firstLineIndex: 0, lastLineIndex: 11, diffText: simpleDiff, expected: `--- a/filename
+++ b/filename
@@ -1,5 +1,5 @@
 apple
-orange
+grape
 ...
 ...
 ...
`},
		{name: "only removal selected", filename: "filename", firstLineIndex: 6, lastLineIndex: 6, diffText: simpleDiff, expected: `--- a/filename
+++ b/filename
@@ -1,5 +1,4 @@
 apple
-orange
 ...
 ...
 ...
`},
		{name: "only addition selected", filename: "filename", firstLineIndex: 7, lastLineIndex: 7, diffText: simpleDiff, expected: `--- a/filename
+++ b/filename
@@ -1,5 +1,6 @@
 apple
+grape
 orange
 ...
 ...
 ...
`},
		{name: "range that extends beyond diff bounds", filename: "filename", firstLineIndex: -100, lastLineIndex: 100, diffText: simpleDiff, expected: `--- a/filename
+++ b/filename
@@ -1,5 +1,5 @@
 apple
-orange
+grape
 ...
 ...
 ...
`},
		{name: "add newline to end of file", filename: "filename", firstLineIndex: -100, lastLineIndex: 100, diffText: addNewlineToEndOfFile, expected: `--- a/filename
+++ b/filename
@@ -60,4 +60,4 @@ grape
 ...
 ...
 ...
-last line
\ No newline at end of file
+last line
`},
		{name: "add newline to end of file, reversed", filename: "filename", firstLineIndex: -100, lastLineIndex: 100, reverse: true, diffText: addNewlineToEndOfFile, expected: `--- a/filename
+++ b/filename
@@ -60,4 +60,4 @@ grape
 ...
 ...
 ...
-last line
\ No newline at end of file
+last line
`},
		{name: "remove newline from end of file", filename: "filename", firstLineIndex: -100, lastLineIndex: 100, diffText: removeNewlinefromEndOfFile, expected: `--- a/filename
+++ b/filename
@@ -60,4 +60,4 @@ grape
 ...
 ...
 ...
-last line
+last line
\ No newline at end of file
`},
		{name: "remove newline from end of file, reversed", filename: "filename", firstLineIndex: -100, lastLineIndex: 100, reverse: true, diffText: removeNewlinefromEndOfFile, expected: `--- a/filename
+++ b/filename
@@ -60,4 +60,4 @@ grape
 ...
 ...
 ...
-last line
+last line
\ No newline at end of file
`},
		{name: "remove newline from end of file, removal only", filename: "filename", firstLineIndex: 8, lastLineIndex: 8, diffText: removeNewlinefromEndOfFile, expected: `--- a/filename
+++ b/filename
@@ -60,4 +60,3 @@ grape
 ...
 ...
 ...
-last line
`},
		{name: "remove newline from end of file, removal only, reversed", filename: "filename", firstLineIndex: 8, lastLineIndex: 8, reverse: true, diffText: removeNewlinefromEndOfFile, expected: `--- a/filename
+++ b/filename
@@ -60,5 +60,4 @@ grape
 ...
 ...
 ...
-last line
 last line
\ No newline at end of file
`},
		{name: "remove newline from end of file, addition only", filename: "filename", firstLineIndex: 9, lastLineIndex: 9, diffText: removeNewlinefromEndOfFile, expected: `--- a/filename
+++ b/filename
@@ -60,4 +60,5 @@ grape
 ...
 ...
 ...
+last line
 last line
\ No newline at end of file
`},
		{name: "remove newline from end of file, addition only, reversed", filename: "filename", firstLineIndex: 9, lastLineIndex: 9, reverse: true, diffText: removeNewlinefromEndOfFile, expected: `--- a/filename
+++ b/filename
@@ -60,3 +60,4 @@ grape
 ...
 ...
 ...
+last line
\ No newline at end of file
`},
		{name: "staging two whole hunks", filename: "filename", firstLineIndex: -100, lastLineIndex: 100, diffText: twoHunks, expected: `--- a/filename
+++ b/filename
@@ -1,5 +1,5 @@
 apple
-grape
+orange
 ...
 ...
 ...
@@ -8,6 +8,8 @@ grape
 ...
 ...
 ...
+pear
+lemon
 ...
 ...
 ...
`},
		{name: "staging part of both hunks", filename: "filename", firstLineIndex: 7, lastLineIndex: 15, diffText: twoHunks, expected: `--- a/filename
+++ b/filename
@@ -1,5 +1,6 @@
 apple
+orange
 grape
 ...
 ...
 ...
@@ -8,6 +9,7 @@ grape
 ...
 ...
 ...
+pear
 ...
 ...
 ...
`},
		{name: "adding a new file", filename: "newfile", firstLineIndex: -100, lastLineIndex: 100, diffText: newFile, expected: `--- a/newfile
+++ b/newfile
@@ -0,0 +1,3 @@
+apple
+orange
+grape
`},
		{name: "adding part of a new file", filename: "newfile", firstLineIndex: 6, lastLineIndex: 7, diffText: newFile, expected: `--- a/newfile
+++ b/newfile
@@ -0,0 +1,2 @@
+apple
+orange
`},
		{name: "adding a new line to a previously empty file", filename: "newfile", firstLineIndex: -100, lastLineIndex: 100, diffText: addNewlineToPreviouslyEmptyFile, expected: `--- a/newfile
+++ b/newfile
@@ -0,0 +1 @@
+new line
\ No newline at end of file
`},
		{name: "adding a new line to a previously empty file, reversed", filename: "newfile", firstLineIndex: -100, lastLineIndex: 100, diffText: addNewlineToPreviouslyEmptyFile, reverse: true, expected: `--- a/newfile
+++ b/newfile
@@ -0,0 +1 @@
+new line
\ No newline at end of file
`},
		{name: "adding part of a hunk", filename: "filename", firstLineIndex: 6, lastLineIndex: 7, diffText: twoChangesInOneHunk, expected: `--- a/filename
+++ b/filename
@@ -1,5 +1,5 @@
 apple
-grape
+kiwi
 orange
 pear
 lemon
`},
		{name: "adding part of a hunk, reverse", filename: "filename", firstLineIndex: 6, lastLineIndex: 7, reverse: true, diffText: twoChangesInOneHunk, expected: `--- a/filename
+++ b/filename
@@ -1,5 +1,5 @@
 apple
-grape
+kiwi
 orange
 banana
 lemon
`},
		{name: "renamed file, whole change selected, strips the rename so only the content change is applied", firstLineIndex: 9, lastLineIndex: 10, stripRename: true, diffText: renameWithModificationDiff, expected: `diff --git a/newname b/newname
index dcd3485..1ba5540 100644
--- a/newname
+++ b/newname
@@ -1,5 +1,5 @@
 apple
-orange
+grape
 ...
 ...
 ...
`},
		{name: "renamed file, only removal selected, strips the rename", firstLineIndex: 9, lastLineIndex: 9, stripRename: true, diffText: renameWithModificationDiff, expected: `diff --git a/newname b/newname
index dcd3485..1ba5540 100644
--- a/newname
+++ b/newname
@@ -1,5 +1,4 @@
 apple
-orange
 ...
 ...
 ...
`},
	}

	for _, s := range scenarios {
		t.Run(s.name, func(t *testing.T) {
			p, err := Parse(s.diffText)
			require.NoError(t, err)
			result := p.Transform(TransformOpts{
				Reverse:             s.reverse,
				FileNameOverride:    s.filename,
				StripRename:         s.stripRename,
				IncludedLineIndices: ExpandRange(s.firstLineIndex, s.lastLineIndex),
			}).FormatPlain()
			assert.Equal(t, s.expected, result)
		})
	}
}

func TestTransform_nonContiguousSelection(t *testing.T) {
	// select the first deletion and the last addition of twoChangesInOneHunk (indices 6 and 10)
	p := MustParse(twoChangesInOneHunk)
	got := p.Transform(TransformOpts{FileNameOverride: "filename", IncludedLineIndices: []int{6, 10}}).FormatPlain()
	assert.Equal(t, `--- a/filename
+++ b/filename
@@ -1,5 +1,5 @@
 apple
-grape
 orange
+banana
 pear
 lemon
`, got)
}

func TestTransform_turnAddedFileIntoDiffAgainstEmptyFile(t *testing.T) {
	p := MustParse(newFile)
	got := p.Transform(TransformOpts{TurnAddedFilesIntoDiffAgainstEmptyFile: true, IncludedLineIndices: ExpandRange(0, 100)}).FormatPlain()
	assert.Equal(t, `diff --git a/newfile b/newfile
index 0000000..4e680cc
--- a/newfile
+++ b/newfile
@@ -0,0 +1,3 @@
+apple
+orange
+grape
`, got)
}

func TestTransform_preservesCRLFAndTrailingWhitespace(t *testing.T) {
	const crlf = "--- a/f\n+++ b/f\n@@ -1,3 +1,3 @@\n a\r\n-b  \r\n+B\t\r\n c\r\n"
	p := MustParse(crlf)
	got := p.Transform(TransformOpts{FileNameOverride: "f", IncludedLineIndices: ExpandRange(0, 100)}).FormatPlain()
	assert.Equal(t, crlf, got)
	partial := p.Transform(TransformOpts{FileNameOverride: "f", IncludedLineIndices: []int{5}}).FormatPlain()
	assert.Equal(t, "--- a/f\n+++ b/f\n@@ -1,3 +1,4 @@\n a\r\n+B\t\r\n b  \r\n c\r\n", partial)
}

func TestParseAndFormatPlain(t *testing.T) {
	scenarios := []struct {
		name     string
		patchStr string
	}{
		{"simpleDiff", simpleDiff},
		{"addNewlineToEndOfFile", addNewlineToEndOfFile},
		{"removeNewlinefromEndOfFile", removeNewlinefromEndOfFile},
		{"twoHunks", twoHunks},
		{"twoChangesInOneHunk", twoChangesInOneHunk},
		{"newFile", newFile},
		{"deletedFile", deletedFile},
		{"addNewlineToPreviouslyEmptyFile", addNewlineToPreviouslyEmptyFile},
		{"exampleHunk", exampleHunk},
	}
	for _, s := range scenarios {
		t.Run(s.name, func(t *testing.T) {
			// unified diffs may omit a length of 1 in the hunk header. the formatter
			// always omits the new length in that case, matching the fixtures.
			p, err := Parse(s.patchStr)
			require.NoError(t, err)
			assert.Equal(t, s.patchStr, p.FormatPlain())
		})
	}
}

func TestParse_headerVariantsAndErrors(t *testing.T) {
	p, err := Parse("--- a/f\n+++ b/f\n@@ -1 +1 @@\n-a\n+b\n")
	require.NoError(t, err)
	assert.Equal(t, 1, p.HunkCount())
	assert.Equal(t, "--- a/f\n+++ b/f\n@@ -1,1 +1 @@\n-a\n+b\n", p.FormatPlain(), "old length is always written")

	_, err = Parse("--- a/f\n+++ b/f\n@@ garbage @@\n-a\n")
	require.Error(t, err)
	assert.Panics(t, func() { MustParse("@@ nope") })

	empty, err := Parse("")
	require.NoError(t, err)
	assert.Equal(t, 0, empty.HunkCount())
	assert.False(t, empty.ContainsChanges())
	assert.Empty(t, empty.FormatPlain())
	assert.Equal(t, 0, empty.NextChangeIdx(3))
	assert.Equal(t, 1, empty.LineNumberOfLine(3))

	header, err := Parse("Binary files a/x and b/x differ\n")
	require.NoError(t, err)
	assert.Equal(t, 0, header.HunkCount())
	assert.Equal(t, 1, header.LineCount())
	assert.Empty(t, header.FormatPlain(), "no changes, nothing to apply")
}

func TestLineNumberOfLine(t *testing.T) {
	scenarios := []struct {
		name      string
		patchStr  string
		indexes   []int
		expecteds []int
	}{
		{
			name:      "twoHunks",
			patchStr:  twoHunks,
			indexes:   []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 1000},
			expecteds: []int{1, 1, 1, 1, 1, 1, 2, 2, 3, 4, 5, 8, 8, 9, 10, 11, 12, 13, 14, 15, 15, 15, 15, 15, 15},
		},
		{
			name:      "twoHunksWithMoreAdditionsThanRemovals",
			patchStr:  twoHunksWithMoreAdditionsThanRemovals,
			indexes:   []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 1000},
			expecteds: []int{1, 1, 1, 1, 1, 1, 2, 2, 3, 4, 5, 6, 9, 9, 10, 11, 12, 13, 14, 15, 16, 16, 16, 16, 16, 16},
		},
	}
	for _, s := range scenarios {
		t.Run(s.name, func(t *testing.T) {
			p := MustParse(s.patchStr)
			for i, idx := range s.indexes {
				assert.Equal(t, s.expecteds[i], p.LineNumberOfLine(idx), "idx %d", idx)
			}
		})
	}
}

func TestNextChangeIdx(t *testing.T) {
	p := MustParse(twoHunks)
	indexes := []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 1000}
	expecteds := []int{6, 6, 6, 6, 6, 6, 6, 7, 15, 15, 15, 15, 15, 15, 15, 15, 16, 16, 16, 16, 16, 16, 16, 16, 16}
	for i, idx := range indexes {
		assert.Equal(t, expecteds[i], p.NextChangeIdx(idx), "idx %d", idx)
	}
	assert.Equal(t, 6, p.NextChangeIdx(-5), "negative index clamps to the start")
}

func TestIsSingleHunkForWholeFile(t *testing.T) {
	scenarios := []struct {
		name     string
		patchStr string
		expected bool
	}{
		{"simpleDiff", simpleDiff, false},
		{"addNewlineToEndOfFile", addNewlineToEndOfFile, false},
		{"removeNewlinefromEndOfFile", removeNewlinefromEndOfFile, false},
		{"twoHunks", twoHunks, false},
		{"twoChangesInOneHunk", twoChangesInOneHunk, false},
		{"newFile", newFile, true},
		{"deletedFile", deletedFile, true},
		{"addNewlineToPreviouslyEmptyFile", addNewlineToPreviouslyEmptyFile, true},
		{"exampleHunk", exampleHunk, false},
	}
	for _, s := range scenarios {
		t.Run(s.name, func(t *testing.T) {
			assert.Equal(t, s.expected, MustParse(s.patchStr).IsSingleHunkForWholeFile())
		})
	}
}

func TestHunkIndexHelpers(t *testing.T) {
	p := MustParse(twoHunks)
	assert.Equal(t, 2, p.HunkCount())
	assert.Equal(t, 20, p.LineCount())
	assert.Len(t, p.Lines(), 20)
	assert.Equal(t, 4, p.HunkStartIdx(0))
	assert.Equal(t, 10, p.HunkEndIdx(0))
	assert.Equal(t, 11, p.HunkStartIdx(1))
	assert.Equal(t, 19, p.HunkEndIdx(1))
	assert.Equal(t, 11, p.HunkStartIdx(99), "index clamps to the last hunk")
	assert.Equal(t, 4, p.HunkStartIdx(-1))
	assert.Equal(t, -1, p.HunkContainingLine(2))
	assert.Equal(t, 0, p.HunkContainingLine(6))
	assert.Equal(t, 1, p.HunkContainingLine(19))
	assert.Equal(t, -1, p.HunkContainingLine(20))
	assert.Equal(t, 8, p.HunkOldStartForLine(15))
	assert.Equal(t, 0, p.HunkOldStartForLine(1))
	assert.Equal(t, " apple\n-grape\n+orange\n", p.FormatRangePlain(5, 7))
	assert.Equal(t, "diff --git a/filename b/filename\n", p.FormatRangePlain(-3, 0))
	assert.Equal(t, " ...\n", p.FormatRangePlain(19, 99))
	assert.Empty(t, p.FormatRangePlain(9, 8))
}

func TestExpandRange(t *testing.T) {
	assert.Equal(t, []int{2, 3, 4}, ExpandRange(2, 4))
	assert.Equal(t, []int{7}, ExpandRange(7, 7))
	assert.Empty(t, ExpandRange(5, 4))
}

func TestLineKindHelpers(t *testing.T) {
	add := &Line{Kind: KindAddition, Content: "+x"}
	del := &Line{Kind: KindDeletion, Content: "-x"}
	ctx := &Line{Kind: KindContext, Content: " x"}
	assert.True(t, add.IsChange())
	assert.True(t, add.IsAddition())
	assert.False(t, add.IsDeletion())
	assert.True(t, del.IsDeletion())
	assert.False(t, ctx.IsChange())
	assert.Equal(t, 2, countKinds([]*Line{add, del, ctx}, KindAddition, KindDeletion))
}
