package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func fixedNow() time.Time { return time.Date(2026, 9, 18, 14, 5, 9, 0, time.UTC) }

func TestWriteAnnotationOutput_Precedence(t *testing.T) {
	const output = "## file.go:1 (+)\ncomment\n"

	t.Run("session path beats --output and --output-dir", func(t *testing.T) {
		dir := t.TempDir()
		sessionPath := filepath.Join(dir, "session.md")
		flagPath := filepath.Join(dir, "flag.md")
		var stdout, stderr bytes.Buffer
		err := writeAnnotationOutput(annotationOutputReq{
			opts:          options{Review: reviewOptions{Output: flagPath, OutputDir: filepath.Join(dir, "out")}},
			output:        output,
			sessionOutput: sessionPath,
			stdout:        &stdout,
			stderr:        &stderr,
		})
		require.NoError(t, err)
		assert.FileExists(t, sessionPath)
		assert.NoFileExists(t, flagPath)
		assert.NoDirExists(t, filepath.Join(dir, "out"))
		assert.Empty(t, stdout.String(), "a file destination keeps stdout silent")
		assert.Empty(t, stderr.String())
	})

	t.Run("--output beats --output-dir", func(t *testing.T) {
		dir := t.TempDir()
		flagPath := filepath.Join(dir, "flag.md")
		var stdout bytes.Buffer
		err := writeAnnotationOutput(annotationOutputReq{
			opts:   options{Review: reviewOptions{Output: flagPath, OutputDir: filepath.Join(dir, "out")}},
			output: output,
			stdout: &stdout,
		})
		require.NoError(t, err)
		assert.FileExists(t, flagPath)
		assert.NoDirExists(t, filepath.Join(dir, "out"))
		assert.Empty(t, stdout.String())
	})

	t.Run("--output-dir writes timestamped file and still prints to stdout", func(t *testing.T) {
		outDir := filepath.Join(t.TempDir(), "reviews")
		var stdout, stderr bytes.Buffer
		err := writeAnnotationOutput(annotationOutputReq{
			opts:      options{Review: reviewOptions{OutputDir: outDir}},
			output:    output,
			repoLabel: "igit",
			stdout:    &stdout,
			stderr:    &stderr,
			now:       fixedNow,
		})
		require.NoError(t, err)
		want := filepath.Join(outDir, "igit-2026-09-18T14-05-09.md")
		got, rerr := os.ReadFile(want) //nolint:gosec // test reads a file under t.TempDir
		require.NoError(t, rerr)
		assert.Equal(t, output, string(got))
		assert.Equal(t, output, stdout.String())
		assert.Equal(t, "annotations saved to "+want+"\n", stderr.String())
	})

	t.Run("--output-dir expands ~", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		var stdout bytes.Buffer
		err := writeAnnotationOutput(annotationOutputReq{
			opts:   options{Review: reviewOptions{OutputDir: "~/reviews"}},
			output: output,
			stdout: &stdout,
			now:    fixedNow,
		})
		require.NoError(t, err)
		assert.FileExists(t, filepath.Join(home, "reviews", "review-2026-09-18T14-05-09.md"))
	})

	t.Run("--output-dir under a regular file fails", func(t *testing.T) {
		blocker := filepath.Join(t.TempDir(), "blocker")
		require.NoError(t, os.WriteFile(blocker, []byte("x"), 0o600))
		var stdout bytes.Buffer
		err := writeAnnotationOutput(annotationOutputReq{
			opts:   options{Review: reviewOptions{OutputDir: blocker}},
			output: output,
			stdout: &stdout,
			now:    fixedNow,
		})
		require.Error(t, err)
		assert.Empty(t, stdout.String())
	})

	t.Run("nil stderr is tolerated", func(t *testing.T) {
		var stdout bytes.Buffer
		err := writeAnnotationOutput(annotationOutputReq{
			opts:   options{Review: reviewOptions{OutputDir: t.TempDir()}},
			output: output,
			stdout: &stdout,
			now:    fixedNow,
		})
		require.NoError(t, err)
		assert.Equal(t, output, stdout.String())
	})
}

