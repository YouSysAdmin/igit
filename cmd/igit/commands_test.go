package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yousysadmin/igit/internal/gitops"
	"github.com/yousysadmin/igit/internal/tui"
)

func TestParseArgs_CommitSubcommand(t *testing.T) {
	cfg := "--config=/nonexistent"
	tests := []struct {
		name     string
		args     []string
		wantMode string
		wantBase string
		wantAgst string
		wantErr  string
	}{
		{name: "default review", args: []string{cfg}, wantMode: "review"},
		{name: "commit subcommand", args: []string{cfg, "commit"}, wantMode: "commit"},
		{name: "commit subcommand with flags after", args: []string{"commit", cfg, "--no-mouse"}, wantMode: "commit"},
		{name: "--mode commit", args: []string{cfg, "--mode", "commit"}, wantMode: "commit"},
		{name: "--mode review", args: []string{cfg, "--mode=review"}, wantMode: "review"},
		{name: "single ref", args: []string{cfg, "HEAD~2"}, wantMode: "review", wantBase: "HEAD~2"},
		{name: "two refs", args: []string{cfg, "main", "feature"}, wantMode: "review", wantBase: "main", wantAgst: "feature"},
		{name: "ref named commit via --", args: []string{cfg, "--", "commit"}, wantMode: "review", wantBase: "commit"},
		{name: "commit with a second positional stays a two-ref diff", args: []string{cfg, "commit", "main"}, wantMode: "review", wantBase: "commit", wantAgst: "main"},
		{name: "invalid mode", args: []string{cfg, "--mode", "nope"}, wantErr: "parse args"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts, err := parseArgs(tt.args)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantMode, opts.startMode())
			assert.Equal(t, tt.wantBase, opts.Refs.Base)
			assert.Equal(t, tt.wantAgst, opts.Refs.Against)
		})
	}
}

func TestCommitUnavailableReason(t *testing.T) {
	assert.Empty(t, commitUnavailableReason(options{}, true))
	assert.Equal(t, "commit mode requires a git repository", commitUnavailableReason(options{}, false))
}

func TestStartMode_subcommandOutranksModeFlag(t *testing.T) {
	assert.Equal(t, "review", options{prSubcommand: true, Mode: "commit"}.startMode())
	assert.Equal(t, "commit", options{commitSubcommand: true, Mode: "review"}.startMode())
	assert.Equal(t, "commit", options{Mode: "commit"}.startMode())
}

func TestDumpConfig_includesMode(t *testing.T) {
	var out strings.Builder
	dumpConfig([]string{"--config", "/nonexistent"}, &out)
	assert.Contains(t, out.String(), "; mode = review")
	assert.NotContains(t, out.String(), "[commit]")
}

func TestStagePlanApplicable(t *testing.T) {
	assert.True(t, stagePlanApplicable(options{}, true))
	assert.False(t, stagePlanApplicable(options{}, false), "not git")
	assert.False(t, stagePlanApplicable(options{Review: reviewOptions{Staged: true}}, true))
	withRef := options{}
	withRef.Refs.Base = "HEAD~1"
	assert.False(t, stagePlanApplicable(withRef, true), "a ref review does not describe the working tree")
}

func TestResolveModes_commitOptions(t *testing.T) {
	setup := resolveModes(modeInputs{opts: options{Commit: commitOptions{NoVerify: true, Signoff: true}}, review: tui.ModelConfig{}, isGit: true})
	require.NotNil(t, setup.commit)
	assert.True(t, setup.commit.NoVerify)
	assert.True(t, setup.commit.Signoff)
	assert.NotNil(t, setup.planner)
	assert.Equal(t, tui.ModeReview, setup.start)
}

func TestParseArgs_commitOptionFlags(t *testing.T) {
	opts, err := parseArgs([]string{"--config=/nonexistent", "--no-verify", "--signoff"})
	require.NoError(t, err)
	assert.True(t, opts.Commit.NoVerify)
	assert.True(t, opts.Commit.Signoff)
	var out strings.Builder
	dumpConfig([]string{"--config", "/nonexistent"}, &out)
	assert.Contains(t, out.String(), "; no-verify = false")
	assert.Contains(t, out.String(), "; signoff = false")
}

