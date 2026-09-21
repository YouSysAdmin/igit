package tui

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yousysadmin/igit/internal/annot"
	"github.com/yousysadmin/igit/internal/git"
	"github.com/yousysadmin/igit/internal/tui/mocks"
	"github.com/yousysadmin/igit/internal/tui/sidepane"
	"github.com/yousysadmin/igit/internal/tui/worddiff"
)

func TestModel_FilesLoaded(t *testing.T) {
	m := testModel(nil, nil)

	result, cmd := m.Update(filesLoadedMsg{entries: []git.FileEntry{{Path: "internal/handler.go"}, {Path: "internal/store.go"}, {Path: "main.go"}}})
	model := result.(Model)

	// tree should be populated with 3 files
	assert.Equal(t, 3, model.tree.TotalFiles())
	assert.NotNil(t, cmd) // should auto-select first file
}

func TestModel_FilesLoadedError(t *testing.T) {
	m := testModel(nil, nil)
	m.ready = true
	m.filesLoaded = false

	result, cmd := m.Update(filesLoadedMsg{err: assert.AnError})
	model := result.(Model)

	assert.Nil(t, cmd)
	assert.Equal(t, 0, model.tree.TotalFiles())
	// filesLoaded must flip even on error, otherwise View() stays on "loading files..." forever
	assert.True(t, model.filesLoaded, "filesLoaded must be set before the error early-return so the loading screen exits")
}

func TestModel_FilesLoaded_DropsStaleResponses(t *testing.T) {
	// a slow first load (seq=0) must not overwrite the tree after
	// a newer load (seq=1) was issued - e.g. user toggled untracked immediately after startup.
	m := testModel(nil, nil)
	m.filesLoaded = false
	m.filesLoadSeq = 1 // simulate a newer load already dispatched (e.g. toggleUntracked)

	// stale response (seq=0) arrives first - must be dropped
	stale := []git.FileEntry{{Path: "stale.go"}}
	result, cmd := m.Update(filesLoadedMsg{seq: 0, entries: stale})
	model := result.(Model)
	assert.Nil(t, cmd)
	assert.False(t, model.filesLoaded, "stale response must not flip filesLoaded")
	assert.Equal(t, 0, model.tree.TotalFiles(), "stale entries must not populate tree")
	assert.Equal(t, "loading files...", model.View(), "View must still show loading while the current load is pending")

	// fresh response (seq=1) arrives - accepted
	fresh := []git.FileEntry{{Path: "fresh.go"}}
	result, _ = m.Update(filesLoadedMsg{seq: 1, entries: fresh})
	model = result.(Model)
	assert.True(t, model.filesLoaded)
	assert.Equal(t, 1, model.tree.TotalFiles())
}

func TestModel_ToggleUntrackedBumpsFilesLoadSeq(t *testing.T) {
	// toggleUntracked must bump filesLoadSeq so any in-flight load
	// from before the toggle is treated as stale by handleFilesLoaded.
	m := testModel(nil, nil)
	m.loadUntracked = func() ([]string, error) { return nil, nil }
	before := m.filesLoadSeq
	cmd := m.toggleUntracked()
	require.NotNil(t, cmd)
	assert.Greater(t, m.filesLoadSeq, before, "toggleUntracked must bump filesLoadSeq")
}

func TestModel_FilesLoadedMultipleFiles(t *testing.T) {
	m := testModel(nil, nil)
	result, _ := m.Update(filesLoadedMsg{entries: []git.FileEntry{{Path: "a.go"}, {Path: "b.go"}, {Path: "c.go"}}})
	model := result.(Model)

	assert.False(t, model.file.singleFile, "singleFile should be false for multiple files")
}

func TestModel_FileLoaded(t *testing.T) {
	m := testModel([]string{"a.go"}, nil)
	m.tree = testNewFileTree([]string{"a.go"})

	lines := []git.DiffLine{
		{NewNum: 1, Content: "package main", ChangeType: git.ChangeContext},
		{NewNum: 2, Content: "func main() {}", ChangeType: git.ChangeAdd},
	}

	result, _ := m.Update(fileLoadedMsg{file: "a.go", lines: lines})
	model := result.(Model)

	assert.Equal(t, "a.go", model.file.name)
	assert.Len(t, model.file.lines, 2)
}

func TestModel_LoadFileDiffThreadsRenameOrigin(t *testing.T) {
	var gotReq git.FileDiffRequest
	m := testModel([]string{"new.go"}, nil)
	m.tree = sidepane.NewFileTree([]git.FileEntry{{Path: "new.go", OldPath: "old.go", Status: git.FileRenamed}})
	m.diffSource = &mocks.DiffSourceMock{
		FileDiffFunc: func(req git.FileDiffRequest) ([]git.DiffLine, error) {
			gotReq = req
			return []git.DiffLine{{NewNum: 1, Content: "x", ChangeType: git.ChangeAdd}}, nil
		},
	}

	t.Run("request and message carry rename origin", func(t *testing.T) {
		msg := m.loadFileDiff("new.go")().(fileLoadedMsg)
		assert.Equal(t, "old.go", gotReq.OldPath, "request OldPath comes from the tree")
		assert.Equal(t, "new.go", gotReq.Path)
		assert.Equal(t, "old.go", msg.oldName, "message carries rename origin")
	})

	t.Run("handleFileLoaded sets oldName on the model", func(t *testing.T) {
		result, _ := m.Update(fileLoadedMsg{file: "new.go", oldName: "old.go", seq: m.file.loadSeq,
			lines: []git.DiffLine{{NewNum: 1, Content: "x", ChangeType: git.ChangeAdd}}})
		assert.Equal(t, "old.go", result.(Model).file.oldName)
	})

	t.Run("non-rename file has empty oldName", func(t *testing.T) {
		m2 := testModel([]string{"plain.go"}, nil)
		m2.tree = testNewFileTree([]string{"plain.go"})
		msg := m2.loadFileDiff("plain.go")().(fileLoadedMsg)
		assert.Empty(t, msg.oldName)
	})
}

func TestModel_ComputeFileStats(t *testing.T) {
	tests := []struct {
		name    string
		lines   []git.DiffLine
		adds    int
		removes int
	}{
		{name: "empty diff", lines: nil, adds: 0, removes: 0},
		{name: "context only", lines: []git.DiffLine{
			{NewNum: 1, Content: "package main", ChangeType: git.ChangeContext},
			{NewNum: 2, Content: "// comment", ChangeType: git.ChangeContext},
		}, adds: 0, removes: 0},
		{name: "adds only", lines: []git.DiffLine{
			{NewNum: 1, Content: "line1", ChangeType: git.ChangeAdd},
			{NewNum: 2, Content: "line2", ChangeType: git.ChangeAdd},
			{NewNum: 3, Content: "line3", ChangeType: git.ChangeAdd},
		}, adds: 3, removes: 0},
		{name: "removes only", lines: []git.DiffLine{
			{OldNum: 1, Content: "old1", ChangeType: git.ChangeRemove},
			{OldNum: 2, Content: "old2", ChangeType: git.ChangeRemove},
		}, adds: 0, removes: 2},
		{name: "mixed changes", lines: []git.DiffLine{
			{NewNum: 1, Content: "package main", ChangeType: git.ChangeContext},
			{OldNum: 2, Content: "old func", ChangeType: git.ChangeRemove},
			{NewNum: 2, Content: "new func", ChangeType: git.ChangeAdd},
			{NewNum: 3, Content: "// ok", ChangeType: git.ChangeContext},
			{Content: "", ChangeType: git.ChangeDivider},
			{NewNum: 10, Content: "added line", ChangeType: git.ChangeAdd},
		}, adds: 2, removes: 1},
		{name: "dividers ignored", lines: []git.DiffLine{
			{Content: "", ChangeType: git.ChangeDivider},
			{Content: "", ChangeType: git.ChangeDivider},
		}, adds: 0, removes: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := testModel(nil, nil)
			m.file.lines = tt.lines
			m.computeFileStats()
			assert.Equal(t, tt.adds, m.file.adds, "fileAdds")
			assert.Equal(t, tt.removes, m.file.removes, "fileRemoves")
		})
	}
}

func TestModel_FileStatsText(t *testing.T) {
	tests := []struct {
		name    string
		lines   []git.DiffLine
		adds    int
		removes int
		want    string
	}{
		{name: "context only shows line count", lines: []git.DiffLine{
			{NewNum: 1, Content: "line1", ChangeType: git.ChangeContext},
			{NewNum: 2, Content: "line2", ChangeType: git.ChangeContext},
			{NewNum: 3, Content: "line3", ChangeType: git.ChangeContext},
		}, adds: 0, removes: 0, want: "3 lines"},
		{name: "diff shows adds/removes", lines: []git.DiffLine{
			{NewNum: 1, Content: "added", ChangeType: git.ChangeAdd},
			{NewNum: 2, Content: "ctx", ChangeType: git.ChangeContext},
		}, adds: 1, removes: 0, want: "+1/-0"},
		{name: "empty diff shows +0/-0", lines: nil, adds: 0, removes: 0, want: "+0/-0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := testModel(nil, nil)
			m.file.lines = tt.lines
			m.file.adds = tt.adds
			m.file.removes = tt.removes
			assert.Equal(t, tt.want, m.fileStatsText())
		})
	}
}

func TestModel_FileLoadedComputesStats(t *testing.T) {
	lines := []git.DiffLine{
		{NewNum: 1, Content: "package main", ChangeType: git.ChangeContext},
		{OldNum: 2, Content: "removed", ChangeType: git.ChangeRemove},
		{NewNum: 2, Content: "added1", ChangeType: git.ChangeAdd},
		{NewNum: 3, Content: "added2", ChangeType: git.ChangeAdd},
	}
	m := testModel([]string{"a.go"}, map[string][]git.DiffLine{"a.go": lines})
	m.tree = testNewFileTree([]string{"a.go"})
	m.file.loadSeq = 1

	result, _ := m.Update(fileLoadedMsg{file: "a.go", seq: 1, lines: lines})
	model := result.(Model)
	assert.Equal(t, 2, model.file.adds)
	assert.Equal(t, 1, model.file.removes)
}

