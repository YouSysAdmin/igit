package keymap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseModes_commitPrefix(t *testing.T) {
	in := strings.NewReader("map commit:x commit\nmap y quit\nunmap commit:a\nunmap q\nmap commit:ctrl+w>s stash\n")
	maps, unmaps, err := parseModes(in)
	require.NoError(t, err)
	require.Len(t, maps, 3)
	assert.Equal(t, mapEntry{key: "x", action: ActionCommit, mode: modeCommit}, maps[0])
	assert.Equal(t, mapEntry{key: "y", action: ActionQuit}, maps[1])
	assert.Equal(t, mapEntry{key: "ctrl+w>s", action: ActionStash, mode: modeCommit}, maps[2])
	require.Len(t, unmaps, 2)
	assert.Equal(t, mapEntry{key: "a", mode: modeCommit}, unmaps[0])
	assert.Equal(t, mapEntry{key: "q"}, unmaps[1])
}

func TestParse_dropsCommitLines(t *testing.T) {
	in := strings.NewReader("map commit:x commit\nmap y quit\nunmap commit:a\n")
	maps, unmaps, err := parse(in)
	require.NoError(t, err)
	require.Len(t, maps, 1)
	assert.Equal(t, "y", maps[0].key)
	assert.Empty(t, unmaps)
}

func TestSplitMode(t *testing.T) {
	mode, key := splitMode("commit:x")
	assert.Equal(t, modeCommit, mode)
	assert.Equal(t, "x", key)
	mode, key = splitMode("x")
	assert.Empty(t, mode)
	assert.Equal(t, "x", key)
	mode, key = splitMode("commit:")
	assert.Empty(t, mode)
	assert.Equal(t, "commit:", key)
	mode, key = splitMode("other:x")
	assert.Empty(t, mode)
	assert.Equal(t, "other:x", key)
}

func TestLoadSet_appliesPerMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys")
	require.NoError(t, os.WriteFile(path, []byte("map commit:x commit\nunmap commit:a\nmap x quit\nunmap q\n"), 0o600))
	set, err := LoadSet(path)
	require.NoError(t, err)
	assert.Equal(t, ActionCommit, set.Commit.Resolve("x"))
	assert.Empty(t, set.Commit.Resolve("a"))
	assert.Equal(t, ActionStageAll, DefaultCommit().Resolve("a"), "default untouched")
	assert.Equal(t, ActionQuit, set.Review.Resolve("x"))
	assert.Empty(t, set.Review.Resolve("q"))
	assert.Equal(t, ActionQuit, set.Commit.Resolve("q"), "review unmap does not leak into commit map")
}

func TestLoadSet_missingFile(t *testing.T) {
	_, err := LoadSet(filepath.Join(t.TempDir(), "nope"))
	require.Error(t, err)
}

func TestLoadSetOrDefault(t *testing.T) {
	assert.Equal(t, ActionStageToggle, LoadSetOrDefault("").Commit.Resolve(" "))
	assert.Equal(t, ActionStageToggle, LoadSetOrDefault(filepath.Join(t.TempDir(), "missing")).Commit.Resolve(" "))
	path := filepath.Join(t.TempDir(), "keys")
	require.NoError(t, os.WriteFile(path, []byte("map commit:z push\n"), 0o600))
	assert.Equal(t, ActionPush, LoadSetOrDefault(path).Commit.Resolve("z"))
}

func TestSetDump_roundTrip(t *testing.T) {
	set := DefaultSet()
	set.Commit.Bind("z", ActionPush)
	set.Review.Unbind("q")
	var buf strings.Builder
	require.NoError(t, set.Dump(&buf))
	out := buf.String()
	assert.Contains(t, out, "map j down\n")
	assert.Contains(t, out, "map commit:z push\n")
	assert.Contains(t, out, "map commit:space stage_toggle\n")
	assert.NotContains(t, out, "map q quit\n")

	path := filepath.Join(t.TempDir(), "keys")
	require.NoError(t, os.WriteFile(path, []byte(out), 0o600))
	rebuilt, err := LoadSet(path)
	require.NoError(t, err)
	assert.Equal(t, set.Commit.bindings, rebuilt.Commit.bindings)
	// review "q" stays bound after reload: dump only writes effective bindings,
	// so a default that was unbound is restored by the defaults on load.
	assert.Equal(t, ActionQuit, rebuilt.Review.Resolve("q"))
	for k, a := range set.Review.bindings {
		assert.Equal(t, a, rebuilt.Review.bindings[k], "review key %q", k)
	}
}

func TestSetDump_failingWriter(t *testing.T) {
	// fail on the very first write and on the separator after the review section
	require.Error(t, DefaultSet().Dump(&failWriter{errAfter: 0}))
	reviewLines := len(strings.Split(strings.TrimSpace(mustDump(t, Default())), "\n"))
	require.Error(t, DefaultSet().Dump(&failWriter{errAfter: reviewLines + 1}))
}

func mustDump(t *testing.T, km *Keymap) string {
	t.Helper()
	var buf strings.Builder
	require.NoError(t, km.Dump(&buf))
	return buf.String()
}
