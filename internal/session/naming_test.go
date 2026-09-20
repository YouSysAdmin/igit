package session

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestOutputFileName(t *testing.T) {
	ts := time.Date(2026, 9, 18, 14, 5, 9, 0, time.UTC)
	assert.Equal(t, "igit-2026-09-18T14-05-09.md", OutputFileName("igit", ts))
	assert.Equal(t, "review-2026-09-18T14-05-09.md", OutputFileName("", ts))
	assert.Equal(t, "my-repo-x-2026-09-18T14-05-09.md", OutputFileName(" my repo/x ", ts))
	assert.Equal(t, "review-2026-09-18T14-05-09.md", OutputFileName("...", ts))
}

func TestRepoLabel(t *testing.T) {
	assert.Equal(t, "proj", RepoLabel("/home/u/proj", "/home/u/proj", nil))
	assert.Equal(t, "sub", RepoLabel("/home/u/proj", "/home/u/proj/sub", nil))
	assert.Equal(t, "proj", RepoLabel("/home/u/proj", "", nil))
	assert.Equal(t, "proj", RepoLabel("/home/u/proj/", "", nil))
	assert.Empty(t, RepoLabel("", "", nil))

	only := filepath.Join(t.TempDir(), "notes", "a.md")
	assert.Equal(t, "notes", RepoLabel("", "/ignored", []string{only}))
	assert.Equal(t, "ignored", RepoLabel("", "/ignored", []string{only, only}), "two --only paths use workDir")
	assert.Equal(t, "ignored", RepoLabel("/g", "/ignored", []string{only}), "inside a repo workDir wins")
}
