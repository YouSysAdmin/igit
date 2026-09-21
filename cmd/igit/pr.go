package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"

	"github.com/yousysadmin/igit/internal/annot"
	"github.com/yousysadmin/igit/internal/forge"
	"github.com/yousysadmin/igit/internal/git"
	"github.com/yousysadmin/igit/internal/session"
	"github.com/yousysadmin/igit/internal/tui"
)

// prSubcommandName and mrSubcommandName are the positional words that review
// a pull or merge request: `igit pr [number|url|branch]`, `igit mr` is the
// same thing in GitLab words. the service comes from the remote, not the word.
const (
	prSubcommandName = "pr"
	mrSubcommandName = "mr"
)

// detectPRSubcommand turns "igit pr [ref]" into a pull-request session. As
// with commit, the word is a subcommand only when it is the first positional
// and was not forced positional with "--". the optional second positional is
// what gh should resolve (number, URL or branch), empty meaning the request of
// the checked-out branch.
func detectPRSubcommand(opts options, args []string) options {
	if (opts.Refs.Base != prSubcommandName && opts.Refs.Base != mrSubcommandName) || slices.Contains(args, "--") {
		return opts
	}
	opts.prSubcommand = true
	opts.prRef = opts.Refs.Against
	opts.Refs.Base, opts.Refs.Against = "", ""
	return opts
}

// validatePRFlags rejects review sources that make no sense for a pull
// request, and request-only flags outside one.
func validatePRFlags(opts options) error {
	if !opts.prSubcommand {
		if opts.Review.Since != "" {
			return errors.New("--since is only valid with igit pr")
		}
		return nil
	}
	if opts.Review.Staged {
		return errors.New("igit pr cannot be used with --staged")
	}
	return nil
}

// sinceReview is the reserved --since value that asks the service for the
// commit the current user last reviewed. A branch of that name is reachable as
// refs/heads/review.
const sinceReview = "review"

// prSession is a resolved request, the client that posts the review and the
// comments the request already carries.
type prSession struct {
	pr       forge.PullRequest
	hub      forge.Client
	notes    []tui.RemoteNote
	root     string // the clone the request was resolved in
	baseline string // what an incremental review starts from, empty for the full diff
}

// pickFunc lets the user choose one of the open pull requests. it returns the
// chosen item's ID or "" when the choice was abandoned.
type pickFunc func(items []tui.PickItem) (string, error)

// preparePullRequest picks the service from the origin remote (or the --forge
// override), resolves the request with its CLI from inside the current clone
// and fetches the commits it needs. the caller then reviews
// pr.MergeBase..pr.HeadSHA like any two-ref diff. Without a ref and without a
// request for the checked-out branch, the open requests are offered through
// the picker pickFor builds. Existing review comments are loaded too. a
// failure there is only a warning on warn.
func preparePullRequest(ctx context.Context, r prRequest) (prSession, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return prSession{}, fmt.Errorf("current directory: %w", err)
	}
	root, ok := git.DiscoverRoot(cwd)
	if !ok {
		return prSession{}, errors.New("igit pr must run inside a clone of the repository the request belongs to")
	}
	hub, err := forge.Open(ctx, root, r.forge)
	if err != nil {
		return prSession{}, fmt.Errorf("pick the code host: %w", err)
	}
	noun := forge.KindNoun(hub.Kind())
	if aerr := hub.Available(); aerr != nil {
		return prSession{}, fmt.Errorf("%s: %w", noun, aerr)
	}
	if r.pickFor != nil {
		r.pick = r.pickFor("Open " + noun + "s")
	}
	return prepareWithHub(ctx, hub, root, r)
}

// prRequest is what a pull-request session needs from the composition root.
type prRequest struct {
	ref     string     // what gh or glab resolves: number, URL or branch
	forge   forge.Kind // the --forge override
	since   string     // the --since value, empty for the full request diff
	opts    options    // history settings, for the --since=review fallback
	pickFor func(title string) pickFunc
	pick    pickFunc // resolved from pickFor, or set directly by tests
	warn    io.Writer
}