func TestModel_FilterOnly(t *testing.T) {
	toEntries := func(paths ...string) []git.FileEntry {
		entries := make([]git.FileEntry, len(paths))
		for i, p := range paths {
			entries[i] = git.FileEntry{Path: p}
		}
		return entries
	}

	t.Run("no filter returns all files", func(t *testing.T) {
		m := testModel(nil, nil)
		files := toEntries("ui/model.go", "diff/diff.go", "README.md")
		assert.Equal(t, files, m.filterOnly(files))
	})

	t.Run("exact path match", func(t *testing.T) {
		m := testModel(nil, nil)
		m.session.only = []string{"ui/model.go"}
		files := toEntries("ui/model.go", "diff/diff.go", "README.md")
		assert.Equal(t, []string{"ui/model.go"}, git.FileEntryPaths(m.filterOnly(files)))
	})

	t.Run("suffix match", func(t *testing.T) {
		m := testModel(nil, nil)
		m.session.only = []string{"model.go"}
		files := toEntries("ui/model.go", "diff/diff.go", "README.md")
		assert.Equal(t, []string{"ui/model.go"}, git.FileEntryPaths(m.filterOnly(files)))
	})

	t.Run("multiple patterns", func(t *testing.T) {
		m := testModel(nil, nil)
		m.session.only = []string{"model.go", "README.md"}
		files := toEntries("ui/model.go", "diff/diff.go", "README.md")
		assert.Equal(t, []string{"ui/model.go", "README.md"}, git.FileEntryPaths(m.filterOnly(files)))
	})

	t.Run("absolute path pattern resolved against workDir", func(t *testing.T) {
		m := testModel(nil, nil)
		m.session.only = []string{"/repo/README.md"}
		m.session.workDir = "/repo"
		files := toEntries("ui/model.go", "README.md")
		assert.Equal(t, []string{"README.md"}, git.FileEntryPaths(m.filterOnly(files)))
	})

	t.Run("absolute path pattern with subdirectory", func(t *testing.T) {
		m := testModel(nil, nil)
		m.session.only = []string{"/repo/ui/model.go"}
		m.session.workDir = "/repo"
		files := toEntries("ui/model.go", "diff/diff.go", "README.md")
		assert.Equal(t, []string{"ui/model.go"}, git.FileEntryPaths(m.filterOnly(files)))
	})

	t.Run("absolute path outside workDir does not match", func(t *testing.T) {
		m := testModel(nil, nil)
		m.session.only = []string{"/other/README.md"}
		m.session.workDir = "/repo"
		files := toEntries("README.md", "ui/model.go")
		assert.Empty(t, m.filterOnly(files))
	})

	t.Run("absolute path suffix match via resolved relative", func(t *testing.T) {
		m := testModel(nil, nil)
		m.session.only = []string{"/repo/model.go"}
		m.session.workDir = "/repo"
		files := toEntries("ui/model.go", "diff/diff.go")
		assert.Equal(t, []string{"ui/model.go"}, git.FileEntryPaths(m.filterOnly(files)))
	})

	t.Run("no matches returns empty", func(t *testing.T) {
		m := testModel(nil, nil)
		m.session.only = []string{"nonexistent.go"}
		files := toEntries("ui/model.go", "diff/diff.go")
		assert.Empty(t, m.filterOnly(files))
	})

	t.Run("dot-slash prefix matches relative entry", func(t *testing.T) {
		m := testModel(nil, nil)
		m.session.only = []string{"./CLAUDE.md"}
		m.session.workDir = "/repo"
		files := toEntries("CLAUDE.md", "ui/model.go")
		assert.Equal(t, []string{"CLAUDE.md"}, git.FileEntryPaths(m.filterOnly(files)))
	})

	t.Run("dot-slash prefix matches absolute entry", func(t *testing.T) {
		m := testModel(nil, nil)
		m.session.only = []string{"./CLAUDE.md"}
		m.session.workDir = "/repo"
		files := toEntries("/repo/CLAUDE.md", "/repo/ui/model.go")
		assert.Equal(t, []string{"/repo/CLAUDE.md"}, git.FileEntryPaths(m.filterOnly(files)))
	})

	t.Run("dot-slash prefix with subdirectory", func(t *testing.T) {
		m := testModel(nil, nil)
		m.session.only = []string{"./ui/model.go"}
		m.session.workDir = "/repo"
		files := toEntries("ui/model.go", "diff/diff.go")
		assert.Equal(t, []string{"ui/model.go"}, git.FileEntryPaths(m.filterOnly(files)))
	})

	t.Run("relative pattern matches absolute entry", func(t *testing.T) {
		m := testModel(nil, nil)
		m.session.only = []string{"CLAUDE.md"}
		m.session.workDir = "/repo"
		files := toEntries("/repo/CLAUDE.md", "/repo/ui/model.go")
		assert.Equal(t, []string{"/repo/CLAUDE.md"}, git.FileEntryPaths(m.filterOnly(files)))
	})

	t.Run("dot-slash prefix no workDir falls through to exact/suffix", func(t *testing.T) {
		m := testModel(nil, nil)
		m.session.only = []string{"./CLAUDE.md"}
		files := toEntries("CLAUDE.md", "ui/model.go")
		assert.Empty(t, m.filterOnly(files))
	})

	t.Run("relative pattern escaping workDir does not match", func(t *testing.T) {
		m := testModel(nil, nil)
		m.session.only = []string{"../other/CLAUDE.md"}
		m.session.workDir = "/repo"
		files := toEntries("CLAUDE.md", "ui/model.go")
		assert.Empty(t, m.filterOnly(files))
	})
}

func TestModel_FilterOnlyNoMatchShowsMessage(t *testing.T) {
	m := testModel(nil, nil)
	m.session.only = []string{"nonexistent.go"}
	m.ready = true
	m.layout.width = 80
	m.layout.height = 24
	m.layout.viewport = viewport.New(76, 20)

	result, cmd := m.Update(filesLoadedMsg{entries: []git.FileEntry{{Path: "ui/model.go"}, {Path: "diff/diff.go"}}})
	model := result.(Model)
	assert.Nil(t, cmd, "should not trigger file load when no files match")
	assert.Contains(t, model.layout.viewport.View(), "no files match --only filter")
}

func TestModel_UntrackedStartup(t *testing.T) {
	t.Run("ShowUntracked=true with LoadUntracked initializes showUntracked on", func(t *testing.T) {
		renderer := &mocks.DiffSourceMock{}
		store := annot.NewStore()
		m := testNewModel(t, renderer, store, noopHighlighter(), ModelConfig{
			TreeWidthRatio: 3,
			ShowUntracked:  true,
			LoadUntracked:  func() ([]string, error) { return nil, nil },
		})
		assert.True(t, m.modes.showUntracked)
	})

	t.Run("ShowUntracked=true without LoadUntracked stays off", func(t *testing.T) {
		renderer := &mocks.DiffSourceMock{}
		store := annot.NewStore()
		m := testNewModel(t, renderer, store, noopHighlighter(), ModelConfig{
			TreeWidthRatio: 3,
			ShowUntracked:  true,
			LoadUntracked:  nil,
		})
		assert.False(t, m.modes.showUntracked, "no-op when LoadUntracked is not wired (e.g. stdin, compare modes)")
	})

	t.Run("ShowUntracked=false stays off even with LoadUntracked wired", func(t *testing.T) {
		renderer := &mocks.DiffSourceMock{}
		store := annot.NewStore()
		m := testNewModel(t, renderer, store, noopHighlighter(), ModelConfig{
			TreeWidthRatio: 3,
			ShowUntracked:  false,
			LoadUntracked:  func() ([]string, error) { return nil, nil },
		})
		assert.False(t, m.modes.showUntracked)
	})

	t.Run("startup loadFiles appends untracked entries when ShowUntracked=true", func(t *testing.T) {
		renderer := &mocks.DiffSourceMock{
			ChangedFilesFunc: func(ref string, staged bool) ([]git.FileEntry, error) {
				return []git.FileEntry{{Path: "main.go", Status: git.FileModified}}, nil
			},
		}
		store := annot.NewStore()
		m := testNewModel(t, renderer, store, noopHighlighter(), ModelConfig{
			TreeWidthRatio: 3,
			ShowUntracked:  true,
			LoadUntracked:  func() ([]string, error) { return []string{"newfile.go"}, nil },
		})
		require.True(t, m.modes.showUntracked)

		msg := m.loadFiles()()
		flMsg, ok := msg.(filesLoadedMsg)
		require.True(t, ok)
		paths := make([]string, 0, len(flMsg.entries))
		for _, e := range flMsg.entries {
			paths = append(paths, e.Path)
		}
		assert.Contains(t, paths, "main.go")
		assert.Contains(t, paths, "newfile.go")
	})

	t.Run("staged + untracked compose: both appear in startup load", func(t *testing.T) {
		renderer := &mocks.DiffSourceMock{
			ChangedFilesFunc: func(ref string, staged bool) ([]git.FileEntry, error) {
				assert.True(t, staged, "staged flag should be propagated to ChangedFiles")
				return []git.FileEntry{{Path: "staged.go", Status: git.FileAdded}}, nil
			},
		}
		store := annot.NewStore()
		m := testNewModel(t, renderer, store, noopHighlighter(), ModelConfig{
			TreeWidthRatio: 3,
			Staged:         true,
			ShowUntracked:  true,
			LoadUntracked:  func() ([]string, error) { return []string{"newfile.go"}, nil },
		})

		msg := m.loadFiles()()
		flMsg, ok := msg.(filesLoadedMsg)
		require.True(t, ok)
		paths := make([]string, 0, len(flMsg.entries))
		for _, e := range flMsg.entries {
			paths = append(paths, e.Path)
		}
		assert.Contains(t, paths, "staged.go")
		assert.Contains(t, paths, "newfile.go")
	})
}

func TestModel_UntrackedToggle(t *testing.T) {
	t.Run("toggle cycles showUntracked and reloads files", func(t *testing.T) {
		entries := []git.FileEntry{{Path: "main.go", Status: git.FileModified}}
		renderer := &mocks.DiffSourceMock{
			ChangedFilesFunc: func(ref string, staged bool) ([]git.FileEntry, error) {
				return entries, nil
			},
			FileDiffFunc: func(git.FileDiffRequest) ([]git.DiffLine, error) {
				return []git.DiffLine{{Content: "line1", ChangeType: git.ChangeContext, OldNum: 1, NewNum: 1}}, nil
			},
		}
		store := annot.NewStore()
		m := testNewModel(t, renderer, store, noopHighlighter(), ModelConfig{
			TreeWidthRatio: 3,
			LoadUntracked: func() ([]string, error) {
				return []string{"newfile.go"}, nil
			},
		})
		m.layout.width = 120
		m.layout.height = 40
		m.ready = true

		// initially untracked is off
		assert.False(t, m.modes.showUntracked)

		// toggle on
		result, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'u'}})
		assert.True(t, result.(Model).modes.showUntracked)
		assert.NotNil(t, cmd)

		// execute loadFiles command - should include untracked file
		msg := cmd()
		flMsg := msg.(filesLoadedMsg)
		paths := make([]string, 0, len(flMsg.entries))
		for _, e := range flMsg.entries {
			paths = append(paths, e.Path)
		}
		assert.Contains(t, paths, "main.go")
		assert.Contains(t, paths, "newfile.go")

		// toggle off - use result from toggle on
		m = result.(Model)
		result, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'u'}})
		assert.False(t, result.(Model).modes.showUntracked)
	})

	t.Run("status bar shows untracked icon when untracked is on", func(t *testing.T) {
		m := testModel([]string{"a.go"}, nil)
		// the untracked icon is always in the list but muted when showUntracked is false
		// check that the untracked icon becomes active when toggled
		m.modes.showUntracked = true
		iconsActive := m.statusModeIcons()
		assert.Contains(t, iconsActive, "∅")
	})

	t.Run("no untracked files when LoadUntracked is nil", func(t *testing.T) {
		entries := []git.FileEntry{{Path: "main.go"}}
		renderer := &mocks.DiffSourceMock{
			ChangedFilesFunc: func(ref string, staged bool) ([]git.FileEntry, error) {
				return entries, nil
			},
			FileDiffFunc: func(git.FileDiffRequest) ([]git.DiffLine, error) {
				return nil, nil
			},
		}
		store := annot.NewStore()
		m := testNewModel(t, renderer, store, noopHighlighter(), ModelConfig{TreeWidthRatio: 3})
		m.layout.width = 120
		m.layout.height = 40
		m.ready = true
		m.modes.showUntracked = true

		// directly execute loadFiles to check behavior without toggling
		cmd := m.loadFiles()
		msg := cmd()
		flMsg := msg.(filesLoadedMsg)
		assert.Len(t, flMsg.entries, 1, "should only have the original file, no untracked")
	})

	t.Run("dedup: untracked file already in staged list", func(t *testing.T) {
		entries := []git.FileEntry{{Path: "newfile.go", Status: git.FileAdded}}
		renderer := &mocks.DiffSourceMock{
			ChangedFilesFunc: func(ref string, staged bool) ([]git.FileEntry, error) {
				return entries, nil
			},
			FileDiffFunc: func(git.FileDiffRequest) ([]git.DiffLine, error) {
				return nil, nil
			},
		}
		store := annot.NewStore()
		m := testNewModel(t, renderer, store, noopHighlighter(), ModelConfig{
			TreeWidthRatio: 3,
			LoadUntracked: func() ([]string, error) {
				return []string{"newfile.go", "other.go"}, nil
			},
		})
		m.layout.width = 120
		m.layout.height = 40
		m.ready = true
		m.modes.showUntracked = true

		cmd := m.loadFiles()
		msg := cmd()
		flMsg := msg.(filesLoadedMsg)
		assert.Len(t, flMsg.entries, 2, "newfile.go should not be duplicated")
	})
}

