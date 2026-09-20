package git_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yousysadmin/igit/internal/git"
	"github.com/yousysadmin/igit/internal/git/mocks"
)

func TestExcludeFilter_ChangedFiles(t *testing.T) {
	inner := &mocks.DiffSourceMock{
		ChangedFilesFunc: func(string, bool) ([]git.FileEntry, error) {
			return []git.FileEntry{
				{Path: "cmd/main.go"}, {Path: "vendor/lib.go"}, {Path: "diff/diff.go"},
				{Path: "vendor/pkg/x.go"}, {Path: "ui/mocks/m.go"},
			}, nil
		},
	}
	ef := git.NewExcludeFilter(inner, []string{"vendor", "ui/mocks"})

	files, err := ef.ChangedFiles("", false)
	require.NoError(t, err)
	assert.Equal(t, []git.FileEntry{{Path: "cmd/main.go"}, {Path: "diff/diff.go"}}, files)
}

func TestExcludeFilter_ChangedFiles_noExcludes(t *testing.T) {
	inner := &mocks.DiffSourceMock{
		ChangedFilesFunc: func(string, bool) ([]git.FileEntry, error) {
			return []git.FileEntry{{Path: "a.go"}, {Path: "b.go"}}, nil
		},
	}
	ef := git.NewExcludeFilter(inner, nil)

	files, err := ef.ChangedFiles("", false)
	require.NoError(t, err)
	assert.Equal(t, []git.FileEntry{{Path: "a.go"}, {Path: "b.go"}}, files)
}

func TestExcludeFilter_ChangedFiles_allExcluded(t *testing.T) {
	inner := &mocks.DiffSourceMock{
		ChangedFilesFunc: func(string, bool) ([]git.FileEntry, error) {
			return []git.FileEntry{{Path: "vendor/a.go"}, {Path: "vendor/b.go"}}, nil
		},
	}
	ef := git.NewExcludeFilter(inner, []string{"vendor"})

	files, err := ef.ChangedFiles("", false)
	require.NoError(t, err)
	assert.Empty(t, files)
}

func TestExcludeFilter_ChangedFiles_innerError(t *testing.T) {
	inner := &mocks.DiffSourceMock{
		ChangedFilesFunc: func(string, bool) ([]git.FileEntry, error) { return nil, errors.New("git failed") },
	}
	ef := git.NewExcludeFilter(inner, []string{"vendor"})

	_, err := ef.ChangedFiles("", false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "git failed")
}

func TestExcludeFilter_FileDiff_passthrough(t *testing.T) {
	lines := []git.DiffLine{
		{OldNum: 1, NewNum: 1, Content: "line1", ChangeType: git.ChangeContext},
		{OldNum: 2, NewNum: 2, Content: "line2", ChangeType: git.ChangeContext},
	}
	inner := &mocks.DiffSourceMock{
		FileDiffFunc: func(git.FileDiffRequest) ([]git.DiffLine, error) { return lines, nil },
	}
	ef := git.NewExcludeFilter(inner, []string{"vendor"})

	// even a file matching exclude prefix is passed through - filtering is only at file list level
	result, err := ef.FileDiff(git.FileDiffRequest{Path: "vendor/foo.go"})
	require.NoError(t, err)
	assert.Equal(t, lines, result)
}

func TestExcludeFilter_FileDiff_innerError(t *testing.T) {
	inner := &mocks.DiffSourceMock{
		FileDiffFunc: func(git.FileDiffRequest) ([]git.DiffLine, error) { return nil, errors.New("read failed") },
	}
	ef := git.NewExcludeFilter(inner, []string{"vendor"})

	_, err := ef.FileDiff(git.FileDiffRequest{Path: "foo.go"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read failed")
}

func TestExcludeFilter_FileDiff_passesContextLinesThrough(t *testing.T) {
	tests := []struct {
		name    string
		context int
	}{
		{name: "full context (zero)", context: 0},
		{name: "small context", context: 5},
		{name: "full-file sentinel", context: 1000000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotContext int
			inner := &mocks.DiffSourceMock{
				FileDiffFunc: func(req git.FileDiffRequest) ([]git.DiffLine, error) {
					gotContext = req.ContextLines
					return nil, nil
				},
			}
			ef := git.NewExcludeFilter(inner, []string{"vendor"})
			_, err := ef.FileDiff(git.FileDiffRequest{Path: "foo.go", ContextLines: tt.context})
			require.NoError(t, err)
			assert.Equal(t, tt.context, gotContext)
		})
	}
}
