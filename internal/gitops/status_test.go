package gitops

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// z joins porcelain records with the NUL terminator `git status -z` emits.
func z(records ...string) string { return strings.Join(records, "\x00") + "\x00" }

func TestParseStatusV2_headerAndOrdinary(t *testing.T) {
	out := z(
		"# branch.oid 0123456789abcdef0123456789abcdef01234567",
		"# branch.head main",
		"# branch.upstream origin/main",
		"# branch.ab +2 -1",
		"# stash 3",
		"1 .M N... 100644 100644 100644 aaaa bbbb app/a.go",
		"1 M. N... 100644 100644 100644 aaaa bbbb app/b.go",
		"1 MM N... 100644 100644 100644 aaaa bbbb app/c with space.go",
		"1 A. N... 000000 100644 100644 0000 bbbb new.txt",
		"1 .D N... 100644 100644 000000 aaaa aaaa gone.txt",
		"1 .M S.M. 160000 160000 160000 aaaa aaaa sub",
		"? untracked.txt",
		"! ignored.log",
	)
	st, err := parseStatusV2(out)
	require.NoError(t, err)

	assert.Equal(t, BranchHead{Name: "main", OID: "0123456789abcdef0123456789abcdef01234567", Upstream: "origin/main", Ahead: 2, Behind: 1}, st.Head)
	assert.Equal(t, 3, st.StashCount)
	require.Len(t, st.Files, 8)

	a := st.Files[0]
	assert.Equal(t, "app/a.go", a.Path)
	assert.Equal(t, Unmodified, a.Index)
	assert.Equal(t, ChangeCode('M'), a.Worktree)
	assert.False(t, a.HasStaged())
	assert.True(t, a.HasUnstaged())
	assert.Equal(t, " M", a.ShortStatus())

	b := st.Files[1]
	assert.True(t, b.HasStaged())
	assert.False(t, b.HasUnstaged())
	assert.Equal(t, "M ", b.ShortStatus())

	c := st.Files[2]
	assert.Equal(t, "app/c with space.go", c.Path)
	assert.True(t, c.HasStaged())
	assert.True(t, c.HasUnstaged())

	added := st.Files[3]
	assert.True(t, added.IsNewInIndex())
	assert.Equal(t, "A ", added.ShortStatus())

	assert.Equal(t, " D", st.Files[4].ShortStatus())
	assert.True(t, st.Files[5].Submodule)

	u := st.Files[6]
	assert.True(t, u.Untracked)
	assert.Equal(t, "??", u.ShortStatus())
	assert.False(t, u.HasStaged())
	assert.True(t, u.HasUnstaged())

	ig := st.Files[7]
	assert.True(t, ig.Ignored)
	assert.Equal(t, "!!", ig.ShortStatus())
	assert.False(t, ig.HasStaged())
	assert.False(t, ig.HasUnstaged(), "ignored files are not actionable")

	staged := st.Staged()
	require.Len(t, staged, 3)
	assert.Equal(t, []string{"app/b.go", "app/c with space.go", "new.txt"}, paths(staged))
	unstaged := st.Unstaged()
	assert.Equal(t, []string{"app/a.go", "app/c with space.go", "gone.txt", "sub", "untracked.txt"}, paths(unstaged))
	assert.Empty(t, st.Conflicted())
}

func paths(fs []StatusEntry) []string {
	out := make([]string, 0, len(fs))
	for _, f := range fs {
		out = append(out, f.Path)
	}
	return out
}

func TestParseStatusV2_renameAndCopy(t *testing.T) {
	out := z(
		"# branch.oid abc",
		"# branch.head dev",
		"2 R. N... 100644 100644 100644 aaaa aaaa R100 new/name.go",
		"old/name.go",
		"2 .C N... 100644 100644 100644 aaaa aaaa C75 copy.go",
		"orig.go",
		"1 .M N... 100644 100644 100644 aaaa bbbb after.go",
	)
	st, err := parseStatusV2(out)
	require.NoError(t, err)
	require.Len(t, st.Files, 3)
	r := st.Files[0]
	assert.Equal(t, "new/name.go", r.Path)
	assert.Equal(t, "old/name.go", r.OrigPath)
	assert.Equal(t, 100, r.RenameScore)
	assert.Equal(t, ChangeCode('R'), r.Index)
	assert.True(t, r.HasStaged())
	assert.Equal(t, "R ", r.ShortStatus())
	c := st.Files[1]
	assert.Equal(t, "orig.go", c.OrigPath)
	assert.Equal(t, 75, c.RenameScore)
	assert.Equal(t, "after.go", st.Files[2].Path, "record after a rename is not swallowed")
}