func TestModel_UntrackedRenames(t *testing.T) {
	t.Run("rename replaces deletion with single rename entry", func(t *testing.T) {
		renderer := &mocks.DiffSourceMock{
			ChangedFilesFunc: func(ref string, staged bool) ([]git.FileEntry, error) {
				return []git.FileEntry{{Path: "old.txt", Status: git.FileDeleted}}, nil
			},
		}
		store := annot.NewStore()
		m := testNewModel(t, renderer, store, noopHighlighter(), ModelConfig{
			TreeWidthRatio: 3,
			ShowUntracked:  true,
			LoadUntracked:  func() ([]string, error) { return []string{"new.txt"}, nil },
			LoadUntrackedRenames: func([]string) ([]git.FileEntry, error) {
				return []git.FileEntry{{Path: "new.txt", OldPath: "old.txt", Status: git.FileRenamed}}, nil
			},
		})

		flMsg := m.loadFiles()().(filesLoadedMsg)
		require.Len(t, flMsg.entries, 1, "deletion+untracked-add collapse into one rename")
		assert.Equal(t, git.FileRenamed, flMsg.entries[0].Status)
		assert.Equal(t, "new.txt", flMsg.entries[0].Path)
		assert.Equal(t, "old.txt", flMsg.entries[0].OldPath)
	})

	t.Run("non-rename untracked files still appended", func(t *testing.T) {
		renderer := &mocks.DiffSourceMock{
			ChangedFilesFunc: func(ref string, staged bool) ([]git.FileEntry, error) {
				return []git.FileEntry{{Path: "old.txt", Status: git.FileDeleted}}, nil
			},
		}
		store := annot.NewStore()
		m := testNewModel(t, renderer, store, noopHighlighter(), ModelConfig{
			TreeWidthRatio: 3,
			ShowUntracked:  true,
			LoadUntracked:  func() ([]string, error) { return []string{"new.txt", "extra.txt"}, nil },
			LoadUntrackedRenames: func([]string) ([]git.FileEntry, error) {
				return []git.FileEntry{{Path: "new.txt", OldPath: "old.txt", Status: git.FileRenamed}}, nil
			},
		})

		flMsg := m.loadFiles()().(filesLoadedMsg)
		byPath := make(map[string]git.ChangeStatus, len(flMsg.entries))
		for _, e := range flMsg.entries {
			byPath[e.Path] = e.Status
		}
		assert.Equal(t, git.FileRenamed, byPath["new.txt"])
		assert.Equal(t, git.FileUntracked, byPath["extra.txt"])
		assert.NotContains(t, byPath, "old.txt", "rename origin deletion is dropped")
		assert.Len(t, flMsg.entries, 2)
	})

	t.Run("detector failure surfaces a warning and keeps untracked files", func(t *testing.T) {
		renderer := &mocks.DiffSourceMock{
			ChangedFilesFunc: func(ref string, staged bool) ([]git.FileEntry, error) {
				return nil, nil
			},
		}
		store := annot.NewStore()
		m := testNewModel(t, renderer, store, noopHighlighter(), ModelConfig{
			TreeWidthRatio: 3,
			ShowUntracked:  true,
			LoadUntracked:  func() ([]string, error) { return []string{"new.txt"}, nil },
			LoadUntrackedRenames: func([]string) ([]git.FileEntry, error) {
				return nil, errors.New("boom")
			},
		})

		flMsg := m.loadFiles()().(filesLoadedMsg)
		require.Len(t, flMsg.entries, 1)
		assert.Equal(t, "new.txt", flMsg.entries[0].Path)
		require.Len(t, flMsg.warnings, 1)
		assert.Contains(t, flMsg.warnings[0], "untracked renames")
	})
}

func TestModel_detectUntrackedRenames(t *testing.T) {
	renames := []git.FileEntry{{Path: "new.txt", OldPath: "old.txt", Status: git.FileRenamed}}
	detector := func([]string) ([]git.FileEntry, error) { return renames, nil }

	tests := []struct {
		name     string
		detector func([]string) ([]git.FileEntry, error)
		ref      string
		staged   bool
		want     int
	}{
		{"working-tree unstaged runs detection", detector, "", false, 1},
		{"nil detector is a no-op", nil, "", false, 0},
		{"ref set skips detection", detector, "HEAD~1", false, 0},
		{"staged skips detection", detector, "", true, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := Model{loadUntrackedRenames: tt.detector}
			m.session.ref = tt.ref
			m.session.staged = tt.staged
			got, warn := m.detectUntrackedRenames([]string{"new.txt"})
			assert.Empty(t, warn)
			assert.Len(t, got, tt.want)
		})
	}
}

func TestModel_mergeUntrackedEntries(t *testing.T) {
	rename := git.FileEntry{Path: "new.txt", OldPath: "old.txt", Status: git.FileRenamed}

	tests := []struct {
		name      string
		entries   []git.FileEntry
		untracked []string
		renames   []git.FileEntry
		want      map[string]git.ChangeStatus
	}{
		{
			name:      "no renames appends untracked as-is",
			entries:   []git.FileEntry{{Path: "mod.txt", Status: git.FileModified}},
			untracked: []string{"fresh.txt"},
			want:      map[string]git.ChangeStatus{"mod.txt": git.FileModified, "fresh.txt": git.FileUntracked},
		},
		{
			name:      "rename drops origin deletion and skips its new-side untracked dup",
			entries:   []git.FileEntry{{Path: "old.txt", Status: git.FileDeleted}},
			untracked: []string{"new.txt"},
			renames:   []git.FileEntry{rename},
			want:      map[string]git.ChangeStatus{"new.txt": git.FileRenamed},
		},
		{
			name:      "unrelated deletion is preserved",
			entries:   []git.FileEntry{{Path: "old.txt", Status: git.FileDeleted}, {Path: "gone.txt", Status: git.FileDeleted}},
			untracked: []string{"new.txt"},
			renames:   []git.FileEntry{rename},
			want:      map[string]git.ChangeStatus{"new.txt": git.FileRenamed, "gone.txt": git.FileDeleted},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Model{}.mergeUntrackedEntries(tt.entries, tt.untracked, tt.renames)
			byPath := make(map[string]git.ChangeStatus, len(got))
			for _, e := range got {
				byPath[e.Path] = e.Status
			}
			assert.Equal(t, tt.want, byPath)
		})
	}
}

func TestModel_StagedOnlyFiles(t *testing.T) {
	t.Run("staged-only new files included in file list", func(t *testing.T) {
		// simulate: working tree has no changes, but index has a new file
		renderer := &mocks.DiffSourceMock{
			ChangedFilesFunc: func(ref string, staged bool) ([]git.FileEntry, error) {
				if staged {
					return []git.FileEntry{{Path: "newfile.go", Status: git.FileAdded}}, nil
				}
				return nil, nil // no unstaged changes
			},
			FileDiffFunc: func(git.FileDiffRequest) ([]git.DiffLine, error) {
				return []git.DiffLine{{Content: "content", ChangeType: git.ChangeAdd, NewNum: 1}}, nil
			},
		}
		store := annot.NewStore()
		m := testNewModel(t, renderer, store, noopHighlighter(), ModelConfig{TreeWidthRatio: 3})
		m.layout.width = 120
		m.layout.height = 40
		m.ready = true

		cmd := m.Init()
		msg := cmd()
		flMsg := msg.(filesLoadedMsg)
		assert.Len(t, flMsg.entries, 1)
		assert.Equal(t, "newfile.go", flMsg.entries[0].Path)
		assert.Equal(t, git.FileAdded, flMsg.entries[0].Status)
	})

	t.Run("staged-only files not duplicated when already in unstaged list", func(t *testing.T) {
		renderer := &mocks.DiffSourceMock{
			ChangedFilesFunc: func(ref string, staged bool) ([]git.FileEntry, error) {
				if staged {
					return []git.FileEntry{{Path: "main.go", Status: git.FileModified}}, nil
				}
				return []git.FileEntry{{Path: "main.go", Status: git.FileModified}}, nil
			},
			FileDiffFunc: func(git.FileDiffRequest) ([]git.DiffLine, error) {
				return nil, nil
			},
		}
		store := annot.NewStore()
		m := testNewModel(t, renderer, store, noopHighlighter(), ModelConfig{TreeWidthRatio: 3})
		m.layout.width = 120
		m.layout.height = 40
		m.ready = true

		cmd := m.Init()
		msg := cmd()
		flMsg := msg.(filesLoadedMsg)
		assert.Len(t, flMsg.entries, 1, "main.go should not be duplicated")
	})

	t.Run("staged-only new files are not merged when unstaged changes exist", func(t *testing.T) {
		renderer := &mocks.DiffSourceMock{
			ChangedFilesFunc: func(ref string, staged bool) ([]git.FileEntry, error) {
				if staged {
					return []git.FileEntry{{Path: "newfile.go", Status: git.FileAdded}}, nil
				}
				return []git.FileEntry{{Path: "main.go", Status: git.FileModified}}, nil
			},
			FileDiffFunc: func(git.FileDiffRequest) ([]git.DiffLine, error) {
				return nil, nil
			},
		}
		store := annot.NewStore()
		m := testNewModel(t, renderer, store, noopHighlighter(), ModelConfig{TreeWidthRatio: 3})
		m.layout.width = 120
		m.layout.height = 40
		m.ready = true

		cmd := m.Init()
		msg := cmd()
		flMsg := msg.(filesLoadedMsg)
		assert.Equal(t, []git.FileEntry{{Path: "main.go", Status: git.FileModified}}, flMsg.entries)
	})

	t.Run("staged fetch failure logged as warning", func(t *testing.T) {
		renderer := &mocks.DiffSourceMock{
			ChangedFilesFunc: func(ref string, staged bool) ([]git.FileEntry, error) {
				if staged {
					return nil, errors.New("git error")
				}
				return nil, nil
			},
			FileDiffFunc: func(git.FileDiffRequest) ([]git.DiffLine, error) {
				return nil, nil
			},
		}
		store := annot.NewStore()
		m := testNewModel(t, renderer, store, noopHighlighter(), ModelConfig{TreeWidthRatio: 3})
		m.layout.width = 120
		m.layout.height = 40
		m.ready = true

		cmd := m.Init()
		msg := cmd()
		flMsg := msg.(filesLoadedMsg)
		assert.Empty(t, flMsg.entries)
		assert.Len(t, flMsg.warnings, 1, "staged fetch error should be in warnings")
		assert.Contains(t, flMsg.warnings[0], "git error")
	})
}

func TestModel_HandleFileLoadedUntrackedFallback(t *testing.T) {
	t.Run("untracked file with empty diff falls back to disk read", func(t *testing.T) {
		entries := []git.FileEntry{{Path: "newfile.go", Status: git.FileUntracked}}
		renderer := &mocks.DiffSourceMock{
			ChangedFilesFunc: func(ref string, staged bool) ([]git.FileEntry, error) {
				return entries, nil
			},
			FileDiffFunc: func(git.FileDiffRequest) ([]git.DiffLine, error) {
				return nil, nil // empty diff for untracked
			},
		}
		store := annot.NewStore()
		m := testNewModel(t, renderer, store, noopHighlighter(), ModelConfig{TreeWidthRatio: 3, WorkDir: "testdata"})
		m.layout.width = 120
		m.layout.height = 40
		m.ready = true

		// load files then select the untracked file
		cmd := m.Init()
		msg := cmd()
		flMsg := msg.(filesLoadedMsg)
		_ = flMsg
		// handleFilesLoaded auto-selects first file and returns loadFileDiff cmd
		result, cmd := m.Update(msg)
		m = result.(Model)
		// handleFileLoaded - empty diff triggers fallback
		msg2 := cmd()
		result, _ = m.Update(msg2)
		m = result.(Model)
		// reaching here without panic means the fallback path handled the untracked file correctly.
		// if testdata/newfile.go doesn't exist on disk, diffLines stays nil - that's expected
		assert.Equal(t, "newfile.go", m.file.name)
	})

	t.Run("non-untracked file with empty diff does not trigger fallback", func(t *testing.T) {
		entries := []git.FileEntry{{Path: "main.go", Status: git.FileModified}}
		renderer := &mocks.DiffSourceMock{
			ChangedFilesFunc: func(ref string, staged bool) ([]git.FileEntry, error) {
				return entries, nil
			},
			FileDiffFunc: func(git.FileDiffRequest) ([]git.DiffLine, error) {
				return nil, nil // empty diff for some reason
			},
		}
		store := annot.NewStore()
		m := testNewModel(t, renderer, store, noopHighlighter(), ModelConfig{TreeWidthRatio: 3, WorkDir: "testdata"})
		m.layout.width = 120
		m.layout.height = 40
		m.ready = true

		cmd := m.Init()
		msg := cmd()
		result, cmd := m.Update(msg)
		m = result.(Model)
		msg2 := cmd()
		result, _ = m.Update(msg2)
		m = result.(Model)
		assert.Nil(t, m.file.lines, "non-untracked empty diff should not trigger disk fallback")
	})
}