func TestFileScope(t *testing.T) {
	assert.Nil(t, fileScope(options{}, "/repo", "/repo"), "no filters, no scope")

	inc := fileScope(options{Include: []string{"src"}, Exclude: []string{"src/gen"}}, "/repo", "/repo")
	assert.True(t, inc("src/a.go"))
	assert.False(t, inc("src/gen/a.go"))
	assert.False(t, inc("docs/a.md"))

	only := fileScope(options{Review: reviewOptions{Only: []string{"./model.go", "sub/x.go"}}}, "/repo", "/repo/pkg")
	assert.True(t, only("model.go"), "pattern as typed, minus ./")
	assert.True(t, only("pkg/model.go"), "resolved against the start directory")
	assert.True(t, only("a/sub/x.go"), "suffix match")
	assert.False(t, only("xsub/x.go"))
	assert.False(t, only("other.go"))

	abs := fileScope(options{Review: reviewOptions{Only: []string{"/repo/pkg/z.go"}}}, "/repo", "/repo/pkg")
	assert.True(t, abs("pkg/z.go"))
	assert.False(t, abs("z.go"))
}

func TestResolveModes_displayOptions(t *testing.T) {
	setup := resolveModes(modeInputs{opts: options{Exclude: []string{"vendor"}, Display: displayOptions{TreeWidth: 4, NoTree: true}}, isGit: true})
	require.NotNil(t, setup.commit)
	assert.Equal(t, 4, setup.commit.SideWidthRatio)
	assert.True(t, setup.commit.NoSidePane)
	require.NotNil(t, setup.commit.FileFilter)
	assert.False(t, setup.commit.FileFilter("vendor/x.go"))

	setup = resolveModes(modeInputs{isGit: true})
	assert.Equal(t, 0, setup.commit.SideWidthRatio, "0 lets the commit model pick its default")
	assert.Nil(t, setup.commit.FileFilter)
}

func TestStartModeFor(t *testing.T) {
	// a half-finished operation is commit-mode work
	mode, note := startModeFor(tui.ModeReview, gitops.InProgress{State: gitops.StateMerging})
	assert.Equal(t, tui.ModeCommit, mode)
	assert.Equal(t, "merging, started in commit mode where it is finished", note)

	mode, note = startModeFor(tui.ModeReview, gitops.InProgress{State: gitops.StateRebasing, Step: 2, Total: 5})
	assert.Equal(t, tui.ModeCommit, mode)
	assert.Contains(t, note, "rebasing 2/5", "the note carries the progress the status bar shows")

	// a clean tree opens where it was asked to, with nothing to explain
	mode, note = startModeFor(tui.ModeReview, gitops.InProgress{})
	assert.Equal(t, tui.ModeReview, mode)
	assert.Empty(t, note)
}

func TestOpenWhereTheWorkIs_leavesNamedModesAlone(t *testing.T) {
	// an operation is in progress, yet a named mode still decides
	mode, note := openWhereTheWorkIs(options{commitSubcommand: true}, gitops.InProgress{State: gitops.StateMerging}, tui.ModeCommit)
	assert.Equal(t, tui.ModeCommit, mode)
	assert.Empty(t, note)

	mode, note = openWhereTheWorkIs(options{prSubcommand: true}, gitops.InProgress{State: gitops.StateMerging}, tui.ModeReview)
	assert.Equal(t, tui.ModeReview, mode, "a pull request review is not about the local tree")
	assert.Empty(t, note)

	mode, _ = openWhereTheWorkIs(options{}, gitops.InProgress{State: gitops.StateMerging}, tui.ModeCommit)
	assert.Equal(t, tui.ModeCommit, mode, "already commit mode, nothing to move")
}
