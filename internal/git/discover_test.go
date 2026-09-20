package git

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDiscoverRoot(t *testing.T) {
	dir := t.TempDir()
	err := os.Mkdir(filepath.Join(dir, ".git"), 0o750)
	require.NoError(t, err)

	root, ok := DiscoverRoot(dir)
	assert.True(t, ok)
	assert.Equal(t, dir, root)
}

func TestDiscoverRoot_walksUp(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, ".git"), 0o750))

	sub := filepath.Join(dir, "deep", "nested")
	require.NoError(t, os.MkdirAll(sub, 0o750))

	root, ok := DiscoverRoot(sub)
	assert.True(t, ok)
	assert.Equal(t, dir, root)
}

func TestDiscoverRoot_worktree(t *testing.T) {
	// in worktrees and submodules .git is a file, not a directory
	dir := t.TempDir()
	err := os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: /some/other/path\n"), 0o600)
	require.NoError(t, err)

	root, ok := DiscoverRoot(dir)
	assert.True(t, ok)
	assert.Equal(t, dir, root)
}

func TestDiscoverRoot_none(t *testing.T) {
	dir := t.TempDir()
	root, ok := DiscoverRoot(dir)
	assert.False(t, ok)
	assert.Empty(t, root)
}