func TestModel_HandleFileLoadedStagedOnlyFallback(t *testing.T) {
	t.Run("staged-only FileAdded with empty diff retries with --cached", func(t *testing.T) {
		entries := []git.FileEntry{{Path: "newfile.go", Status: git.FileAdded}}
		cachedLines := []git.DiffLine{{NewNum: 1, Content: "package main", ChangeType: git.ChangeContext}}
		renderer := &mocks.DiffSourceMock{
			ChangedFilesFunc: func(ref string, staged bool) ([]git.FileEntry, error) {
				return entries, nil
			},
			FileDiffFunc: func(req git.FileDiffRequest) ([]git.DiffLine, error) {
				if req.Staged {
					return cachedLines, nil
				}
				return nil, nil // empty unstaged diff for staged-only file
			},
		}
		store := annot.NewStore()
		m := testNewModel(t, renderer, store, noopHighlighter(), ModelConfig{TreeWidthRatio: 3, WorkDir: "testdata"})
		m.layout.width = 120
		m.layout.height = 40
		m.ready = true

		// load files then handle auto-selected first file
		cmd := m.Init()
		msg := cmd()
		result, cmd := m.Update(msg)
		m = result.(Model)
		msg2 := cmd()
		result, _ = m.Update(msg2)
		m = result.(Model)

		assert.Equal(t, cachedLines, m.file.lines, "staged-only file should show --cached diff content")
	})

	t.Run("staged mode does not retry --cached", func(t *testing.T) {
		entries := []git.FileEntry{{Path: "newfile.go", Status: git.FileAdded}}
		renderer := &mocks.DiffSourceMock{
			ChangedFilesFunc: func(ref string, staged bool) ([]git.FileEntry, error) {
				return entries, nil
			},
			FileDiffFunc: func(git.FileDiffRequest) ([]git.DiffLine, error) {
				return nil, nil // empty diff even with --cached
			},
		}
		store := annot.NewStore()
		m := testNewModel(t, renderer, store, noopHighlighter(), ModelConfig{TreeWidthRatio: 3, WorkDir: "testdata"})
		m.layout.width = 120
		m.layout.height = 40
		m.ready = true
		m.session.staged = true

		cmd := m.Init()
		msg := cmd()
		result, cmd := m.Update(msg)
		m = result.(Model)
		msg2 := cmd()
		result, _ = m.Update(msg2)
		m = result.(Model)

		assert.Nil(t, m.file.lines, "staged mode should not retry with --cached")
	})

	t.Run("FileModified with empty diff does not trigger staged retry", func(t *testing.T) {
		entries := []git.FileEntry{{Path: "main.go", Status: git.FileModified}}
		renderer := &mocks.DiffSourceMock{
			ChangedFilesFunc: func(ref string, staged bool) ([]git.FileEntry, error) {
				return entries, nil
			},
			FileDiffFunc: func(req git.FileDiffRequest) ([]git.DiffLine, error) {
				if req.Staged {
					return []git.DiffLine{{NewNum: 1, Content: "staged", ChangeType: git.ChangeContext}}, nil
				}
				return nil, nil
			},
		}
		store := annot.NewStore()
		m := testNewModel(t, renderer, store, noopHighlighter(), ModelConfig{TreeWidthRatio: 3, WorkDir: "testdata"})
		m.layout.width = 120
		m.layout.height = 40
		m.ready = true

		cmd := m.Init()
		msg := cmd()
		result, cmd := m.Update(msg)
		m = result.(Model)
		msg2 := cmd()
		result, _ = m.Update(msg2)
		m = result.(Model)

		assert.Nil(t, m.file.lines, "FileModified should not trigger staged retry even with empty diff")
	})

	t.Run("staged retry propagates current compact context", func(t *testing.T) {
		entries := []git.FileEntry{{Path: "newfile.go", Status: git.FileAdded}}
		cachedLines := []git.DiffLine{{NewNum: 1, Content: "package main", ChangeType: git.ChangeContext}}
		var stagedCtx int
		renderer := &mocks.DiffSourceMock{
			ChangedFilesFunc: func(ref string, staged bool) ([]git.FileEntry, error) {
				return entries, nil
			},
			FileDiffFunc: func(req git.FileDiffRequest) ([]git.DiffLine, error) {
				if req.Staged {
					stagedCtx = req.ContextLines
					return cachedLines, nil
				}
				return nil, nil
			},
		}
		store := annot.NewStore()
		m := testNewModel(t, renderer, store, noopHighlighter(), ModelConfig{TreeWidthRatio: 3, WorkDir: "testdata"})
		m.layout.width = 120
		m.layout.height = 40
		m.ready = true
		m.compact.applicable = true
		m.modes.compact = true
		m.modes.compactContext = 5

		cmd := m.Init()
		msg := cmd()
		result, cmd := m.Update(msg)
		m = result.(Model)
		msg2 := cmd()
		result, _ = m.Update(msg2)
		m = result.(Model)

		assert.Equal(t, 5, stagedCtx, "staged retry must pass current compact context, not 0")
		assert.Equal(t, cachedLines, m.file.lines)
	})
}

func TestModel_FilesLoadedSingleFile(t *testing.T) {
	m := testModel(nil, nil)
	result, cmd := m.Update(filesLoadedMsg{entries: []git.FileEntry{{Path: "main.go"}}})
	model := result.(Model)

	assert.True(t, model.file.singleFile, "singleFile should be true for one file")
	assert.Equal(t, paneDiff, model.layout.focus, "focus should be on diff pane in single-file mode")
	assert.NotNil(t, cmd) // should auto-select first file
}

func TestModel_FilesLoadedKeepsTreeWidth(t *testing.T) {
	m := testModel(nil, nil)
	// simulate initial resize (viewport created with multi-file width)
	resized, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m = resized.(Model)
	assert.True(t, m.ready, "model should be ready after resize")

	// a single file keeps the tree pane at its configured width
	result, _ := m.Update(filesLoadedMsg{entries: []git.FileEntry{{Path: "main.go"}}})
	model := result.(Model)
	assert.True(t, model.file.singleFile)
	assert.Equal(t, 30, model.layout.treeWidth, "tree keeps the configured width")
	assert.Equal(t, 66, model.layout.viewport.Width, "viewport width is width - tree - borders")
}

func TestModel_HandleBlameLoadedSyncsViewportForWrap(t *testing.T) {
	m := testModel(nil, nil)
	m.file.name = "a.go"
	m.file.lines = []git.DiffLine{
		{NewNum: 1, Content: strings.Repeat("a", 60), ChangeType: git.ChangeContext},
		{NewNum: 2, Content: "tail", ChangeType: git.ChangeContext},
	}
	m.modes.wrap = true
	m.modes.showBlame = true
	m.layout.focus = paneDiff
	m.layout.treeHidden = true
	m.layout.width = 40
	m.layout.viewport = viewport.New(37, 2)
	m.nav.diffCursor = 1

	m.syncViewportToCursor()
	before := m.layout.viewport.YOffset

	result, _ := m.handleBlameLoaded(blameLoadedMsg{
		file: "a.go",
		seq:  m.file.loadSeq,
		data: map[int]git.BlameLine{
			1: {Author: "LongAuthor", Time: time.Now()},
			2: {Author: "LongAuthor", Time: time.Now()},
		},
	})
	model := result.(Model)

	assert.Greater(t, model.layout.viewport.YOffset, before, "viewport should be re-synced after blame narrows wrap width")
	cursorY := model.cursorViewportY()
	assert.GreaterOrEqual(t, cursorY, model.layout.viewport.YOffset)
	assert.Less(t, cursorY, model.layout.viewport.YOffset+model.layout.viewport.Height)
}

func TestModel_FileLoadedResetsCursor(t *testing.T) {
	lines := []git.DiffLine{
		{NewNum: 1, Content: "line1", ChangeType: git.ChangeContext},
		{NewNum: 2, Content: "line2", ChangeType: git.ChangeContext},
	}

	m := testModel([]string{"a.go"}, nil)
	m.tree = testNewFileTree([]string{"a.go"})
	m.nav.diffCursor = 5 // simulate cursor was elsewhere

	result, _ := m.Update(fileLoadedMsg{file: "a.go", lines: lines})
	model := result.(Model)
	assert.Equal(t, 0, model.nav.diffCursor) // cursor reset to first line
}

func TestModel_FileLoadedAcceptedAfterCursorMove(t *testing.T) {
	// simulate: user presses n to load b.go (seq=1), then j/k moves cursor to c.go before response arrives.
	// the response for b.go should still be accepted because it carries the latest sequence number.
	files := []string{"a.go", "b.go", "c.go"}
	m := testModel(files, nil)
	m.tree = testNewFileTree(files)

	// user presses n to load b.go
	m.file.loadSeq = 1
	m.tree.StepFile(sidepane.DirectionNext) // cursor -> b.go

	// then j/k moves cursor to c.go (without triggering a load)
	m.tree.Move(sidepane.MotionDown) // cursor -> c.go
	assert.Equal(t, "c.go", m.tree.SelectedFile(), "cursor moved to c.go")

	// b.go response arrives with matching seq - should be accepted
	bLines := []git.DiffLine{{NewNum: 1, Content: "package b", ChangeType: git.ChangeContext}}
	result, _ := m.Update(fileLoadedMsg{file: "b.go", seq: 1, lines: bLines})
	model := result.(Model)
	assert.Equal(t, "b.go", model.file.name, "response should be accepted despite cursor being on c.go")
	assert.Equal(t, bLines, model.file.lines)
}

func TestModel_TriggerReload_BumpsFileLoadSeq(t *testing.T) {
	m := testNewModel(t, plainRenderer(), annot.NewStore(), noopHighlighter(), ModelConfig{})
	oldSeq := m.file.loadSeq

	m.triggerReload()

	assert.Equal(t, oldSeq+1, m.file.loadSeq, "triggerReload must bump file.loadSeq to invalidate in-flight fileLoadedMsg")
}

func TestModel_TriggerReload_DropsStaleFileLoadedMsg(t *testing.T) {
	// a fileLoadedMsg dispatched before R is pressed must be
	// dropped by handleFileLoaded because triggerReload bumps file.loadSeq.
	m := testNewModel(t, plainRenderer(), annot.NewStore(), noopHighlighter(), ModelConfig{})
	sentinelLine := git.DiffLine{Content: "sentinel", ChangeType: git.ChangeContext, NewNum: 1}
	m.file.name = "a.go"
	m.file.lines = []git.DiffLine{sentinelLine}

	// capture seq before reload. this is the seq the in-flight load was dispatched with
	oldSeq := m.file.loadSeq

	m.triggerReload() // bumps file.loadSeq - the old seq is now stale

	// deliver the stale fileLoadedMsg (seq matches oldSeq, not the bumped value)
	staleLine := git.DiffLine{Content: "stale", ChangeType: git.ChangeAdd, NewNum: 1}
	result, _ := m.Update(fileLoadedMsg{file: "a.go", seq: oldSeq, lines: []git.DiffLine{staleLine}})
	model := result.(Model)

	// stale message must be dropped - sentinel lines must be unchanged
	assert.Equal(t, []git.DiffLine{sentinelLine}, model.file.lines,
		"stale fileLoadedMsg must be dropped after triggerReload bumps file.loadSeq")
}

func TestModel_LoadCommits_ReturnsNilWhenNotApplicable(t *testing.T) {
	m := testModel(nil, nil)
	m.commits.source = &fakeCommitLog{}
	m.commits.applicable = false

	cmd := m.loadCommits()
	assert.Nil(t, cmd, "loadCommits must return nil when not applicable")
}

func TestModel_LoadCommits_ReturnsNilWhenSourceIsNil(t *testing.T) {
	m := testModel(nil, nil)
	m.commits.source = nil
	m.commits.applicable = true

	cmd := m.loadCommits()
	assert.Nil(t, cmd, "loadCommits must return nil when source is nil")
}

