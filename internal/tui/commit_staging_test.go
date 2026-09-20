package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yousysadmin/igit/internal/annot"
	"github.com/yousysadmin/igit/internal/git"
	"github.com/yousysadmin/igit/internal/gitops"
)

func TestCommitModel_AnnotationsOnlyOnWorktreeSide(t *testing.T) {
	c := newTestCommit(t, newRepoMock(sampleStatus()))
	c.diff.store.Add(annot.Annotation{File: "a.go", Line: 2, Type: "+", Comment: "review note"})

	// unstaged worktree diff: line numbers match the review numbering
	c.installRaw(gitops.DiffSpec{Path: "a.go"}, sampleRaw)
	assert.False(t, c.diff.host.annotationsHidden)
	assert.Contains(t, c.diff.View(), "review note")

	// staged side: index line numbers, annotations must not be drawn
	c.installRaw(gitops.DiffSpec{Path: "a.go", Cached: true}, sampleRaw)
	assert.True(t, c.diff.host.annotationsHidden)
	assert.NotContains(t, c.diff.View(), "review note")
	assert.False(t, c.diff.hasAnnotation(c.diff.paneLines()[3]))

	// a historical diff (log / stash) hides them too. clearing resets
	c.handleRefDiff(commitRefDiffMsg{seq: c.hist.diffSeq, path: "a.go", lines: c.diff.paneLines()})
	assert.True(t, c.diff.host.annotationsHidden)
	c.clearDiff()
	assert.False(t, c.diff.host.annotationsHidden)
}

func TestCommitModel_StartAtChangeAppliesToStagingDiff(t *testing.T) {
	cfg := testCommitConfig()
	cfg.Repo = newRepoMock(sampleStatus())
	cfg.DiffPane.StartAtChange = true
	c, err := NewCommitModel(*cfg)
	require.NoError(t, err)
	c.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	c.installRaw(gitops.DiffSpec{Path: "a.go"}, sampleRaw)
	assert.Equal(t, 2, c.diff.paneCursor(), "cursor starts on the first changed row")
}

const multiFilePatch = `diff --git a/a.go b/a.go
index 111..222 100644
--- a/a.go
+++ b/a.go
@@ -1,3 +1,3 @@
 one
-two
+TWO
diff --git a/old.txt b/new.txt
similarity index 90%
rename from old.txt
rename to new.txt
--- a/old.txt
+++ b/new.txt
@@ -1,2 +1,2 @@
 keep
-- dash line
+plus
diff --git a/gone.md b/gone.md
deleted file mode 100644
--- a/gone.md
+++ /dev/null
@@ -1,2 +0,0 @@
-bye
-now
diff --git a/logo.png b/logo.png
index 333..444 100644
Binary files a/logo.png and b/logo.png differ
`

func TestSplitPatchFiles(t *testing.T) {
	files := splitPatchFiles(multiFilePatch)
	require.Len(t, files, 4)

	assert.Equal(t, "a.go", files[0].path)
	assert.Equal(t, "a.go", files[0].display)
	assert.Contains(t, files[0].raw, "+TWO")
	assert.NotContains(t, files[0].raw, "rename from", "a section stops at the next file")

	assert.Equal(t, "new.txt", files[1].path)
	assert.Equal(t, "old.txt -> new.txt", files[1].display)
	assert.Contains(t, files[1].raw, "-- dash line", "a body line starting with -- is not read as a path")

	assert.Equal(t, "gone.md", files[2].path)
	assert.Equal(t, "gone.md (deleted)", files[2].display)

	assert.Equal(t, "logo.png", files[3].path, "a binary section takes its path from the git header")
	assert.Equal(t, "logo.png", files[3].display)

	assert.Empty(t, splitPatchFiles(""))
	assert.Empty(t, splitPatchFiles("   \n"))
	assert.Empty(t, splitPatchFiles("not a patch\n"))
}

func TestCommitModel_EntryDiffLines(t *testing.T) {
	c := newTestCommit(t, newRepoMock(sampleStatus()))
	lines, highlighted := c.entryDiffLines(splitPatchFiles(multiFilePatch))
	require.Len(t, highlighted, len(lines))

	var dividers []string
	for _, l := range lines {
		if l.ChangeType == git.ChangeDivider {
			dividers = append(dividers, l.Content)
		}
	}
	assert.Equal(t, []string{
		"a.go", "@@ -1,2 +1,2 @@",
		"", "old.txt -> new.txt", "@@ -1,2 +1,2 @@",
		"", "gone.md (deleted)", "@@ -1,2 +0,0 @@",
		"", "logo.png",
	}, dividers, "every file opens with a blank row and its own divider, hunk headers follow")

	var headers []string
	for _, l := range lines {
		if l.IsFileHeader {
			headers = append(headers, l.Content)
		}
	}
	assert.Equal(t, []string{"a.go", "old.txt -> new.txt", "gone.md (deleted)", "logo.png"}, headers, "only the file names are marked as headers")

	var content []string
	for _, l := range lines {
		if l.ChangeType != git.ChangeDivider {
			content = append(content, string(l.ChangeType)+l.Content)
		}
	}
	assert.Contains(t, content, "+TWO")
	assert.Contains(t, content, "-- dash line")
	assert.Contains(t, content, "-bye")
	assert.Contains(t, content, " (binary file)")
}

func TestCommitModel_EntryDiffLinesTruncates(t *testing.T) {
	c := newTestCommit(t, newRepoMock(sampleStatus()))
	var big strings.Builder
	for i := range 40 {
		fmt.Fprintf(&big, "diff --git a/f%d.go b/f%d.go\n--- a/f%d.go\n+++ b/f%d.go\n@@ -1,1 +1,1 @@\n", i, i, i, i)
		for j := range 600 {
			fmt.Fprintf(&big, "+line %d\n", j)
		}
	}
	lines, highlighted := c.entryDiffLines(splitPatchFiles(big.String()))
	require.Len(t, highlighted, len(lines))
	assert.Less(t, len(lines), maxEntryDiffLines+700, "the walk stops once the cap is passed")
	assert.Contains(t, lines[len(lines)-1].Content, "more files, press enter")
}
