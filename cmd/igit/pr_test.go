package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yousysadmin/igit/internal/annot"
	"github.com/yousysadmin/igit/internal/forge"
	"github.com/yousysadmin/igit/internal/tui"
)

func TestParseArgs_PRSubcommand(t *testing.T) {
	cfg := "--config=/nonexistent"
	tests := []struct {
		name    string
		args    []string
		wantPR  bool
		wantRef string
		wantErr string
	}{
		{name: "pr of the current branch", args: []string{cfg, "pr"}, wantPR: true},
		{name: "pr by number", args: []string{cfg, "pr", "42"}, wantPR: true, wantRef: "42"},
		{name: "pr by url with flags after", args: []string{"pr", "https://github.com/a/b/pull/7", cfg, "--no-mouse"}, wantPR: true, wantRef: "https://github.com/a/b/pull/7"},
		{name: "mr alias", args: []string{cfg, "mr", "7"}, wantPR: true, wantRef: "7"},
		{name: "mr of the current branch", args: []string{cfg, "mr"}, wantPR: true},
		{name: "ref named pr via --", args: []string{cfg, "--", "pr"}, wantPR: false},
		{name: "pr with staged", args: []string{cfg, "pr", "1", "--staged"}, wantErr: "igit pr cannot be used with --staged"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts, err := parseArgs(tt.args)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantPR, opts.prSubcommand)
			assert.Equal(t, tt.wantRef, opts.prRef)
			if tt.wantPR {
				assert.Empty(t, opts.Refs.Base, "the subcommand word is not a ref")
				assert.Empty(t, opts.Refs.Against)
			} else {
				assert.Equal(t, "pr", opts.Refs.Base)
			}
		})
	}
}

func TestResolveModes_pullRequestStartsInReview(t *testing.T) {
	assert.Contains(t, commitUnavailableReason(options{prSubcommand: true}, true), "pull request")
	setup := resolveModes(modeInputs{opts: options{prSubcommand: true, Mode: "commit"}, isGit: true})
	assert.Equal(t, tui.ModeReview, setup.start, "the pr subcommand outranks the --mode default")
	assert.Nil(t, setup.commit, "a pull request is a review session")
	assert.Contains(t, setup.unavailable, "pull request")
}

func TestPRReviewer_adapter(t *testing.T) {
	var apiStdin string
	run := func(_ context.Context, _, name, stdin string, args ...string) (string, error) {
		switch {
		case name == "git" && args[0] == "diff":
			return "diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1,2 +1,2 @@\n one\n-two\n+TWO\n", nil
		case name == "gh":
			apiStdin = stdin
			return `{"html_url":"https://github.com/o/r/pull/5#pullrequestreview-9"}`, nil
		}
		return "", nil
	}
	pr := forge.PullRequest{Owner: "o", Repo: "r", Number: 5, Title: "Title", URL: "https://github.com/o/r/pull/5", MergeBase: "mb", HeadSHA: "head"}
	r := prReviewer{pr: pr, hub: forge.NewWithRunner("/repo", run)}
	assert.Equal(t, "PR #5: Title", r.Describe())

	annots := []annot.Annotation{
		{File: "a.go", Line: 2, Type: "+", Comment: "shout"},
		{File: "a.go", Line: 40, Type: " ", Comment: "elsewhere"},
	}
	plan, err := r.Plan(t.Context(), annots)
	require.NoError(t, err)
	assert.Equal(t, tui.PRPlan{Comments: 1, InBody: 1}, plan)

	sub, err := r.Submit(t.Context(), tui.PRRequestChanges, annots)
	require.NoError(t, err)
	assert.Equal(t, tui.PRSubmission{URL: "https://github.com/o/r/pull/5#pullrequestreview-9", Event: tui.PRRequestChanges, Comments: 1, InBody: 1}, sub)
	assert.Contains(t, apiStdin, `"event":"REQUEST_CHANGES"`)
	assert.Contains(t, apiStdin, `"commit_id":"head"`)
}

func TestPRSubmissionNote(t *testing.T) {
	assert.Empty(t, prSubmissionNote(nil, "pull request"))
	note := prSubmissionNote(&tui.PRSubmission{URL: "u", Event: tui.PRApprove, Comments: 1}, "pull request")
	assert.Equal(t, "approved the pull request with 1 comment: u", note)
	note = prSubmissionNote(&tui.PRSubmission{URL: "u", Event: tui.PRComment, Comments: 3, InBody: 2}, "pull request")
	assert.Equal(t, "commented on the pull request with 3 comments (2 in the review body): u", note)
	assert.True(t, strings.HasPrefix(prSubmissionNote(&tui.PRSubmission{Event: "WEIRD"}, "pull request"), "reviewed the pull request"))
}

func TestFinalize_printsPRNote(t *testing.T) {
	var stdout, stderr strings.Builder
	err := finalize(finalizeReq{
		opts:        options{Review: reviewOptions{HistoryMax: 20, HistoryDir: t.TempDir()}},
		annotations: "## a.go:1 (+)\nx\n",
		files:       []string{"a.go"},
		workDir:     "repo",
		prNote:      "approved the pull request with 1 comment: u",
		stdout:      &stdout,
		stderr:      &stderr,
	})
	require.NoError(t, err)
	assert.Equal(t, "## a.go:1 (+)\nx\n", stdout.String())
	assert.Equal(t, "approved the pull request with 1 comment: u\n", stderr.String())
}