func TestModel_LoadCommits_ReturnsCmdWhenApplicable(t *testing.T) {
	fake := &fakeCommitLog{fn: func(string) ([]git.CommitInfo, error) {
		return []git.CommitInfo{{Hash: "abc"}, {Hash: "def"}}, nil
	}}
	m := testModel(nil, nil)
	m.commits.source = fake
	m.commits.applicable = true
	m.session.ref = "HEAD~2"
	m.commits.loadSeq = 7

	cmd := m.loadCommits()
	require.NotNil(t, cmd, "loadCommits must return a command when applicable and source is set")

	msg := cmd()
	cmsg, ok := msg.(commitsLoadedMsg)
	require.True(t, ok, "command must emit a commitsLoadedMsg")
	assert.Equal(t, uint64(7), cmsg.seq, "captured seq must be on the message")
	assert.Len(t, cmsg.list, 2)
	assert.Equal(t, "abc", cmsg.list[0].Hash)
	assert.False(t, cmsg.truncated, "under MaxCommits must not be truncated")
	require.NoError(t, cmsg.err)
	assert.Equal(t, "HEAD~2", fake.lastRef, "CommitLog must be called with the captured ref")
}

func TestModel_LoadCommits_PropagatesError(t *testing.T) {
	boom := errors.New("vcs blew up")
	fake := &fakeCommitLog{fn: func(string) ([]git.CommitInfo, error) {
		return nil, boom
	}}
	m := testModel(nil, nil)
	m.commits.source = fake
	m.commits.applicable = true
	m.session.ref = "bad"

	cmd := m.loadCommits()
	require.NotNil(t, cmd)
	msg := cmd()
	cmsg, ok := msg.(commitsLoadedMsg)
	require.True(t, ok)
	require.Error(t, cmsg.err)
	assert.Equal(t, boom, cmsg.err)
	assert.Empty(t, cmsg.list)
	assert.False(t, cmsg.truncated)
}

func TestModel_LoadCommits_TruncatedFlag(t *testing.T) {
	full := make([]git.CommitInfo, git.MaxCommits)
	fake := &fakeCommitLog{fn: func(string) ([]git.CommitInfo, error) { return full, nil }}
	m := testModel(nil, nil)
	m.commits.source = fake
	m.commits.applicable = true

	cmd := m.loadCommits()
	require.NotNil(t, cmd)
	cmsg := cmd().(commitsLoadedMsg)
	assert.True(t, cmsg.truncated, "exactly MaxCommits results must mark truncated")
}

func TestModel_HandleCommitsLoaded_PopulatesState(t *testing.T) {
	m := testModel(nil, nil)
	m.commits.loadSeq = 3
	m.commits.loaded = false
	m.commits.list = nil
	m.commits.err = nil
	m.commits.truncated = false

	list := []git.CommitInfo{{Hash: "abc"}, {Hash: "def"}}
	result, cmd := m.Update(commitsLoadedMsg{seq: 3, list: list, truncated: true})
	model := result.(Model)

	assert.Nil(t, cmd)
	assert.True(t, model.commits.loaded, "loaded must flip to true after matching seq")
	assert.Equal(t, list, model.commits.list)
	assert.True(t, model.commits.truncated)
	require.NoError(t, model.commits.err)
}

func TestModel_HandleCommitsLoaded_DropsStaleResult(t *testing.T) {
	// a slow commit fetch (seq=0) must not overwrite state after a
	// newer load (seq=1) was issued - e.g. user pressed R immediately after startup.
	m := testModel(nil, nil)
	m.commits.loadSeq = 1 // simulate a newer load already dispatched (e.g. triggerReload)
	m.commits.loaded = false
	m.commits.list = nil

	stale := []git.CommitInfo{{Hash: "stale"}}
	result, cmd := m.Update(commitsLoadedMsg{seq: 0, list: stale})
	model := result.(Model)

	assert.Nil(t, cmd)
	assert.False(t, model.commits.loaded, "stale result must not flip loaded")
	assert.Nil(t, model.commits.list, "stale result must not populate list")
}

func TestModel_HandleCommitsLoaded_SetsLoadedOnError(t *testing.T) {
	m := testModel(nil, nil)
	m.commits.loadSeq = 5
	m.commits.loaded = false

	boom := errors.New("vcs blew up")
	result, cmd := m.Update(commitsLoadedMsg{seq: 5, err: boom})
	model := result.(Model)

	assert.Nil(t, cmd)
	assert.True(t, model.commits.loaded, "error result must still mark loaded=true to cache the failure")
	assert.Equal(t, boom, model.commits.err)
	assert.Empty(t, model.commits.list)
}

func TestModel_TriggerReload_RefetchesCommits(t *testing.T) {
	fake := &fakeCommitLog{fn: func(string) ([]git.CommitInfo, error) {
		return []git.CommitInfo{{Hash: "fresh"}}, nil
	}}
	m := testNewModel(t, plainRenderer(), annot.NewStore(), noopHighlighter(), ModelConfig{})
	m.commits.source = fake
	m.commits.applicable = true
	m.commits.loaded = true
	m.commits.list = []git.CommitInfo{{Hash: "stale"}}
	oldSeq := m.commits.loadSeq

	cmd := m.triggerReload()

	assert.False(t, m.commits.loaded, "triggerReload must invalidate commit cache")
	assert.Nil(t, m.commits.list, "triggerReload must clear commit list")
	assert.Equal(t, oldSeq+1, m.commits.loadSeq, "triggerReload must bump commits.loadSeq")
	require.NotNil(t, cmd, "triggerReload must return a non-nil batch cmd")

	// execute the batch and find the commitsLoadedMsg to verify the refetch actually runs
	batch, ok := cmd().(tea.BatchMsg)
	require.True(t, ok, "triggerReload must return a tea.BatchMsg when both loaders are active")
	var gotCommits *commitsLoadedMsg
	for _, inner := range batch {
		if msg, ok := inner().(commitsLoadedMsg); ok {
			gotCommits = &msg
			break
		}
	}
	require.NotNil(t, gotCommits, "batch must include a commitsLoadedMsg")
	assert.Equal(t, 1, fake.calls, "CommitLog must be called once by the refetch")
	assert.Equal(t, []git.CommitInfo{{Hash: "fresh"}}, gotCommits.list, "refetched commits must be fresh, not stale")
	assert.Equal(t, oldSeq+1, gotCommits.seq, "refetched commits must carry the bumped seq")
}

func TestModel_TriggerReload_BumpsSeqAndCallsLoadFiles(t *testing.T) {
	callCount := 0
	renderer := &mocks.DiffSourceMock{
		ChangedFilesFunc: func(ref string, staged bool) ([]git.FileEntry, error) {
			callCount++
			return []git.FileEntry{{Path: "main.go"}}, nil
		},
		FileDiffFunc: func(git.FileDiffRequest) ([]git.DiffLine, error) {
			return nil, nil
		},
	}
	m := testNewModel(t, renderer, annot.NewStore(), noopHighlighter(), ModelConfig{})
	initialSeq := m.filesLoadSeq

	cmd := m.triggerReload()
	assert.Equal(t, initialSeq+1, m.filesLoadSeq, "triggerReload must bump filesLoadSeq")
	assert.NotNil(t, cmd, "triggerReload must return a loadFiles command")

	// execute the command to confirm it calls ChangedFiles
	msg := cmd()
	_, ok := msg.(filesLoadedMsg)
	assert.True(t, ok, "triggerReload command must emit filesLoadedMsg")
	assert.Equal(t, 1, callCount, "triggerReload must trigger ChangedFiles")
}

func TestModel_RecomputeIntraRanges(t *testing.T) {
	m := testModel(nil, nil)
	m.modes.wordDiff = true
	m.cfg.tabSpaces = "    "
	m.file.lines = []git.DiffLine{
		{Content: "context before", ChangeType: git.ChangeContext},
		{Content: "return foo(bar)", ChangeType: git.ChangeRemove},
		{Content: "return foo(baz)", ChangeType: git.ChangeAdd},
		{Content: "context after", ChangeType: git.ChangeContext},
	}

	m.recomputeIntraRanges()

	require.Len(t, m.file.intraRanges, 4)
	assert.Nil(t, m.file.intraRanges[0], "context line should have no ranges")
	assert.NotNil(t, m.file.intraRanges[1], "remove line should have ranges")
	assert.NotNil(t, m.file.intraRanges[2], "add line should have ranges")
	assert.Nil(t, m.file.intraRanges[3], "context line should have no ranges")

	// verify the ranges point to "bar" and "baz"
	require.Len(t, m.file.intraRanges[1], 1)
	assert.Equal(t, worddiff.Range{Start: 11, End: 14}, m.file.intraRanges[1][0])
	require.Len(t, m.file.intraRanges[2], 1)
	assert.Equal(t, worddiff.Range{Start: 11, End: 14}, m.file.intraRanges[2][0])
}

func TestModel_RecomputeIntraRanges_IdenticalPair(t *testing.T) {
	m := testModel(nil, nil)
	m.modes.wordDiff = true
	m.cfg.tabSpaces = "    "
	m.file.lines = []git.DiffLine{
		{Content: "same line content", ChangeType: git.ChangeRemove},
		{Content: "same line content", ChangeType: git.ChangeAdd},
	}

	m.recomputeIntraRanges()

	// identical lines produce no changed ranges, so intra-line ranges remain nil
	assert.Nil(t, m.file.intraRanges[0], "identical remove should have no ranges")
	assert.Nil(t, m.file.intraRanges[1], "identical add should have no ranges")
}

func TestModel_RecomputeIntraRanges_PureAddBlock(t *testing.T) {
	m := testModel(nil, nil)
	m.modes.wordDiff = true
	m.cfg.tabSpaces = "    "
	m.file.lines = []git.DiffLine{
		{Content: "context", ChangeType: git.ChangeContext},
		{Content: "new line 1", ChangeType: git.ChangeAdd},
		{Content: "new line 2", ChangeType: git.ChangeAdd},
	}

	m.recomputeIntraRanges()

	// pure add block has no pairs, so no intra-line ranges
	for i, r := range m.file.intraRanges {
		assert.Nil(t, r, "line %d should have no ranges", i)
	}
}

func TestModel_RecomputeIntraRanges_DissimilarPair(t *testing.T) {
	m := testModel(nil, nil)
	m.modes.wordDiff = true
	m.cfg.tabSpaces = "    "
	m.file.lines = []git.DiffLine{
		{Content: "alpha bravo charlie delta echo foxtrot golf hotel india juliet", ChangeType: git.ChangeRemove},
		{Content: "xxx yyy zzz aaa bbb ccc ddd eee fff ggg", ChangeType: git.ChangeAdd},
	}

	m.recomputeIntraRanges()

	// dissimilar pair should have no ranges due to similarity gate
	assert.Nil(t, m.file.intraRanges[0])
	assert.Nil(t, m.file.intraRanges[1])
}

func TestModel_RecomputeIntraRanges_TabContent(t *testing.T) {
	m := testModel(nil, nil)
	m.modes.wordDiff = true
	m.cfg.tabSpaces = "    "
	m.file.lines = []git.DiffLine{
		{Content: "\treturn foo(bar)", ChangeType: git.ChangeRemove},
		{Content: "\treturn foo(baz)", ChangeType: git.ChangeAdd},
	}

	m.recomputeIntraRanges()

	// ranges should be on tab-replaced content
	require.NotNil(t, m.file.intraRanges[0])
	require.NotNil(t, m.file.intraRanges[1])

	// after tab replacement, "\t" becomes "    " (4 spaces), so "bar" starts at 4+11=15
	tabReplaced := strings.ReplaceAll(m.file.lines[0].Content, "\t", m.cfg.tabSpaces)
	require.Len(t, m.file.intraRanges[0], 1)
	changed := tabReplaced[m.file.intraRanges[0][0].Start:m.file.intraRanges[0][0].End]
	assert.Equal(t, "bar", changed)
}

func TestModel_RecomputeIntraRanges_MultipleBlocks(t *testing.T) {
	m := testModel(nil, nil)
	m.modes.wordDiff = true
	m.cfg.tabSpaces = "    "
	m.file.lines = []git.DiffLine{
		{Content: "old first", ChangeType: git.ChangeRemove},
		{Content: "new first", ChangeType: git.ChangeAdd},
		{Content: "context between", ChangeType: git.ChangeContext},
		{Content: "old second", ChangeType: git.ChangeRemove},
		{Content: "new second", ChangeType: git.ChangeAdd},
	}

	m.recomputeIntraRanges()

	// both blocks should have ranges
	assert.NotNil(t, m.file.intraRanges[0], "first block remove")
	assert.NotNil(t, m.file.intraRanges[1], "first block add")
	assert.Nil(t, m.file.intraRanges[2], "context line")
	assert.NotNil(t, m.file.intraRanges[3], "second block remove")
	assert.NotNil(t, m.file.intraRanges[4], "second block add")
}

