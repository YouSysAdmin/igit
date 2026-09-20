package main

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yousysadmin/igit/internal/git"
	"github.com/yousysadmin/igit/internal/tui"
	"github.com/yousysadmin/igit/internal/tui/mocks"
)

func TestGitDiffSource_WithOnly(t *testing.T) {
	dir := t.TempDir()
	g := git.NewGit(dir)
	renderer, workDir, err := gitDiffSource(g, options{Review: reviewOptions{Only: []string{"file.md"}}}, dir)
	require.NoError(t, err)
	require.NotNil(t, renderer)
	assert.IsType(t, &git.FallbackSource{}, renderer)
	assert.Equal(t, dir, workDir)
}

func TestGitDiffSource_WithoutOnly(t *testing.T) {
	dir := t.TempDir()
	g := git.NewGit(dir)
	renderer, workDir, err := gitDiffSource(g, options{}, dir)
	require.NoError(t, err)
	require.NotNil(t, renderer)
	// with no --only, returns *git.Git directly without FallbackRenderer wrapper
	assert.IsType(t, &git.Git{}, renderer)
	assert.Equal(t, dir, workDir)
}

func TestMakeNoVCSRenderer_WithOnly(t *testing.T) {
	tmpDir := t.TempDir()

	renderer, workDir, err := fileDiffSource([]string{"file.md"}, tmpDir)
	require.NoError(t, err)
	require.NotNil(t, renderer)
	assert.IsType(t, &git.FileReader{}, renderer)
	assert.Equal(t, tmpDir, workDir)
}

func TestMakeNoVCSRenderer_NoOnly(t *testing.T) {
	renderer, workDir, err := fileDiffSource(nil, "/tmp")
	require.Error(t, err)
	assert.Nil(t, renderer)
	assert.Empty(t, workDir)
	assert.Contains(t, err.Error(), "no git repository found")
}

func TestGitDiffSource_WithExclude(t *testing.T) {
	dir := t.TempDir()
	g := git.NewGit(dir)
	renderer, workDir, err := gitDiffSource(g, options{Exclude: []string{"vendor"}}, dir)
	require.NoError(t, err)
	require.NotNil(t, renderer)
	assert.IsType(t, &git.ExcludeFilter{}, renderer)
	assert.Equal(t, dir, workDir)
}

func TestGitDiffSource_WithInclude(t *testing.T) {
	dir := t.TempDir()
	g := git.NewGit(dir)
	renderer, workDir, err := gitDiffSource(g, options{Include: []string{"src"}}, dir)
	require.NoError(t, err)
	require.NotNil(t, renderer)
	assert.IsType(t, &git.IncludeFilter{}, renderer)
	assert.Equal(t, dir, workDir)
}

func TestIncludeExcludeComposition(t *testing.T) {
	// functional composition test: IncludeFilter + ExcludeFilter working together
	files := []git.FileEntry{
		{Path: "src/app.go"}, {Path: "src/vendor/lib.go"}, {Path: "src/main.go"},
		{Path: "pkg/util.go"}, {Path: "vendor/dep.go"},
	}
	inner := &mocks.DiffSourceMock{
		ChangedFilesFunc: func(string, bool) ([]git.FileEntry, error) { return files, nil },
		FileDiffFunc:     func(git.FileDiffRequest) ([]git.DiffLine, error) { return nil, nil },
	}

	// include narrows to src/, then exclude removes src/vendor/
	incl := git.NewIncludeFilter(inner, []string{"src"})
	excl := git.NewExcludeFilter(incl, []string{"src/vendor"})

	files, err := excl.ChangedFiles("", false)
	require.NoError(t, err)
	assert.Equal(t, []git.FileEntry{{Path: "src/app.go"}, {Path: "src/main.go"}}, files)
}

func TestDiscoverRoot_git(t *testing.T) {
	// this test runs from inside the igit repo (which is a git repo)
	root, ok := git.DiscoverRoot(".")
	assert.True(t, ok)
	assert.DirExists(t, root)
	assert.NotEmpty(t, root)
}

func TestDiscoverRoot_none(t *testing.T) {
	t.Chdir(t.TempDir())
	root, ok := git.DiscoverRoot(".")
	assert.False(t, ok)
	assert.Empty(t, root)
}