func fakeHub(answers, fail map[string]string, calls *[]string) *forge.GitHub {
	return forge.NewWithRunner("/repo", func(_ context.Context, _, name, _ string, args ...string) (string, error) {
		key := name + " " + strings.Join(args, " ")
		if calls != nil {
			*calls = append(*calls, key)
		}
		for prefix, stderr := range fail {
			if strings.HasPrefix(key, prefix) {
				return "", &forge.CommandError{Name: name, Args: args, Stderr: stderr, Err: errors.New("exit status 1")}
			}
		}
		for prefix, out := range answers {
			if strings.HasPrefix(key, prefix) {
				return out, nil
			}
		}
		return "", nil
	})
}

const prViewJSON = `{"number":12,"url":"https://github.com/acme/widgets/pull/12","title":"Fix","baseRefName":"main","headRefName":"fix","headRefOid":"abc"}`

func TestPrepareWithHub_loadsComments(t *testing.T) {
	var calls []string
	hub := fakeHub(map[string]string{
		"gh pr view":                prViewJSON,
		"git merge-base":            "mb\n",
		"gh api --paginate --slurp": `[[{"path":"a.go","line":3,"side":"RIGHT","body":"hi","user":{"login":"al"}}]]`,
	}, nil, &calls)
	var warn strings.Builder
	sess, err := prepareWithHub(t.Context(), hub, "12", nil, &warn)
	require.NoError(t, err)
	assert.Equal(t, "mb", sess.pr.MergeBase)
	assert.Equal(t, []tui.RemoteNote{{File: "a.go", Line: 3, Side: "RIGHT", Author: "al", Body: "hi"}}, sess.notes)
	assert.Empty(t, warn.String())

	// a failing comment fetch is a warning, not an error
	hub = fakeHub(map[string]string{"gh pr view": prViewJSON, "git merge-base": "mb\n"}, map[string]string{"gh api": "HTTP 500"}, nil)
	sess, err = prepareWithHub(t.Context(), hub, "12", nil, &warn)
	require.NoError(t, err)
	assert.Empty(t, sess.notes)
	assert.Contains(t, warn.String(), "warning: existing review comments not loaded")
}

func TestPrepareWithHub_picksWhenBranchHasNoPR(t *testing.T) {
	views := 0
	hub := forge.NewWithRunner("/repo", func(_ context.Context, _, name, _ string, args ...string) (string, error) {
		key := name + " " + strings.Join(args, " ")
		switch {
		case strings.HasPrefix(key, "gh pr view --json"): // no ref: the branch has no request
			views++
			return "", &forge.CommandError{Name: name, Args: args, Stderr: "no pull requests found for branch \"main\"", Err: errors.New("exit status 1")}
		case strings.HasPrefix(key, "gh pr view 7"):
			return strings.ReplaceAll(prViewJSON, `"number":12`, `"number":7`), nil
		case strings.HasPrefix(key, "gh pr list"):
			return `[{"number":7,"title":"Seven","headRefName":"seven","isDraft":true,"author":{"login":"al"}},{"number":6,"title":"Six","headRefName":"six","author":{"login":"bo"}}]`, nil
		case strings.HasPrefix(key, "git merge-base"):
			return "mb\n", nil
		}
		return "", nil
	})
	var offered []tui.PickItem
	pick := func(items []tui.PickItem) (string, error) { offered = items; return "7", nil }
	sess, err := prepareWithHub(t.Context(), hub, "", pick, nil)
	require.NoError(t, err)
	assert.Equal(t, 7, sess.pr.Number)
	require.Len(t, offered, 2)
	assert.Equal(t, tui.PickItem{ID: "7", Label: "#7  Seven  [draft]", Detail: "al · seven"}, offered[0])
	assert.Equal(t, "6", offered[1].ID)

	// canceling the picker is an error the caller reports
	cancel := func([]tui.PickItem) (string, error) { return "", nil }
	_, err = prepareWithHub(t.Context(), hub, "", cancel, nil)
	require.ErrorContains(t, err, "no pull request chosen")

	// an explicit ref that fails is never turned into a picker
	_, err = prepareWithHub(t.Context(), hub, "nope", pick, nil)
	require.ErrorContains(t, err, "pull request")
	assert.Equal(t, 2, views, "each no-ref flow looks the branch up once")
}

func TestPrepareWithHub_noOpenRequestsKeepsOriginalError(t *testing.T) {
	hub := fakeHub(map[string]string{"gh pr list": "[]"}, map[string]string{"gh pr view": "no pull requests found for branch \"main\""}, nil)
	_, err := prepareWithHub(t.Context(), hub, "", func([]tui.PickItem) (string, error) { return "1", nil }, nil)
	require.ErrorContains(t, err, "no pull requests found")
}

func TestShortSHA(t *testing.T) {
	assert.Equal(t, "0123456", shortSHA("0123456789abcdef0123456789abcdef01234567"))
	assert.Equal(t, "abc", shortSHA("abc"))
	assert.Empty(t, shortSHA(""))
}