func TestParseStatusV2_conflicts(t *testing.T) {
	out := z(
		"# branch.oid abc",
		"# branch.head main",
		"u UU N... 100644 100644 100644 100644 h1 h2 h3 both.go",
		"u AA N... 000000 100644 100644 100644 h1 h2 h3 added-both.go",
		"u DU N... 100644 000000 100644 100644 h1 h2 h3 deleted-by-us.go",
	)
	st, err := parseStatusV2(out)
	require.NoError(t, err)
	require.Len(t, st.Files, 3)
	for _, f := range st.Files {
		assert.True(t, f.Conflict)
		assert.False(t, f.HasStaged())
		assert.True(t, f.HasUnstaged())
	}
	assert.Equal(t, "UU", st.Files[0].ShortStatus())
	assert.Equal(t, "AA", st.Files[1].ConflictXY)
	assert.Equal(t, "DU", st.Files[2].ShortStatus())
	assert.Equal(t, []string{"added-both.go", "both.go", "deleted-by-us.go"}, paths(st.Conflicted()))
	assert.Empty(t, st.Unstaged(), "conflicts are reported in their own section")
	assert.Empty(t, st.Staged())
}

func TestParseStatusV2_detachedInitialAndGone(t *testing.T) {
	st, err := parseStatusV2(z("# branch.oid (initial)", "# branch.head main"))
	require.NoError(t, err)
	assert.True(t, st.Head.Unborn)
	assert.Empty(t, st.Head.OID)
	assert.Equal(t, "main", st.Head.Name)

	st, err = parseStatusV2(z("# branch.oid abc", "# branch.head (detached)"))
	require.NoError(t, err)
	assert.True(t, st.Head.Detached)
	assert.Empty(t, st.Head.Name)

	st, err = parseStatusV2(z("# branch.oid abc", "# branch.head feat", "# branch.upstream origin/feat"))
	require.NoError(t, err)
	assert.Equal(t, "origin/feat", st.Head.Upstream)
	assert.True(t, st.Head.UpstreamGone, "no branch.ab line means the upstream ref is gone")

	st, err = parseStatusV2(z("# branch.oid abc", "# branch.head feat", "# branch.upstream origin/feat", "# branch.ab +0 -0"))
	require.NoError(t, err)
	assert.False(t, st.Head.UpstreamGone)
}

func TestFileStatus_zeroValueIsUnmodified(t *testing.T) {
	var f StatusEntry
	assert.False(t, f.HasStaged())
	assert.False(t, f.HasUnstaged())
	assert.Equal(t, "  ", f.ShortStatus())
}

func TestParseStatusV2_emptyAndMalformed(t *testing.T) {
	st, err := parseStatusV2("")
	require.NoError(t, err)
	assert.Empty(t, st.Files)

	_, err = parseStatusV2(z("1 .M N..."))
	require.Error(t, err)
	_, err = parseStatusV2(z("2 R. N... 100644 100644 100644 a a R100 new.go"))
	require.Error(t, err, "rename without original path")
	_, err = parseStatusV2(z("2 R. N... a"))
	require.Error(t, err)
	_, err = parseStatusV2(z("u UU N... a"))
	require.Error(t, err)
	_, err = parseStatusV2(z("x weird"))
	require.Error(t, err)
	_, err = parseStatusV2(z("1 MMM N... 100644 100644 100644 aaaa bbbb x"))
	require.Error(t, err)
	_, err = parseStatusV2(z("u U N... 100644 100644 100644 100644 h1 h2 h3 both.go"))
	require.Error(t, err)
	_, err = parseStatusV2(z("# short"))
	require.NoError(t, err, "unknown or short headers are ignored")
}