func TestCommitLogApplicable(t *testing.T) {
	refOpts := options{}
	refOpts.Refs.Base = "HEAD~1"
	g := git.NewGit(t.TempDir())

	tests := []struct {
		name string
		opts options
		cl   git.CommitLogger
		want bool
	}{
		{name: "nil commit logger", opts: refOpts, cl: nil, want: false},
		{name: "staged mode", opts: options{Review: reviewOptions{Staged: true}}, cl: g, want: false},
		{name: "empty ref", opts: options{}, cl: g, want: false},
		{name: "ref + logger applicable", opts: refOpts, cl: g, want: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, commitLogApplicable(tc.opts, tc.cl))
		})
	}
}

func TestCompactApplicable(t *testing.T) {
	dir := t.TempDir()
	g := git.NewGit(dir)
	fr := git.NewFileReader([]string{"file.md"}, dir)
	fallback := git.NewFallbackSource(g, []string{"file.md"}, dir)
	incl := git.NewIncludeFilter(g, []string{"src"})
	excl := git.NewExcludeFilter(g, []string{"vendor"})

	tests := []struct {
		name     string
		opts     options
		renderer tui.DiffSource
		want     bool
	}{
		{name: "plain git ref", opts: options{}, renderer: g, want: true},
		{name: "only without VCS (FileReader)", opts: options{Review: reviewOptions{Only: []string{"file.md"}}}, renderer: fr, want: false},
		{name: "only in VCS repo (Fallback wrapping Git)", opts: options{Review: reviewOptions{Only: []string{"file.md"}}}, renderer: fallback, want: true},
		{name: "include wrapping git", opts: options{Include: []string{"src"}}, renderer: incl, want: true},
		{name: "exclude wrapping git", opts: options{Exclude: []string{"vendor"}}, renderer: excl, want: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, compactApplicable(tc.renderer))
		})
	}
}

func TestGit_ImplementsCommitLogger(t *testing.T) {
	assert.Implements(t, (*git.CommitLogger)(nil), git.NewGit(t.TempDir()))
}

func TestSourceEditorPolicy_ModeBehavior(t *testing.T) {
	workDir := t.TempDir()
	refOpts := options{}
	refOpts.Refs.Base = "HEAD~1"

	tests := []struct {
		name string
		opts options
		root string
		want tui.SourceEditorPolicy
	}{
		{
			name: "worktree reloads after clean exit",
			opts: options{},
			root: workDir,
			want: tui.SourceEditorPolicy{
				Available:            true,
				Root:                 workDir,
				ReloadAfterCleanExit: true,
			},
		},
		{
			name: "staged opens without reload",
			opts: options{Review: reviewOptions{Staged: true}},
			root: workDir,
			want: tui.SourceEditorPolicy{Available: true, Root: workDir},
		},
		{
			name: "ref opens without reload",
			opts: refOpts,
			root: workDir,
			want: tui.SourceEditorPolicy{Available: true, Root: workDir},
		},
		{
			name: "empty root disables source editor",
			opts: options{},
			root: "",
			want: tui.SourceEditorPolicy{},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, sourceEditorPolicy(tc.opts, tc.root))
		})
	}
}

func TestFilterUntracked(t *testing.T) {
	base := func() ([]string, error) {
		return []string{"src/app.go", "src/vendor/lib.go", "docs/readme.md"}, nil
	}

	t.Run("nil fn returns nil", func(t *testing.T) {
		assert.Nil(t, filterUntracked(nil, []string{"src"}, nil))
	})

	t.Run("no prefixes returns fn unchanged", func(t *testing.T) {
		got, err := filterUntracked(base, nil, nil)()
		require.NoError(t, err)
		assert.Equal(t, []string{"src/app.go", "src/vendor/lib.go", "docs/readme.md"}, got)
	})

	t.Run("include narrows untracked", func(t *testing.T) {
		got, err := filterUntracked(base, []string{"src"}, nil)()
		require.NoError(t, err)
		assert.Equal(t, []string{"src/app.go", "src/vendor/lib.go"}, got)
	})

	t.Run("exclude drops untracked", func(t *testing.T) {
		got, err := filterUntracked(base, nil, []string{"src/vendor"})()
		require.NoError(t, err)
		assert.Equal(t, []string{"src/app.go", "docs/readme.md"}, got)
	})

	t.Run("include and exclude combined", func(t *testing.T) {
		got, err := filterUntracked(base, []string{"src"}, []string{"src/vendor"})()
		require.NoError(t, err)
		assert.Equal(t, []string{"src/app.go"}, got)
	})

	t.Run("inner error propagates", func(t *testing.T) {
		boom := func() ([]string, error) { return nil, errors.New("boom") }
		_, err := filterUntracked(boom, []string{"src"}, nil)()
		require.Error(t, err)
	})
}