func TestHandleFileLoaded_SingleColLineNum_FullContext(t *testing.T) {
	lines := []git.DiffLine{
		{OldNum: 1, NewNum: 1, Content: "package main", ChangeType: git.ChangeContext},
		{OldNum: 2, NewNum: 2, Content: "// comment", ChangeType: git.ChangeContext},
		{OldNum: 3, NewNum: 3, Content: "func main() {}", ChangeType: git.ChangeContext},
	}
	m := testModel(nil, nil)
	m.modes.lineNumbers = true

	result, _ := m.Update(fileLoadedMsg{file: "a.go", lines: lines})
	model := result.(Model)

	assert.True(t, model.file.singleColLineNum, "full-context file should set singleColLineNum to true")
	assert.Equal(t, model.file.lineNumWidth+1, model.lineNumGutterWidth(), "full-context should use single-column gutter width")
}

func TestHandleFileLoaded_SingleColLineNum_RealDiff(t *testing.T) {
	lines := []git.DiffLine{
		{OldNum: 1, NewNum: 1, Content: "package main", ChangeType: git.ChangeContext},
		{OldNum: 2, Content: "old line", ChangeType: git.ChangeRemove},
		{NewNum: 2, Content: "new line", ChangeType: git.ChangeAdd},
		{OldNum: 3, NewNum: 3, Content: "// end", ChangeType: git.ChangeContext},
	}
	m := testModel(nil, nil)
	m.modes.lineNumbers = true

	result, _ := m.Update(fileLoadedMsg{file: "a.go", lines: lines})
	model := result.(Model)

	assert.False(t, model.file.singleColLineNum, "file with add/remove lines should set singleColLineNum to false")
}

func TestModel_CurrentContextLines(t *testing.T) {
	tests := []struct {
		name       string
		compact    bool
		ctx        int
		applicable bool
		want       int
	}{
		{name: "compact off, applicable", compact: false, ctx: 5, applicable: true, want: 0},
		{name: "compact off, not applicable", compact: false, ctx: 5, applicable: false, want: 0},
		{name: "compact on, applicable", compact: true, ctx: 5, applicable: true, want: 5},
		{name: "compact on, not applicable", compact: true, ctx: 5, applicable: false, want: 0},
		{name: "compact on, custom ctx", compact: true, ctx: 10, applicable: true, want: 10},
		{name: "compact on, zero ctx", compact: true, ctx: 0, applicable: true, want: 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := testModel([]string{"a.go"}, nil)
			m.modes.compact = tc.compact
			m.modes.compactContext = tc.ctx
			m.compact.applicable = tc.applicable
			assert.Equal(t, tc.want, m.currentContextLines())
		})
	}
}

func TestModel_LoadFileDiffPassesContextLines(t *testing.T) {
	var captured int
	renderer := &mocks.DiffSourceMock{
		ChangedFilesFunc: func(ref string, staged bool) ([]git.FileEntry, error) { return nil, nil },
		FileDiffFunc: func(req git.FileDiffRequest) ([]git.DiffLine, error) {
			captured = req.ContextLines
			return nil, nil
		},
	}
	m := testModel([]string{"a.go"}, nil)
	m.diffSource = renderer
	m.modes.compact = true
	m.modes.compactContext = 7
	m.compact.applicable = true

	cmd := m.loadFileDiff("a.go")
	require.NotNil(t, cmd)
	cmd() // executes and records contextLines
	assert.Equal(t, 7, captured, "compact mode should pass compactContext to FileDiff")
}

func TestModel_LoadFileDiffPassesZeroWhenNotApplicable(t *testing.T) {
	var captured = -1
	renderer := &mocks.DiffSourceMock{
		ChangedFilesFunc: func(ref string, staged bool) ([]git.FileEntry, error) { return nil, nil },
		FileDiffFunc: func(req git.FileDiffRequest) ([]git.DiffLine, error) {
			captured = req.ContextLines
			return nil, nil
		},
	}
	m := testModel([]string{"a.go"}, nil)
	m.diffSource = renderer
	m.modes.compact = true
	m.modes.compactContext = 5
	m.compact.applicable = false

	cmd := m.loadFileDiff("a.go")
	require.NotNil(t, cmd)
	cmd()
	assert.Equal(t, 0, captured, "non-applicable compact mode must still pass 0 (full file)")
}

func TestModel_ReloadCurrentFileBumpsLoadSeqAndFetches(t *testing.T) {
	var calls int
	renderer := &mocks.DiffSourceMock{
		ChangedFilesFunc: func(ref string, staged bool) ([]git.FileEntry, error) { return nil, nil },
		FileDiffFunc: func(req git.FileDiffRequest) ([]git.DiffLine, error) {
			calls++
			return nil, nil
		},
	}
	m := testModel([]string{"a.go"}, nil)
	m.diffSource = renderer
	m.file.name = "a.go"
	beforeSeq := m.file.loadSeq

	cmd := m.reloadCurrentFile()
	require.NotNil(t, cmd)
	assert.Greater(t, m.file.loadSeq, beforeSeq, "reloadCurrentFile must bump file.loadSeq to invalidate prior in-flight loads")
	cmd()
	assert.Equal(t, 1, calls, "reloadCurrentFile command must invoke FileDiff once")
}

func TestModel_ReloadCurrentFileNoOpWhenEmpty(t *testing.T) {
	m := testModel([]string{"a.go"}, nil)
	m.file.name = ""
	beforeSeq := m.file.loadSeq

	cmd := m.reloadCurrentFile()
	assert.Nil(t, cmd, "reloadCurrentFile must be a no-op when no file is loaded")
	assert.Equal(t, beforeSeq, m.file.loadSeq, "no load implies no seq bump")
}

func TestModel_CaptureCompactAnchor(t *testing.T) {
	lines := []git.DiffLine{
		{NewNum: 10, Content: "ctx", ChangeType: git.ChangeContext},
		{OldNum: 11, Content: "removed", ChangeType: git.ChangeRemove},
		{NewNum: 12, Content: "added", ChangeType: git.ChangeAdd},
	}

	t.Run("nil when no file loaded", func(t *testing.T) {
		m := testModel([]string{"a.go"}, nil)
		m.file.name = ""
		m.file.lines = lines
		m.nav.diffCursor = 1
		assert.Nil(t, m.captureCompactAnchor())
	})

	t.Run("nil when cursor out of range", func(t *testing.T) {
		m := testModel([]string{"a.go"}, nil)
		m.file.name = "a.go"
		m.file.lines = lines
		m.nav.diffCursor = 99
		assert.Nil(t, m.captureCompactAnchor())
	})

	t.Run("nil when cursor negative", func(t *testing.T) {
		m := testModel([]string{"a.go"}, nil)
		m.file.name = "a.go"
		m.file.lines = lines
		m.nav.diffCursor = -1
		assert.Nil(t, m.captureCompactAnchor())
	})

	t.Run("captures added line by new number", func(t *testing.T) {
		m := testModel([]string{"a.go"}, nil)
		m.file.name = "a.go"
		m.file.lines = lines
		m.nav.diffCursor = 2
		a := m.captureCompactAnchor()
		require.NotNil(t, a)
		assert.Equal(t, 12, a.srcLine)
		assert.Equal(t, git.ChangeAdd, a.changeType)
		assert.Equal(t, 0, a.hunkIdx) // single hunk starts at index 1
	})

	t.Run("captures removed line by old number", func(t *testing.T) {
		m := testModel([]string{"a.go"}, nil)
		m.file.name = "a.go"
		m.file.lines = lines
		m.nav.diffCursor = 1
		a := m.captureCompactAnchor()
		require.NotNil(t, a)
		assert.Equal(t, 11, a.srcLine)
		assert.Equal(t, git.ChangeRemove, a.changeType)
	})
}

func TestModel_ApplyCompactAnchor(t *testing.T) {
	t.Run("restores cursor to matching change line", func(t *testing.T) {
		m := testModel([]string{"a.go"}, nil)
		m.file.name = "a.go"
		m.file.lines = []git.DiffLine{
			{ChangeType: git.ChangeDivider},
			{NewNum: 40, Content: "c", ChangeType: git.ChangeContext},
			{NewNum: 41, Content: "add", ChangeType: git.ChangeAdd},
		}
		m.applyCompactAnchor(&compactAnchor{srcLine: 41, changeType: git.ChangeAdd, hunkIdx: 0})
		assert.Equal(t, 2, m.nav.diffCursor)
	})

	t.Run("falls back to nearest hunk when line absent", func(t *testing.T) {
		m := testModel([]string{"a.go"}, nil)
		m.file.name = "a.go"
		m.file.lines = []git.DiffLine{
			{ChangeType: git.ChangeDivider},
			{NewNum: 40, Content: "c", ChangeType: git.ChangeContext},
			{NewNum: 41, Content: "add", ChangeType: git.ChangeAdd},
		}
		// context line NewNum 5 not present -> fall back to hunk 0 (index 2)
		m.applyCompactAnchor(&compactAnchor{srcLine: 5, changeType: git.ChangeContext, hunkIdx: 0})
		assert.Equal(t, 2, m.nav.diffCursor)
	})

	t.Run("divider anchor skips line lookup and uses hunk", func(t *testing.T) {
		m := testModel([]string{"a.go"}, nil)
		m.file.name = "a.go"
		m.file.lines = []git.DiffLine{
			{ChangeType: git.ChangeDivider},
			{NewNum: 40, Content: "c", ChangeType: git.ChangeContext},
			{NewNum: 41, Content: "add", ChangeType: git.ChangeAdd},
		}
		// hunk 0 starts at index 2. skipInitialDividers would land on index 1
		// (the context line), so cursor==2 proves the hunk-fallback branch ran
		m.applyCompactAnchor(&compactAnchor{srcLine: 0, changeType: git.ChangeDivider, hunkIdx: 0})
		assert.Equal(t, 2, m.nav.diffCursor)
	})

	t.Run("skips to first visible line when no hunks", func(t *testing.T) {
		m := testModel([]string{"a.go"}, nil)
		m.file.name = "a.go"
		m.file.lines = []git.DiffLine{
			{ChangeType: git.ChangeDivider},
			{NewNum: 40, Content: "c", ChangeType: git.ChangeContext},
			{NewNum: 41, Content: "d", ChangeType: git.ChangeContext},
		}
		m.applyCompactAnchor(&compactAnchor{srcLine: 999, changeType: git.ChangeContext, hunkIdx: -1})
		assert.Equal(t, 1, m.nav.diffCursor) // skipInitialDividers lands on first non-divider
	})

	t.Run("restores cursor to matching removed line by old number", func(t *testing.T) {
		m := testModel([]string{"a.go"}, nil)
		m.file.name = "a.go"
		m.file.lines = []git.DiffLine{
			{ChangeType: git.ChangeDivider},
			{NewNum: 40, Content: "c", ChangeType: git.ChangeContext},
			{OldNum: 88, Content: "removed", ChangeType: git.ChangeRemove},
			{NewNum: 41, Content: "added", ChangeType: git.ChangeAdd},
		}
		// findDiffLineIndex resolves a remove anchor by OldNum, not NewNum
		m.applyCompactAnchor(&compactAnchor{srcLine: 88, changeType: git.ChangeRemove, hunkIdx: 0})
		assert.Equal(t, 2, m.nav.diffCursor)
	})

	t.Run("collapsed mode keeps restored cursor visible", func(t *testing.T) {
		m := testModel([]string{"a.go"}, nil)
		m.file.name = "a.go"
		m.modes.collapsed.enabled = true
		m.file.lines = []git.DiffLine{
			{ChangeType: git.ChangeDivider},
			{NewNum: 40, Content: "c", ChangeType: git.ChangeContext},
			{OldNum: 88, Content: "removed", ChangeType: git.ChangeRemove},
			{NewNum: 41, Content: "added", ChangeType: git.ChangeAdd},
		}
		// the removed line (index 2) is collapsed-hidden. adjustCursorIfHidden must
		// nudge the cursor to the nearest visible line (the added line at index 3)
		m.applyCompactAnchor(&compactAnchor{srcLine: 88, changeType: git.ChangeRemove, hunkIdx: 0})
		assert.Equal(t, 3, m.nav.diffCursor)
		assert.False(t, m.isCollapsedHidden(m.nav.diffCursor, m.findHunks()),
			"restored cursor must be on a visible line in collapsed mode")
	})
}