func TestFinalize_SessionOutputPath(t *testing.T) {
	sessionPath := filepath.Join(t.TempDir(), "chosen.md")
	var stdout bytes.Buffer
	err := finalize(finalizeReq{
		opts:          options{Review: reviewOptions{HistoryMax: 20, HistoryDir: t.TempDir()}},
		annotations:   "## a.go:1 (+)\nx\n",
		files:         []string{"a.go"},
		workDir:       "repo",
		sessionOutput: sessionPath,
		stdout:        &stdout,
	})
	require.NoError(t, err)
	assert.FileExists(t, sessionPath)
	assert.Empty(t, stdout.String())
}

func TestOutputPathFuncs(t *testing.T) {
	work := t.TempDir()
	defaultFn, saveAsFn := outputPathFuncs(options{}, "repo", work)
	assert.Empty(t, defaultFn(), "no --output-dir means no default destination")
	got := saveAsFn()
	assert.Equal(t, work, filepath.Dir(got))
	assert.Regexp(t, `^repo-\d{4}-\d{2}-\d{2}T\d{2}-\d{2}-\d{2}\.md$`, filepath.Base(got))

	outDir := filepath.Join(t.TempDir(), "out")
	defaultFn, saveAsFn = outputPathFuncs(options{Review: reviewOptions{OutputDir: outDir}}, "repo", work)
	assert.Equal(t, outDir, filepath.Dir(defaultFn()))
	assert.Equal(t, outDir, filepath.Dir(saveAsFn()))
}

func TestOutputDirPath_defaultsToNow(t *testing.T) {
	got := outputDirPath("/tmp/x", "r", nil)
	assert.Equal(t, "/tmp/x", filepath.Dir(got))
	assert.Regexp(t, `^r-\d{4}-\d{2}-\d{2}T\d{2}-\d{2}-\d{2}\.md$`, filepath.Base(got))
}

func TestFinalize_RestoredSessionClearsTheEntry(t *testing.T) {
	histDir := t.TempDir()
	workDir := t.TempDir()
	opts := options{Review: reviewOptions{HistoryMax: 20, HistoryDir: histDir}}
	entry := filepath.Join(histDir, filepath.Base(workDir), "worktree.md")

	var stdout bytes.Buffer
	require.NoError(t, finalize(finalizeReq{
		opts: opts, annotations: "## a.go:1 (+)\nx\n", files: []string{"a.go"},
		workDir: workDir, stdout: &stdout,
	}))
	require.FileExists(t, entry)

	// a session that only browsed leaves the saved review alone
	require.NoError(t, finalize(finalizeReq{opts: opts, workDir: workDir, stdout: &stdout}))
	assert.FileExists(t, entry)

	// continuing that review and deleting every annotation clears it
	require.NoError(t, finalize(finalizeReq{opts: opts, workDir: workDir, restored: true, stdout: &stdout}))
	assert.NoFileExists(t, entry)
	assert.Equal(t, "## a.go:1 (+)\nx\n", stdout.String(), "an empty review writes no output")
}

func TestFinalize_PostedReviewClearsTheEntry(t *testing.T) {
	histDir := t.TempDir()
	workDir := t.TempDir()
	opts := options{Review: reviewOptions{HistoryMax: 20, HistoryDir: histDir}}
	entry := filepath.Join(histDir, filepath.Base(workDir), "mr-7.md")
	annotations := "## a.go:1 (+)\nx\n"

	// a session that quits without posting keeps its draft
	var stdout, stderr bytes.Buffer
	require.NoError(t, finalize(finalizeReq{
		opts: opts, annotations: annotations, files: []string{"a.go"},
		workDir: workDir, prNumber: 7, prKind: "mr", stdout: &stdout, stderr: &stderr,
	}))
	require.FileExists(t, entry)

	// posting the review hands the annotations to the request, so the draft goes
	stdout.Reset()
	require.NoError(t, finalize(finalizeReq{
		opts: opts, annotations: annotations, files: []string{"a.go"},
		workDir: workDir, prNumber: 7, prKind: "mr", prPosted: true,
		prNote: "commented on the merge request", stdout: &stdout, stderr: &stderr,
	}))
	assert.NoFileExists(t, entry, "a posted review is not restored next session")
	assert.Equal(t, annotations, stdout.String(), "the local output is still written")
	assert.Contains(t, stderr.String(), "commented on the merge request")
}