// prepareWithHub is preparePullRequest after the client exists. tests call it
// with a fake runner.
func prepareWithHub(ctx context.Context, hub forge.Client, root string, r prRequest) (prSession, error) {
	noun := forge.KindNoun(hub.Kind())
	pr, err := hub.Resolve(ctx, r.ref)
	if err != nil {
		if r.ref != "" || r.pick == nil || !hub.IsNoRequest(err) {
			return prSession{}, fmt.Errorf("%s: %w", noun, err)
		}
		if pr, err = pickPullRequest(ctx, hub, r.pick, err); err != nil {
			return prSession{}, err
		}
	}
	if perr := hub.Prepare(ctx, &pr); perr != nil {
		return prSession{}, fmt.Errorf("%s: %w", noun, perr)
	}
	sess := prSession{pr: pr, hub: hub, root: root}
	if r.since != "" {
		// resolved before the program starts: a range git cannot read would
		// otherwise surface as a load error painted into the diff pane
		if sess.baseline, err = resolveBaseline(ctx, hub, pr, r, root); err != nil {
			return prSession{}, err
		}
	}
	comments, cerr := hub.Comments(ctx, pr)
	if cerr != nil && r.warn != nil {
		_, _ = fmt.Fprintf(r.warn, "warning: existing review comments not loaded: %v\n", cerr)
	}
	sess.notes = remoteNotes(comments)
	return sess, nil
}

// resolveBaseline picks what an incremental review starts from. The reserved
// word asks the service and falls back to the commit igit recorded for its own
// last review of the request. An empty result leaves the full request diff in
// place, with the reason on warn. A revision the user named explicitly is an
// error when it does not resolve, because they asked for that one.
func resolveBaseline(ctx context.Context, hub forge.Client, pr forge.PullRequest, r prRequest, root string) (string, error) {
	if r.since != sinceReview {
		b, err := hub.Since(ctx, pr, r.since)
		if err != nil {
			return "", fmt.Errorf("--since %s: %w", r.since, err)
		}
		return describeBaseline(b, pr, r.warn), nil
	}
	b, err := hub.Since(ctx, pr, "")
	if err != nil {
		return "", fmt.Errorf("--since=review: %w", err)
	}
	if b.Commit == "" {
		if hist := historyCommitFor(r.opts, root, pr); hist != "" {
			// best effort: the history keeps a short hash, which cannot be
			// fetched by object name and can be ambiguous in a large repo
			if hb, herr := hub.Since(ctx, pr, hist); herr == nil {
				b = hb
			}
		}
	}
	if b.Commit == "" {
		warnf(r.warn, "no submitted review of %s found, showing the full request diff", pr.Name())
		return "", nil
	}
	return describeBaseline(b, pr, r.warn), nil
}

// describeBaseline warns about the range shapes that carry more than the
// author's own work, and about a request that has not moved at all.
func describeBaseline(b forge.Baseline, pr forge.PullRequest, warn io.Writer) string {
	switch {
	case b.Commit == "":
		return ""
	case b.Commit == pr.HeadSHA:
		warnf(warn, "the %s has not changed since %s", pr.Noun(), shortSHA(b.Commit))
	case b.Rewritten:
		warnf(warn, "the branch was rewritten since %s, the range also carries what the rewrite brought in", shortSHA(b.Commit))
	case b.Merged:
		warnf(warn, "the base branch was merged in since %s, the range also carries that", shortSHA(b.Commit))
	}
	return b.Commit
}

// historyCommitFor is the head igit recorded for its last review of the
// request, empty when the history has no entry for it.
func historyCommitFor(opts options, gitRoot string, pr forge.PullRequest) string {
	svc := historyService(opts)
	if svc == nil || gitRoot == "" {
		return ""
	}
	params := session.Params{Path: gitRoot, GitRoot: gitRoot, PR: pr.Number, PRKind: pr.ScopeKind()}
	entries, err := svc.List(params)
	if err != nil {
		return ""
	}
	scope := session.ScopeName(params)
	for _, e := range entries {
		if e.Scope == scope {
			return e.Commit
		}
	}
	return ""
}

func warnf(w io.Writer, format string, args ...any) {
	if w == nil {
		return
	}
	_, _ = fmt.Fprintf(w, "warning: "+format+"\n", args...)
}