func TestModel_HandleFileLoaded_DropsStaleCompactAnchor(t *testing.T) {
	m := testModel([]string{"a.go"}, nil)
	m.file.name = "a.go"
	m.file.loadSeq = 5
	// stale anchor: its seq predates the current load, so the seq guard must drop
	// it and fall through to the default top positioning rather than reposition
	m.compact.pendingAnchor = &compactAnchor{seq: 4, srcLine: 41, changeType: git.ChangeAdd, hunkIdx: 0}
	msg := fileLoadedMsg{file: "a.go", seq: 5, lines: []git.DiffLine{
		{ChangeType: git.ChangeDivider},
		{NewNum: 40, Content: "c", ChangeType: git.ChangeContext},
		{NewNum: 41, Content: "add", ChangeType: git.ChangeAdd},
	}}
	result, _ := m.handleFileLoaded(msg)
	model := result.(Model)
	assert.Nil(t, model.compact.pendingAnchor, "stale anchor must be cleared")
	assert.Equal(t, 1, model.nav.diffCursor, "stale anchor must not reposition, cursor resets to first visible line")
}

func TestModel_HandleFileLoaded_PopulatesLineWidths(t *testing.T) {
	m := testModel([]string{"a.go"}, nil)
	m.file.name = "a.go"
	msg := fileLoadedMsg{file: "a.go", seq: 0, lines: []git.DiffLine{
		{NewNum: 1, Content: "abc", ChangeType: git.ChangeContext},
		{NewNum: 2, Content: "much longer line here", ChangeType: git.ChangeAdd},
	}}
	result, _ := m.handleFileLoaded(msg)
	model := result.(Model)

	require.Len(t, model.file.lineWidths, 2, "widths must stay parallel to lines")
	assert.Equal(t, changePrefixWidth+3, model.file.lineWidths[0])
	assert.Equal(t, changePrefixWidth+21, model.file.lineWidths[1])
}

func TestModel_LoadFilesReconcilesReviewedFingerprints(t *testing.T) {
	entry := git.FileEntry{Path: "a.go", Status: git.FileModified}
	original := []git.DiffLine{
		{OldNum: 1, NewNum: 1, Content: "context", ChangeType: git.ChangeContext},
		{OldNum: 2, Content: "old", ChangeType: git.ChangeRemove},
		{NewNum: 2, Content: "new", ChangeType: git.ChangeAdd},
	}

	setup := func(t *testing.T, entries []git.FileEntry, fileDiff func(git.FileDiffRequest) ([]git.DiffLine, error)) Model {
		t.Helper()
		renderer := &mocks.DiffSourceMock{
			ChangedFilesFunc: func(string, bool) ([]git.FileEntry, error) { return entries, nil },
			FileDiffFunc:     fileDiff,
		}
		m := testNewModel(t, renderer, annot.NewStore(), noopHighlighter(), ModelConfig{})
		m.tree.Rebuild([]git.FileEntry{entry})
		m.tree.SetReviewed(entry.Path, git.FileFingerprint(entry, original))
		return m
	}

	t.Run("unchanged semantic patch survives shifted lines and context", func(t *testing.T) {
		shifted := []git.DiffLine{
			{ChangeType: git.ChangeDivider, Content: "shifted"},
			{OldNum: 100, NewNum: 100, Content: "new upstream context", ChangeType: git.ChangeContext},
			{OldNum: 101, Content: "old", ChangeType: git.ChangeRemove},
			{NewNum: 101, Content: "new", ChangeType: git.ChangeAdd},
		}
		m := setup(t, []git.FileEntry{entry}, func(git.FileDiffRequest) ([]git.DiffLine, error) { return shifted, nil })

		result, _ := m.Update(m.loadFiles()())
		model := result.(Model)

		assert.True(t, model.tree.IsReviewed("a.go"))
	})

	t.Run("changed patch clears reviewed state", func(t *testing.T) {
		changed := append([]git.DiffLine(nil), original...)
		changed[2].Content = "newer"
		m := setup(t, []git.FileEntry{entry}, func(git.FileDiffRequest) ([]git.DiffLine, error) { return changed, nil })

		result, _ := m.Update(m.loadFiles()())
		model := result.(Model)

		assert.False(t, model.tree.IsReviewed("a.go"))
	})

	t.Run("missing file clears reviewed state", func(t *testing.T) {
		m := setup(t, nil, func(git.FileDiffRequest) ([]git.DiffLine, error) {
			t.Fatal("removed reviewed file must not fetch a diff")
			return nil, nil
		})

		result, _ := m.Update(m.loadFiles()())
		model := result.(Model)

		assert.False(t, model.tree.IsReviewed("a.go"))
	})

	t.Run("fingerprint error fails closed", func(t *testing.T) {
		m := setup(t, []git.FileEntry{entry}, func(git.FileDiffRequest) ([]git.DiffLine, error) {
			return nil, errors.New("diff unavailable")
		})

		msg := m.loadFiles()().(filesLoadedMsg)
		require.Len(t, msg.warnings, 1)
		result, _ := m.Update(msg)
		model := result.(Model)

		assert.False(t, model.tree.IsReviewed("a.go"))
	})

	t.Run("opaque binary diff clears reviewed state", func(t *testing.T) {
		binary := []git.DiffLine{{Content: git.BinaryPlaceholder, IsBinary: true}}
		m := setup(t, []git.FileEntry{entry}, func(git.FileDiffRequest) ([]git.DiffLine, error) {
			return binary, nil
		})
		m.tree.SetReviewed(entry.Path, git.FileFingerprint(entry, binary))

		result, _ := m.Update(m.loadFiles()())
		model := result.(Model)

		assert.False(t, model.tree.IsReviewed("a.go"))
	})
}

func TestModel_LoadFilesFingerprintsOnlyReviewedPaths(t *testing.T) {
	entries := []git.FileEntry{
		{Path: "a.go", Status: git.FileModified},
		{Path: "b.go", Status: git.FileModified},
		{Path: "c.go", Status: git.FileModified},
		{Path: "d.go", Status: git.FileModified},
		{Path: "e.go", Status: git.FileModified},
		{Path: "unreviewed.go", Status: git.FileModified},
	}
	var mu sync.Mutex
	var requested []string
	renderer := &mocks.DiffSourceMock{
		ChangedFilesFunc: func(string, bool) ([]git.FileEntry, error) { return entries, nil },
		FileDiffFunc: func(req git.FileDiffRequest) ([]git.DiffLine, error) {
			mu.Lock()
			requested = append(requested, req.Path)
			mu.Unlock()
			return []git.DiffLine{{Content: req.Path, ChangeType: git.ChangeAdd}}, nil
		},
	}
	m := testNewModel(t, renderer, annot.NewStore(), noopHighlighter(), ModelConfig{})
	m.tree.Rebuild(entries)
	for _, path := range []string{"a.go", "b.go", "c.go", "d.go", "e.go"} {
		m.tree.SetReviewed(path, "old-fingerprint")
	}

	_ = m.loadFiles()()

	mu.Lock()
	defer mu.Unlock()
	assert.ElementsMatch(t, []string{"a.go", "b.go", "c.go", "d.go", "e.go"}, requested)
}

func TestModel_FetchEffectiveFileDiffStrictUntrackedError(t *testing.T) {
	renderer := &mocks.DiffSourceMock{
		ChangedFilesFunc: func(string, bool) ([]git.FileEntry, error) { return nil, nil },
		FileDiffFunc:     func(git.FileDiffRequest) ([]git.DiffLine, error) { return nil, nil },
	}
	m := testNewModel(t, renderer, annot.NewStore(), noopHighlighter(), ModelConfig{WorkDir: t.TempDir()})

	_, err := m.fetchEffectiveFileDiff(git.FileEntry{Path: "missing.bin", Status: git.FileUntracked}, 0, true)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "read untracked file missing.bin")
}

func TestModel_LoadFilesPreservesMarkAddedAfterReviewedSnapshot(t *testing.T) {
	entry := git.FileEntry{Path: "a.go", Status: git.FileModified}
	lines := []git.DiffLine{{Content: "new", ChangeType: git.ChangeAdd}}
	renderer := &mocks.DiffSourceMock{
		ChangedFilesFunc: func(string, bool) ([]git.FileEntry, error) { return []git.FileEntry{entry}, nil },
		FileDiffFunc:     func(git.FileDiffRequest) ([]git.DiffLine, error) { return lines, nil },
	}
	m := testNewModel(t, renderer, annot.NewStore(), noopHighlighter(), ModelConfig{})
	m.tree.Rebuild([]git.FileEntry{entry})
	m.file.name = entry.Path
	m.file.lines = lines
	loadCmd := m.loadFiles() // captures an empty reviewed snapshot

	result, _ := m.handleMarkReviewed()
	m = result.(Model)
	require.True(t, m.tree.IsReviewed(entry.Path))
	result, _ = m.Update(loadCmd())
	m = result.(Model)

	assert.True(t, m.tree.IsReviewed(entry.Path), "fresh mark must survive reconciliation of the older snapshot")
}

func TestModel_FilesReloadPreservesUnreviewedAutoAdvanceSelection(t *testing.T) {
	entries := []git.FileEntry{{Path: "a.go"}, {Path: "b.go"}, {Path: "c.go"}}
	lines := []git.DiffLine{{Content: "changed", ChangeType: git.ChangeAdd}}
	renderer := &mocks.DiffSourceMock{
		ChangedFilesFunc: func(string, bool) ([]git.FileEntry, error) { return entries, nil },
		FileDiffFunc:     func(git.FileDiffRequest) ([]git.DiffLine, error) { return lines, nil },
	}
	m := testNewModel(t, renderer, annot.NewStore(), noopHighlighter(), ModelConfig{})
	m.tree.Rebuild(entries)
	m.tree.ToggleUnreviewedFilter()
	require.True(t, m.tree.SelectByPath("b.go"))
	m.file.name = "b.go"
	m.file.lines = lines

	reloadCmd := m.loadFiles() // captures reviewed state before b.go is marked
	result, advanceCmd := m.handleMarkReviewed()
	m = result.(Model)
	require.NotNil(t, advanceCmd)
	require.Equal(t, "c.go", m.tree.SelectedFile())

	result, followupCmd := m.Update(reloadCmd())
	m = result.(Model)

	assert.Equal(t, "c.go", m.tree.SelectedFile(), "reload must not jump back to the first unreviewed file")
	assert.NotNil(t, followupCmd, "reload should issue a fresh load for the preserved selection")
}

func TestModel_FilesReloadPreservesUnreviewedScrollOffset(t *testing.T) {
	entries := []git.FileEntry{
		{Path: "a.go"}, {Path: "b.go"}, {Path: "c.go"}, {Path: "d.go"}, {Path: "e.go"},
		{Path: "f.go"}, {Path: "g.go"}, {Path: "h.go"}, {Path: "i.go"},
	}
	m := testModel(nil, nil)
	m.tree.Rebuild(entries)
	m.tree.SetReviewed("a.go", "fp-a")
	m.tree.SetReviewed("b.go", "fp-b")
	reviewed := map[string]string{"a.go": "fp-a", "b.go": "fp-b"}
	m.tree.ToggleUnreviewedFilter()
	require.True(t, m.tree.SelectByPath("h.go"))
	m.tree.EnsureVisible(5)
	m.tree.Move(sidepane.MotionUp)
	m.tree.Move(sidepane.MotionUp)
	require.Equal(t, "f.go", m.tree.SelectedFile())
	require.True(t, m.tree.SelectByVisibleRow(2))
	require.Equal(t, "f.go", m.tree.SelectedFile(), "selected file starts in the middle of the viewport")

	m.file.name = "f.go"
	for range 3 {
		m.triggerReload()
		result, _ := m.Update(filesLoadedMsg{
			seq: m.filesLoadSeq, entries: entries,
			reviewedBefore: reviewed, reviewedFingerprints: reviewed,
		})
		m = result.(Model)
		m.tree.EnsureVisible(5)

		assert.Equal(t, "f.go", m.tree.SelectedFile())
		require.True(t, m.tree.SelectByVisibleRow(2))
		require.Equal(t, "f.go", m.tree.SelectedFile(), "repeated reloads should keep the selected file on the same row")
	}
}

