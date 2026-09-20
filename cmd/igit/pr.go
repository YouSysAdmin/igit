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
// request, which always compares the merge base with the head commit.
func validatePRFlags(opts options) error {
	if !opts.prSubcommand {
		return nil
	}
	if opts.Review.Staged {
		return errors.New("igit pr cannot be used with --staged")
	}
	return nil
}

// prSession is a resolved request, the client that posts the review and the
// comments the request already carries.
type prSession struct {
	pr    forge.PullRequest
	hub   forge.Client
	notes []tui.RemoteNote
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
func preparePullRequest(ctx context.Context, ref string, override forge.Kind, pickFor func(title string) pickFunc, warn io.Writer) (prSession, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return prSession{}, fmt.Errorf("current directory: %w", err)
	}
	root, ok := git.DiscoverRoot(cwd)
	if !ok {
		return prSession{}, errors.New("igit pr must run inside a clone of the repository the request belongs to")
	}
	hub, err := forge.Open(ctx, root, override)
	if err != nil {
		return prSession{}, fmt.Errorf("pick the code host: %w", err)
	}
	noun := forge.KindNoun(hub.Kind())
	if aerr := hub.Available(); aerr != nil {
		return prSession{}, fmt.Errorf("%s: %w", noun, aerr)
	}
	var pick pickFunc
	if pickFor != nil {
		pick = pickFor("Open " + noun + "s")
	}
	return prepareWithHub(ctx, hub, ref, pick, warn)
}

// prepareWithHub is preparePullRequest after the client exists. tests call it
// with a fake runner.
func prepareWithHub(ctx context.Context, hub forge.Client, ref string, pick pickFunc, warn io.Writer) (prSession, error) {
	noun := forge.KindNoun(hub.Kind())
	pr, err := hub.Resolve(ctx, ref)
	if err != nil {
		if ref != "" || pick == nil || !hub.IsNoRequest(err) {
			return prSession{}, fmt.Errorf("%s: %w", noun, err)
		}
		if pr, err = pickPullRequest(ctx, hub, pick, err); err != nil {
			return prSession{}, err
		}
	}
	if perr := hub.Prepare(ctx, &pr); perr != nil {
		return prSession{}, fmt.Errorf("%s: %w", noun, perr)
	}
	sess := prSession{pr: pr, hub: hub}
	comments, cerr := hub.Comments(ctx, pr)
	if cerr != nil && warn != nil {
		_, _ = fmt.Fprintf(warn, "warning: existing review comments not loaded: %v\n", cerr)
	}
	sess.notes = remoteNotes(comments)
	return sess, nil
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
// where GitHub shows them too.
func remoteNotes(comments []forge.LineComment) []tui.RemoteNote {
	notes := make([]tui.RemoteNote, 0, len(comments))
	for _, c := range comments {
		notes = append(notes, tui.RemoteNote{File: c.Path, Line: c.Line, Side: c.Side, Author: c.Author, Body: c.Body})
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