// pickPullRequest offers the open requests when the branch has none. noPR is
// the original CLI error, returned when there is nothing to offer.
func pickPullRequest(ctx context.Context, hub forge.Client, pick pickFunc, noPR error) (forge.PullRequest, error) {
	noun := forge.KindNoun(hub.Kind())
	list, err := hub.ListOpen(ctx)
	if err != nil {
		return forge.PullRequest{}, fmt.Errorf("%s: %w", noun, err)
	}
	if len(list) == 0 {
		return forge.PullRequest{}, fmt.Errorf("%s: %w", noun, noPR)
	}
	sigil := "#"
	if hub.Kind() == forge.KindGitLab {
		sigil = "!"
	}
	items := make([]tui.PickItem, 0, len(list))
	for _, s := range list {
		label := fmt.Sprintf("%s%d  %s", sigil, s.Number, s.Title)
		if s.Draft {
			label += "  [draft]"
		}
		items = append(items, tui.PickItem{ID: strconv.Itoa(s.Number), Label: label, Detail: s.Author + " · " + s.Branch})
	}
	id, err := pick(items)
	if err != nil {
		return forge.PullRequest{}, fmt.Errorf("choose %s: %w", noun, err)
	}
	if id == "" {
		return forge.PullRequest{}, fmt.Errorf("no %s chosen", noun)
	}
	pr, err := hub.Resolve(ctx, id)
	if err != nil {
		return forge.PullRequest{}, fmt.Errorf("%s: %w", noun, err)
	}
	return pr, nil
}

// remoteNotes converts existing review comments into notes the review model
// draws under their lines. Multi-line comments are anchored to their last line,
// where GitHub shows them too. A comment the request no longer anchors keeps
// the position it was written against so the model can say where it belonged.
func remoteNotes(comments []forge.LineComment) []tui.RemoteNote {
	notes := make([]tui.RemoteNote, 0, len(comments))
	for _, c := range comments {
		notes = append(notes, tui.RemoteNote{
			File: c.Path, Line: c.Line, Side: c.Side, Author: c.Author, Body: c.Body,
			Outdated: c.Outdated, OrigPath: c.OrigPath, OrigLine: c.OrigLine,
		})
	}
	return notes
}

// prReviewer adapts a forge client to the review model's PRReviewer.
type prReviewer struct {
	pr  forge.PullRequest
	hub forge.Client
}

func (r prReviewer) Describe() string { return r.pr.Label() }

func (r prReviewer) Verdicts() []tui.PREvent {
	events := r.hub.Verdicts()
	out := make([]tui.PREvent, 0, len(events))
	for _, e := range events {
		out = append(out, tui.PREvent(e))
	}
	return out
}

func (r prReviewer) Plan(ctx context.Context, annots []annot.Annotation) (tui.PRPlan, error) {
	plan, err := r.hub.Plan(ctx, r.pr, annots)
	if err != nil {
		return tui.PRPlan{}, fmt.Errorf("plan review: %w", err)
	}
	return tui.PRPlan{Comments: len(plan.Comments), InBody: len(plan.Outside)}, nil
}

func (r prReviewer) Submit(ctx context.Context, event tui.PREvent, annots []annot.Annotation) (tui.PRSubmission, error) {
	sub, err := r.hub.Submit(ctx, r.pr, forge.Event(event), annots)
	if err != nil {
		return tui.PRSubmission{}, fmt.Errorf("submit review: %w", err)
	}
	return tui.PRSubmission{URL: sub.URL, Event: event, Comments: sub.Comments, InBody: sub.InBody}, nil
}

// prSubmissionNote is the line printed on stderr after a review was posted.
// noun is what the service calls the request.
func prSubmissionNote(sub *tui.PRSubmission, noun string) string {
	if sub == nil {
		return ""
	}
	verb := map[tui.PREvent]string{tui.PRComment: "commented on", tui.PRApprove: "approved", tui.PRRequestChanges: "requested changes on"}[sub.Event]
	if verb == "" {
		verb = "reviewed"
	}
	note := fmt.Sprintf("%s the %s", verb, noun)
	if sub.Comments > 0 {
		note += fmt.Sprintf(" with %d %s", sub.Comments, pluralComments(sub.Comments))
	}
	if sub.InBody > 0 {
		note += fmt.Sprintf(" (%d in the review body)", sub.InBody)
	}
	return note + ": " + sub.URL
}

func pluralComments(n int) string {
	if n == 1 {
		return "comment"
	}
	return "comments"
}

// shortSHA abbreviates a commit hash to the length git uses by default.
func shortSHA(sha string) string {
	if len(sha) <= 7 {
		return sha
	}
	return sha[:7]
}