func TestModel_FilesReloadPreservesVisibleRowWhenFilesAboveChange(t *testing.T) {
	entries := []git.FileEntry{
		{Path: "a.go"}, {Path: "b.go"}, {Path: "c.go"}, {Path: "d.go"}, {Path: "e.go"},
		{Path: "f.go"}, {Path: "g.go"}, {Path: "h.go"}, {Path: "i.go"},
	}
	m := testModel(nil, nil)
	m.tree.Rebuild(entries)
	m.tree.ToggleUnreviewedFilter()
	require.True(t, m.tree.SelectByPath("h.go"))
	m.tree.EnsureVisible(5)
	m.tree.Move(sidepane.MotionUp)
	m.tree.Move(sidepane.MotionUp)
	require.Equal(t, "f.go", m.tree.SelectedFile())
	require.True(t, m.tree.SelectByVisibleRow(2))
	require.Equal(t, "f.go", m.tree.SelectedFile(), "selected file starts in the middle of the viewport")

	m.file.name = "f.go"
	m.triggerReload()
	reloaded := []git.FileEntry{
		{Path: "a.go"}, {Path: "d.go"}, {Path: "e.go"}, {Path: "f.go"},
		{Path: "g.go"}, {Path: "h.go"}, {Path: "i.go"},
	}
	result, _ := m.Update(filesLoadedMsg{seq: m.filesLoadSeq, entries: reloaded})
	m = result.(Model)
	m.tree.EnsureVisible(5)

	assert.Equal(t, "f.go", m.tree.SelectedFile())
	require.True(t, m.tree.SelectByVisibleRow(2))
	require.Equal(t, "f.go", m.tree.SelectedFile(), "reload should preserve the row when files above disappear")
}

func TestModel_HandleFileLoaded_StartAtChange(t *testing.T) {
	withChange := []git.DiffLine{
		{ChangeType: git.ChangeDivider},
		{OldNum: 40, NewNum: 40, Content: "ctx", ChangeType: git.ChangeContext},
		{OldNum: 41, NewNum: 41, Content: "ctx", ChangeType: git.ChangeContext},
		{NewNum: 42, Content: "add", ChangeType: git.ChangeAdd},
		{OldNum: 42, NewNum: 43, Content: "ctx", ChangeType: git.ChangeContext},
	}

	load := func(t *testing.T, on bool, lines []git.DiffLine, prep func(m *Model)) Model {
		t.Helper()
		m := testModel([]string{"a.go"}, nil)
		m.file.name = "a.go"
		m.session.startAtChange = on
		if prep != nil {
			prep(&m)
		}
		result, _ := m.handleFileLoaded(fileLoadedMsg{file: "a.go", seq: m.file.loadSeq, lines: lines})
		return result.(Model)
	}

	t.Run("off keeps the first visible line", func(t *testing.T) {
		assert.Equal(t, 1, load(t, false, withChange, nil).nav.diffCursor)
	})

	t.Run("on lands on the first changed line", func(t *testing.T) {
		assert.Equal(t, 3, load(t, true, withChange, nil).nav.diffCursor)
	})

	t.Run("context-only file falls back to the first visible line", func(t *testing.T) {
		contextOnly := []git.DiffLine{
			{ChangeType: git.ChangeDivider},
			{OldNum: 1, NewNum: 1, Content: "ctx", ChangeType: git.ChangeContext},
			{OldNum: 2, NewNum: 2, Content: "ctx", ChangeType: git.ChangeContext},
		}
		assert.Equal(t, 1, load(t, true, contextOnly, nil).nav.diffCursor)
	})

	t.Run("empty diff leaves the cursor at zero", func(t *testing.T) {
		assert.Equal(t, 0, load(t, true, nil, nil).nav.diffCursor)
	})

	t.Run("collapsed delete-only hunk keeps its placeholder", func(t *testing.T) {
		deleteFirst := []git.DiffLine{
			{OldNum: 1, NewNum: 1, Content: "ctx", ChangeType: git.ChangeContext},
			{OldNum: 2, Content: "gone", ChangeType: git.ChangeRemove},
			{OldNum: 3, NewNum: 2, Content: "ctx", ChangeType: git.ChangeContext},
			{NewNum: 3, Content: "add", ChangeType: git.ChangeAdd},
		}
		m := load(t, true, deleteFirst, func(m *Model) { m.modes.collapsed.enabled = true })
		assert.Equal(t, 1, m.nav.diffCursor)
	})

	t.Run("pending hunk jump wins", func(t *testing.T) {
		back := false
		m := load(t, true, withChange, func(m *Model) { m.nav.pendingHunkJump = &back })
		assert.Equal(t, 3, m.nav.diffCursor)
		assert.Nil(t, m.nav.pendingHunkJump)
	})

	t.Run("compact anchor wins", func(t *testing.T) {
		m := load(t, true, withChange, func(m *Model) {
			m.compact.pendingAnchor = &compactAnchor{seq: m.file.loadSeq, srcLine: 41, changeType: git.ChangeContext, hunkIdx: 0}
		})
		assert.Equal(t, 2, m.nav.diffCursor, "anchor restores the pre-toggle line, overriding start-at-change")
	})

	t.Run("reapplies on every subsequent file load", func(t *testing.T) {
		m := load(t, true, withChange, nil)
		require.Equal(t, 3, m.nav.diffCursor)
		m.file.name = "b.go"
		m.file.loadSeq++
		result, _ := m.handleFileLoaded(fileLoadedMsg{file: "b.go", seq: m.file.loadSeq, lines: withChange})
		assert.Equal(t, 3, result.(Model).nav.diffCursor, "switching files positions again, not once per session")
	})

	t.Run("annotation jump wins", func(t *testing.T) {
		m := load(t, true, withChange, func(m *Model) {
			m.pendingAnnotJump = &annotJump{Annotation: annot.Annotation{File: "a.go", Line: 41, Type: string(git.ChangeContext)}}
		})
		assert.Equal(t, 2, m.nav.diffCursor, "annotation target overrides start-at-change")
		assert.Nil(t, m.pendingAnnotJump)
	})

	// fileLoadedMsg can arrive before the first WindowSizeMsg. the change must still be on
	// screen once the resize lands, not merely selected somewhere far below the fold
	t.Run("change is visible when the window size arrives after the load", func(t *testing.T) {
		lines := make([]git.DiffLine, 0, 401)
		for i := 1; i <= 400; i++ {
			lines = append(lines, git.DiffLine{OldNum: i, NewNum: i, Content: "ctx", ChangeType: git.ChangeContext})
		}
		lines = append(lines, git.DiffLine{NewNum: 401, Content: "add", ChangeType: git.ChangeAdd})

		m := testModel([]string{"a.go"}, nil)
		m.file.name = "a.go"
		m.session.startAtChange = true
		m.ready = false
		m.layout.viewport.Height = 0

		result, _ := m.handleFileLoaded(fileLoadedMsg{file: "a.go", seq: m.file.loadSeq, lines: lines})
		m = result.(Model)
		require.Equal(t, 400, m.nav.diffCursor, "cursor lands on the change with no viewport height yet")

		result, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
		m = result.(Model)

		cursorY, top, height := m.cursorViewportY(), m.layout.viewport.YOffset, m.layout.viewport.Height
		require.Positive(t, height)
		assert.GreaterOrEqual(t, cursorY, top, "change must not sit above the visible window")
		assert.Less(t, cursorY, top+height, "change must not sit below the visible window")
	})
}

// pins the centerViewportOnCursor call in the start-at-change branch of handleFileLoaded. it is the
// only render on the no-hunk path, and on the hunk path it is what re-applies an offset that
// centerHunkInViewport clamped against the previous file's length. deleting it as a duplicate render
// leaves the pane painting the old file.
func TestModel_StartAtChange_RendersTheLoadedFile(t *testing.T) {
	short := []git.DiffLine{{OldNum: 1, NewNum: 1, Content: "short file", ChangeType: git.ChangeContext}}

	// testModel leaves the viewport zero-sized, and a zero-height viewport renders "" - which would
	// make every assertion below pass against an unrendered pane
	newModel := func(t *testing.T, files []string) Model {
		t.Helper()
		m := testModel(files, nil)
		m.session.startAtChange = true
		m.layout.viewport.Width, m.layout.viewport.Height = 80, 20
		return m
	}

	load := func(t *testing.T, m Model, file string, lines []git.DiffLine) Model {
		t.Helper()
		m.file.name = file
		m.file.loadSeq++
		result, _ := m.handleFileLoaded(fileLoadedMsg{file: file, seq: m.file.loadSeq, lines: lines})
		return result.(Model)
	}

	t.Run("context-only file replaces the previous file's content", func(t *testing.T) {
		m := newModel(t, []string{"a.go", "b.md"})
		m = load(t, m, "a.go", []git.DiffLine{
			{OldNum: 1, NewNum: 1, Content: "first file only", ChangeType: git.ChangeContext},
		})

		m = load(t, m, "b.md", []git.DiffLine{
			{OldNum: 1, NewNum: 1, Content: "second file only", ChangeType: git.ChangeContext},
		})

		painted := m.layout.viewport.View()
		assert.Contains(t, painted, "second file only", "the pane must paint the file that just loaded")
		assert.NotContains(t, painted, "first file only", "stale content from the previous file must be gone")
	})

	t.Run("offset survives a short previous file", func(t *testing.T) {
		lines := make([]git.DiffLine, 0, 401)
		for i := 1; i <= 400; i++ {
			lines = append(lines, git.DiffLine{OldNum: i, NewNum: i, Content: "ctx", ChangeType: git.ChangeContext})
		}
		lines = append(lines, git.DiffLine{NewNum: 401, Content: "the change", ChangeType: git.ChangeAdd})

		m := newModel(t, []string{"a.go", "b.go"})
		m = load(t, m, "a.go", short)
		require.Zero(t, m.layout.viewport.YOffset, "the short file leaves the viewport at the top")

		m = load(t, m, "b.go", lines)

		require.Equal(t, 400, m.nav.diffCursor)
		assert.Positive(t, m.layout.viewport.YOffset,
			"offset must be re-applied after the new content is installed, not clamped against the short file")
		assert.Contains(t, m.layout.viewport.View(), "the change", "the change must actually be on screen")
	})
}

func TestModel_MergeInProgressReviewsTheWholeResult(t *testing.T) {
	// a conflict already resolved is staged, so it has no unstaged diff left.
	// It is still part of what the merge will commit, so the list must carry it
	unstaged := []git.FileEntry{{Path: "conflict.go", Status: git.FileUnmerged}}
	staged := []git.FileEntry{
		{Path: "resolved.go", Status: git.FileModified},
		{Path: "added.go", Status: git.FileAdded},
		{Path: "conflict.go", Status: git.FileModified}, // already listed, not duplicated
	}
	renderer := &mocks.DiffSourceMock{
		ChangedFilesFunc: func(_ string, wantStaged bool) ([]git.FileEntry, error) {
			if wantStaged {
				return staged, nil
			}
			return unstaged, nil
		},
		FileDiffFunc: func(git.FileDiffRequest) ([]git.DiffLine, error) { return nil, nil },
	}

	m := testNewModel(t, renderer, annot.NewStore(), noopHighlighter(), ModelConfig{MergeInProgress: true})
	msg, ok := m.loadFiles()().(filesLoadedMsg)
	require.True(t, ok)
	require.NoError(t, msg.err)
	paths := make([]string, 0, len(msg.entries))
	for _, e := range msg.entries {
		paths = append(paths, e.Path)
	}
	assert.Equal(t, []string{"conflict.go", "resolved.go", "added.go"}, paths,
		"the conflict, the resolution and the new file, each once")

	// without a merge the staged side stays out while anything is unstaged
	m = testNewModel(t, renderer, annot.NewStore(), noopHighlighter(), ModelConfig{})
	msg, ok = m.loadFiles()().(filesLoadedMsg)
	require.True(t, ok)
	require.NoError(t, msg.err)
	require.Len(t, msg.entries, 1)
	assert.Equal(t, "conflict.go", msg.entries[0].Path)
}
