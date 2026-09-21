package forge

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testOldSHA = "13a83c27b42b9d0c128088bb922d7391da79918b"

func TestParseLineMap(t *testing.T) {
	t.Run("an empty diff maps every line onto itself", func(t *testing.T) {
		lm, err := parseLineMap("   \n")
		require.NoError(t, err)
		n, ok := lm.forward(42)
		assert.True(t, ok)
		assert.Equal(t, 42, n)
	})

	t.Run("a surviving line keeps its place through an insertion", func(t *testing.T) {
		raw := "--- a/a.go\n+++ b/a.go\n@@ -1,3 +1,5 @@\n+one\n+two\n ctx1\n ctx2\n keep\n"
		lm, err := parseLineMap(raw)
		require.NoError(t, err)
		for old, want := range map[int]int{1: 3, 2: 4, 3: 5} {
			n, ok := lm.forward(old)
			assert.True(t, ok, "old line %d survived", old)
			assert.Equal(t, want, n)
		}
	})

	t.Run("a rewritten line does not survive", func(t *testing.T) {
		raw := "--- a/a.go\n+++ b/a.go\n@@ -1,3 +1,3 @@\n ctx1\n-gone\n+fresh\n ctx3\n"
		lm, err := parseLineMap(raw)
		require.NoError(t, err)
		_, ok := lm.forward(2)
		assert.False(t, ok, "the line the author rewrote has no new position")
		n, ok := lm.forward(3)
		assert.True(t, ok)
		assert.Equal(t, 3, n)
	})
}

func TestReanchor(t *testing.T) {
	pr := PullRequest{HeadSHA: "head", Remote: "origin"}
	diff := "--- a/a.go\n+++ b/a.go\n@@ -1,3 +1,5 @@\n+one\n+two\n ctx1\n ctx2\n keep\n"

	t.Run("a current comment is untouched", func(t *testing.T) {
		f := &fakeRunner{}
		got := reanchor(t.Context(), f.run, "/repo", pr, []LineComment{{Path: "a.go", Line: 7, Side: "RIGHT"}})
		assert.Equal(t, []LineComment{{Path: "a.go", Line: 7, Side: "RIGHT"}}, got)
		assert.Empty(t, f.calls, "no git runs for a comment the service still anchors")
	})

	t.Run("a surviving line is placed on the head", func(t *testing.T) {
		f := &fakeRunner{answers: map[string]string{"git cat-file -e": "", "git diff": diff}}
		got := reanchor(t.Context(), f.run, "/repo", pr, []LineComment{
			{Path: "a.go", Side: "RIGHT", Outdated: true, OrigLine: 3, OrigSHA: testOldSHA},
		})
		assert.Equal(t, 5, got[0].Line)
	})

	t.Run("a line that did not survive becomes a file note", func(t *testing.T) {
		rewritten := "--- a/a.go\n+++ b/a.go\n@@ -1,3 +1,3 @@\n ctx1\n-gone\n+fresh\n ctx3\n"
		f := &fakeRunner{answers: map[string]string{"git cat-file -e": "", "git diff": rewritten}}
		got := reanchor(t.Context(), f.run, "/repo", pr, []LineComment{
			{Path: "a.go", Side: "RIGHT", Outdated: true, OrigLine: 2, OrigSHA: testOldSHA},
		})
		assert.Equal(t, 0, got[0].Line, "nowhere to put it, the file block keeps it")
		assert.True(t, got[0].Outdated)
		assert.Equal(t, 2, got[0].OrigLine, "it still says where it was written")
	})

	t.Run("a left-side comment keeps its original line", func(t *testing.T) {
		f := &fakeRunner{}
		got := reanchor(t.Context(), f.run, "/repo", pr, []LineComment{
			{Path: "a.go", Side: "LEFT", Outdated: true, OrigLine: 4, OrigSHA: testOldSHA},
		})
		assert.Equal(t, 4, got[0].Line, "the head diff cannot number the base, so it is passed through")
		assert.Empty(t, f.calls)
	})

	t.Run("a commit the clone cannot read is fetched once then given up on", func(t *testing.T) {
		f := &fakeRunner{fail: map[string]string{"git cat-file -e": "missing", "git fetch": "refused"}}
		got := reanchor(t.Context(), f.run, "/repo", pr, []LineComment{
			{Path: "a.go", Side: "RIGHT", Outdated: true, OrigLine: 3, OrigSHA: testOldSHA},
		})
		assert.Equal(t, 0, got[0].Line, "unreadable commit, the note falls to the file block")
		var fetches int
		for _, c := range f.calls {
			if len(c.args) > 0 && c.args[0] == "fetch" {
				fetches++
			}
		}
		assert.Equal(t, 1, fetches, "the fetch is tried once, not once per comment")
	})

	t.Run("one diff serves every comment on the same file and commit", func(t *testing.T) {
		f := &fakeRunner{answers: map[string]string{"git cat-file -e": "", "git diff": diff}}
		reanchor(t.Context(), f.run, "/repo", pr, []LineComment{
			{Path: "a.go", Side: "RIGHT", Outdated: true, OrigLine: 1, OrigSHA: testOldSHA},
			{Path: "a.go", Side: "RIGHT", Outdated: true, OrigLine: 2, OrigSHA: testOldSHA},
			{Path: "a.go", Side: "RIGHT", Outdated: true, OrigLine: 3, OrigSHA: testOldSHA},
		})
		var diffs int
		for _, c := range f.calls {
			if len(c.args) > 0 && c.args[0] == "diff" {
				diffs++
			}
		}
		assert.Equal(t, 1, diffs)
	})

	t.Run("an unparsable diff never fails the session", func(t *testing.T) {
		f := &fakeRunner{answers: map[string]string{"git cat-file -e": "", "git diff": "@@ not a patch"}}
		got := reanchor(t.Context(), f.run, "/repo", pr, []LineComment{
			{Path: "a.go", Side: "RIGHT", Outdated: true, OrigLine: 3, OrigSHA: testOldSHA},
		})
		require.Len(t, got, 1, "the comment is kept whatever git says")
	})
}

func TestIsFullHash(t *testing.T) {
	assert.True(t, isFullHash(testOldSHA))
	assert.False(t, isFullHash("13a83c2"), "a short hash cannot be fetched by object name")
	assert.False(t, isFullHash(strings.ToUpper(testOldSHA)))
	assert.False(t, isFullHash(""))
}

func TestNormalizeBody(t *testing.T) {
	assert.Equal(t, "one\ntwo\nthree", normalizeBody("one\r\ntwo\rthree"),
		"a bare carriage return would send the terminal cursor back over the line it just drew")
	assert.Equal(t, "trimmed", normalizeBody("  trimmed \r\n"))
	assert.Empty(t, normalizeBody("\r\n \r\n"))
}
