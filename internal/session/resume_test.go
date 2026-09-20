package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func entryByScope(t *testing.T, entries []Entry, scope string) Entry {
	t.Helper()
	for _, e := range entries {
		if e.Scope == scope {
			return e
		}
	}
	t.Fatalf("no entry with scope %q", scope)
	return Entry{}
}

func TestSaveAndList_OneEntryPerScope(t *testing.T) {
	svc := New(t.TempDir())
	svc.Save(Params{Path: "/repo/proj", Ref: "main..feature", Annotations: "## a.go:3 (+)\nfirst\n\n## b.go:1 (-)\nsecond\n"})
	svc.Save(Params{Path: "/repo/proj", PR: 12, Annotations: "## c.go:9 (+)\ndraft\n"})
	svc.Save(Params{Path: "/repo/proj", Annotations: "## d.go:1 (+)\nwt\n"})
	svc.Save(Params{Path: "/repo/proj", Staged: true, Annotations: "## d.go:1 (+)\nstaged\n"})

	entries, err := svc.List(Params{Path: "/repo/proj"})
	require.NoError(t, err)
	require.Len(t, entries, 4)

	ref := entryByScope(t, entries, "ref-main..feature")
	assert.Equal(t, "main..feature", ref.Ref)
	assert.Equal(t, "/repo/proj", ref.Path)
	require.Len(t, ref.Records, 2)
	assert.Equal(t, "a.go", ref.Records[0].File)
	assert.Equal(t, 3, ref.Records[0].Line)
	assert.Equal(t, "second", ref.Records[1].Comment)
	assert.Contains(t, ref.Label(), "main..feature  2 annotations")
	assert.False(t, ref.Time.IsZero(), "time comes from the title line")

	pr := entryByScope(t, entries, "pr-12")
	assert.Equal(t, 12, pr.PR)
	assert.Equal(t, "draft", pr.Records[0].Comment)
	assert.Contains(t, pr.Label(), "PR #12  1 annotation")

	assert.Equal(t, "wt", entryByScope(t, entries, "worktree").Records[0].Comment)
	assert.Equal(t, "staged", entryByScope(t, entries, "worktree-staged").Records[0].Comment)
}

func TestSave_RewritesOnlyWhenAnnotationsChange(t *testing.T) {
	svc := New(t.TempDir())
	p := Params{Path: "/repo/proj", Annotations: "## a.go:1 (+)\nfirst\n"}
	svc.Save(p)
	file := filepath.Join(svc.baseDir, "proj", "worktree.md")
	old := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	require.NoError(t, os.Chtimes(file, old, old))

	svc.Save(p) // same annotations: untouched
	info, err := os.Stat(file)
	require.NoError(t, err)
	assert.True(t, info.ModTime().Equal(old), "unchanged annotations must not rewrite the entry")

	p.Annotations = "## a.go:1 (+)\nfirst\n\n## a.go:2 (+)\nmore\n"
	svc.Save(p) // changed: rewritten in place, still one file
	info, err = os.Stat(file)
	require.NoError(t, err)
	assert.False(t, info.ModTime().Equal(old))
	entries, err := svc.List(Params{Path: "/repo/proj"})
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Len(t, entries[0].Records, 2)

	// a session that ends without annotations clears the scope
	svc.Save(Params{Path: "/repo/proj"})
	assert.NoFileExists(t, file)
	svc.Save(Params{Path: "/repo/proj"}) // removing twice is fine
}

func TestSave_PrunesOldestBeyondMaxEntries(t *testing.T) {
	svc := New(t.TempDir())
	svc.MaxEntries = 2
	dir := filepath.Join(svc.baseDir, "proj")
	svc.Save(Params{Path: "/repo/proj", Ref: "a..b", Annotations: "## x:1 (+)\n1\n"})
	require.NoError(t, os.Chtimes(filepath.Join(dir, "ref-a..b.md"), time.Unix(1000, 0), time.Unix(1000, 0)))
	svc.Save(Params{Path: "/repo/proj", Ref: "c..d", Annotations: "## x:1 (+)\n2\n"})
	require.NoError(t, os.Chtimes(filepath.Join(dir, "ref-c..d.md"), time.Unix(2000, 0), time.Unix(2000, 0)))
	svc.Save(Params{Path: "/repo/proj", PR: 3, Annotations: "## x:1 (+)\n3\n"})

	names, err := filepath.Glob(filepath.Join(dir, "*.md"))
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{filepath.Join(dir, "ref-c..d.md"), filepath.Join(dir, "pr-3.md")}, names, "the oldest entry goes, the new one stays")
}

func TestScopeName(t *testing.T) {
	assert.Equal(t, "worktree", ScopeName(Params{}))
	assert.Equal(t, "worktree-staged", ScopeName(Params{Staged: true}))
	assert.Equal(t, "pr-42", ScopeName(Params{PR: 42, Ref: "abc..def"}))
	assert.Equal(t, "ref-main..feature", ScopeName(Params{Ref: "main..feature"}))
	assert.Equal(t, "ref-origin_main..feat_x_y", ScopeName(Params{Ref: "origin/main..feat/x y"}))
	assert.Equal(t, "ref-HEAD~3", ScopeName(Params{Ref: "HEAD~3"}))
	long := ScopeName(Params{Ref: string(make([]byte, 200))})
	assert.LessOrEqual(t, len(long), len("ref-")+80)
}

func TestList_MissingDirectoryAndBrokenFiles(t *testing.T) {
	svc := New(t.TempDir())
	entries, err := svc.List(Params{Path: "/repo/none"})
	require.NoError(t, err)
	assert.Empty(t, entries)

	dir := filepath.Join(svc.baseDir, "proj")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "broken.md"), []byte("garbage without a section\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("ignored\n"), 0o600))
	svc.Save(Params{Path: "/repo/proj", Annotations: "## a.go:1 (+)\nok\n"})
	entries, err = svc.List(Params{Path: "/repo/proj"})
	require.NoError(t, err)
	assert.Len(t, entries, 1, "unreadable entries are skipped")
}

func TestReadEntry_StopsAtTheDiffSnapshot(t *testing.T) {
	file := filepath.Join(t.TempDir(), "ref-master.md")
	content := "# Review: 2026-09-19 11:46:05\npath: /r\nrefs: master\ncommit: f2b0cc6\n\n## Annotations\n\n## x.md:14 (+)\nnote\n\n---\n\n## Diff\n\ndiff --git a/x.md b/x.md\n## Heading:1 (+)\nlooks like a record but is diff\n"
	require.NoError(t, os.WriteFile(file, []byte(content), 0o600))
	e, err := ReadEntry(file)
	require.NoError(t, err)
	assert.Equal(t, "ref-master", e.Scope)
	assert.Equal(t, "master", e.Ref)
	assert.Equal(t, "f2b0cc6", e.Commit)
	assert.Equal(t, "/r", e.Path)
	assert.Equal(t, time.Date(2026, 9, 19, 11, 46, 5, 0, time.Local), e.Time)
	require.Len(t, e.Records, 1)
	assert.Equal(t, "note", e.Records[0].Comment)
	assert.Equal(t, "## x.md:14 (+)\nnote\n", e.annotations)
}

func TestEntry_LabelWorkingTree(t *testing.T) {
	e := Entry{Time: time.Date(2026, 9, 19, 11, 46, 0, 0, time.Local)}
	assert.Equal(t, "2026-09-19 11:46  working tree  0 annotations", e.Label())
}

func TestHeadCommit(t *testing.T) {
	assert.Empty(t, HeadCommit(""))
	assert.Empty(t, HeadCommit(t.TempDir()), "not a repository")
}
