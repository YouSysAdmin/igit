package forge

import (
	"cmp"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"slices"
	"strings"

	"github.com/yousysadmin/igit/internal/annot"
)

// Review is the POST /pulls/{n}/reviews payload.
type Review struct {
	CommitID string    `json:"commit_id"`
	Body     string    `json:"body,omitempty"`
	Event    string    `json:"event"`
	Comments []Comment `json:"comments,omitempty"`
}

// GitHub reviews pull requests of the repository cloned at dir.
type GitHub struct {
	dir string
	run Runner
}

// New returns a GitHub client for the clone at dir.
func New(dir string) *GitHub { return &GitHub{dir: dir, run: execRunner} }

// NewWithRunner returns a client whose commands go through run.
func NewWithRunner(dir string, run Runner) *GitHub { return &GitHub{dir: dir, run: run} }

// Available reports whether the gh CLI can be found.
func (g *GitHub) Available() error {
	if _, err := exec.LookPath("gh"); err != nil {
		return errors.New("igit pr needs the GitHub CLI: install gh and run `gh auth login`")
	}
	return nil
}

var pullURLRe = regexp.MustCompile(`github\.com/([^/]+)/([^/]+)/pull/(\d+)`)

// parsePullURL extracts owner and repository from a pull request URL.
func parsePullURL(url string) (owner, repo string, err error) {
	m := pullURLRe.FindStringSubmatch(url)
	if m == nil {
		return "", "", fmt.Errorf("unexpected pull request url %q", url)
	}
	return m[1], m[2], nil
}

// Resolve looks up a pull request through gh: ref is a number, a URL, a
// branch name or empty for the request of the current branch.
func (g *GitHub) Resolve(ctx context.Context, ref string) (PullRequest, error) {
	args := []string{"pr", "view"}
	if ref != "" {
		args = append(args, ref)
	}
	args = append(args, "--json", "number,url,title,baseRefName,headRefName,headRefOid")
	out, err := g.run(ctx, g.dir, "gh", "", args...)
	if err != nil {
		return PullRequest{}, fmt.Errorf("resolve pull request: %w", err)
	}
	var v struct {
		Number      int    `json:"number"`
		URL         string `json:"url"`
		Title       string `json:"title"`
		BaseRefName string `json:"baseRefName"`
		HeadRefName string `json:"headRefName"`
		HeadRefOid  string `json:"headRefOid"`
	}
	if uerr := json.Unmarshal([]byte(out), &v); uerr != nil {
		return PullRequest{}, fmt.Errorf("parse gh pr view output: %w", uerr)
	}
	owner, repo, err := parsePullURL(v.URL)
	if err != nil {
		return PullRequest{}, err
	}
	if v.HeadRefOid == "" || v.BaseRefName == "" {
		return PullRequest{}, fmt.Errorf("pull request #%d has no head commit or base branch", v.Number)
	}
	return PullRequest{
		Forge: KindGitHub,
		Owner: owner, Repo: repo, Number: v.Number, URL: v.URL, Title: strings.TrimSpace(v.Title),
		BaseRef: v.BaseRefName, HeadRef: v.HeadRefName, HeadSHA: v.HeadRefOid,
	}, nil
}

// Prepare fetches the pull request head and the base branch from the remote
// that points at the repository, then records the merge base the review
// starts from. Nothing is checked out.
func (g *GitHub) Prepare(ctx context.Context, pr *PullRequest) error {
	remote := remoteFor(ctx, g.run, g.dir, pr.Owner+"/"+pr.Repo)
	headRef := fmt.Sprintf("+refs/pull/%d/head:refs/remotes/%s/pr/%d", pr.Number, remote, pr.Number)
	baseRef := fmt.Sprintf("+refs/heads/%s:refs/remotes/%s/%s", pr.BaseRef, remote, pr.BaseRef)
	if _, err := g.run(ctx, g.dir, "git", "", "fetch", "--no-tags", "--no-write-fetch-head", remote, headRef, baseRef); err != nil {
		return fmt.Errorf("fetch pull request #%d from %s: %w", pr.Number, remote, err)
	}
	out, err := g.run(ctx, g.dir, "git", "", "merge-base", remote+"/"+pr.BaseRef, pr.HeadSHA)
	if err != nil {
		return fmt.Errorf("merge base of %s and %s: %w", pr.BaseRef, pr.HeadSHA, err)
	}
	pr.Remote = remote
	pr.MergeBase = strings.TrimSpace(out)
	if pr.MergeBase == "" {
		return fmt.Errorf("no merge base between %s and pull request #%d", pr.BaseRef, pr.Number)
	}
	return nil
}

// Plan converts annotations into review comments. Every annotated line is
// checked against the pull request's own diff of that file (default three
// lines of context), because GitHub only anchors comments to lines that
// appear in the diff it shows. the rest is reported in Outside.
func (g *GitHub) Plan(ctx context.Context, pr PullRequest, annots []annot.Annotation) (Plan, error) {
	plan, _, err := anchor(ctx, g.run, g.dir, pr, annots)
	return plan, err
}

// Kind names the service.
func (g *GitHub) Kind() Kind { return KindGitHub }

// Verdicts returns every event, GitHub reviews accept all three.
func (g *GitHub) Verdicts() []Event { return slices.Clone(allVerdicts) }

// IsNoRequest reports whether err is gh telling that the current branch has
// no pull request.
func (g *GitHub) IsNoRequest(err error) bool { return IsNoPullRequest(err) }

// Submit posts the annotations as one review with the given verdict and
// returns where it landed.
func (g *GitHub) Submit(ctx context.Context, pr PullRequest, event Event, annots []annot.Annotation) (Submission, error) {
	plan, err := g.Plan(ctx, pr, annots)
	if err != nil {
		return Submission{}, err
	}
	review := Review{CommitID: pr.HeadSHA, Event: string(event), Comments: plan.Comments, Body: bodyFor(plan.Outside)}
	payload, err := json.Marshal(review)
	if err != nil {
		return Submission{}, fmt.Errorf("encode review: %w", err)
	}
	endpoint := fmt.Sprintf("repos/%s/%s/pulls/%d/reviews", pr.Owner, pr.Repo, pr.Number)
	out, err := g.run(ctx, g.dir, "gh", string(payload), "api", "-X", "POST", "-H", "Accept: application/vnd.github+json", endpoint, "--input", "-")
	if err != nil {
		return Submission{}, fmt.Errorf("post review: %w", err)
	}
	var resp struct {
		HTMLURL string `json:"html_url"`
	}
	_ = json.Unmarshal([]byte(out), &resp) // a missing url is not a failure, the review is posted
	url := resp.HTMLURL
	if url == "" {
		url = pr.URL
	}
	return Submission{URL: url, Event: event, Comments: len(plan.Comments), InBody: len(plan.Outside)}, nil
}

// Comments returns the pull request's review comments. Ones GitHub still
// anchors keep their line. Ones it no longer anchors, because the author
// changed the code under them, are re-anchored against the current head and
// marked outdated, so a second review never loses what the first one said.
// Every page is fetched.
func (g *GitHub) Comments(ctx context.Context, pr PullRequest) ([]LineComment, error) {
	endpoint := fmt.Sprintf("repos/%s/%s/pulls/%d/comments", pr.Owner, pr.Repo, pr.Number)
	out, err := g.run(ctx, g.dir, "gh", "", "api", "--paginate", "--slurp", "-H", "Accept: application/vnd.github+json", endpoint)
	if err != nil {
		return nil, fmt.Errorf("list review comments: %w", err)
	}
	var pages [][]struct {
		Path        string `json:"path"`
		Line        *int   `json:"line"`
		StartLine   *int   `json:"start_line"`
		OrigLine    *int   `json:"original_line"`
		OrigSHA     string `json:"original_commit_id"`
		SubjectType string `json:"subject_type"`
		Side        string `json:"side"`
		Body        string `json:"body"`
		User        struct {
			Login string `json:"login"`
		} `json:"user"`
	}
	if strings.TrimSpace(out) == "" {
		return nil, nil
	}
	if err := json.Unmarshal([]byte(out), &pages); err != nil {
		return nil, fmt.Errorf("parse review comments: %w", err)
	}
	var comments []LineComment
	for _, page := range pages {
		for _, c := range page {
			if c.Path == "" {
				continue // nothing to anchor it to, not even a file
			}
			lc := LineComment{Path: c.Path, Side: c.Side, Author: c.User.Login, Body: normalizeBody(c.Body)}
			if lc.Side == "" {
				lc.Side = "RIGHT"
			}
			switch {
			case c.SubjectType == "file":
				// a comment on the file itself, it never had a line
			case c.Line != nil && *c.Line > 0:
				lc.Line = *c.Line
				if c.StartLine != nil && *c.StartLine > 0 && *c.StartLine < lc.Line {
					lc.StartLine = *c.StartLine
				}
			default:
				// GitHub reports no current line: the code it sat on changed
				lc.Outdated, lc.OrigSHA = true, c.OrigSHA
				if c.OrigLine != nil {
					lc.OrigLine = *c.OrigLine
				}
			}
			comments = append(comments, lc)
		}
	}
	return reanchor(ctx, g.run, g.dir, pr, comments), nil
}

// ListOpen returns the repository's open pull requests, newest first.
func (g *GitHub) ListOpen(ctx context.Context) ([]Summary, error) {
	out, err := g.run(ctx, g.dir, "gh", "", "pr", "list", "--state", "open", "--limit", "100", "--json", "number,title,author,headRefName,isDraft")
	if err != nil {
		return nil, fmt.Errorf("list pull requests: %w", err)
	}
	var v []struct {
		Number      int    `json:"number"`
		Title       string `json:"title"`
		HeadRefName string `json:"headRefName"`
		IsDraft     bool   `json:"isDraft"`
		Author      struct {
			Login string `json:"login"`
		} `json:"author"`
	}
	if strings.TrimSpace(out) == "" {
		return nil, nil
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		return nil, fmt.Errorf("parse pull request list: %w", err)
	}
	list := make([]Summary, 0, len(v))
	for _, p := range v {
		list = append(list, Summary{Number: p.Number, Title: strings.TrimSpace(p.Title), Author: p.Author.Login, Branch: p.HeadRefName, Draft: p.IsDraft})
	}
	return list, nil
}

// Since resolves what an incremental review starts from. An empty rev asks
// GitHub for the commit this user last submitted a review on.
func (g *GitHub) Since(ctx context.Context, pr PullRequest, rev string) (Baseline, error) {
	if rev == "" {
		last, err := g.lastReviewed(ctx, pr)
		if err != nil || last == "" {
			return Baseline{}, err
		}
		rev = last
	}
	return baselineFor(ctx, g.run, g.dir, pr, rev)
}

// lastReviewed is the commit the authenticated user last submitted a review
// on, empty when there is none. Pending reviews are drafts and are skipped, a
// dismissed one still says what was last looked at.
func (g *GitHub) lastReviewed(ctx context.Context, pr PullRequest) (string, error) {
	login, err := g.login(ctx)
	if err != nil || login == "" {
		return "", err
	}
	endpoint := fmt.Sprintf("repos/%s/%s/pulls/%d/reviews", pr.Owner, pr.Repo, pr.Number)
	out, err := g.run(ctx, g.dir, "gh", "", "api", "--paginate", "--slurp", "-H", "Accept: application/vnd.github+json", endpoint)
	if err != nil {
		return "", fmt.Errorf("list reviews: %w", err)
	}
	if strings.TrimSpace(out) == "" {
		return "", nil
	}
	var pages [][]struct {
		ID          int64  `json:"id"`
		State       string `json:"state"`
		CommitID    string `json:"commit_id"`
		SubmittedAt string `json:"submitted_at"`
		User        struct {
			Login string `json:"login"`
		} `json:"user"`
	}
	if err := json.Unmarshal([]byte(out), &pages); err != nil {
		return "", fmt.Errorf("parse reviews: %w", err)
	}
	var bestAt string
	var bestID int64
	var commit string
	for _, page := range pages {
		for _, r := range page {
			if r.User.Login != login || r.CommitID == "" || r.State == "PENDING" {
				continue
			}
			if cmp.Or(cmp.Compare(r.SubmittedAt, bestAt), cmp.Compare(r.ID, bestID)) > 0 {
				bestAt, bestID, commit = r.SubmittedAt, r.ID, r.CommitID
			}
		}
	}
	return commit, nil
}

// login is the user gh acts as.
func (g *GitHub) login(ctx context.Context) (string, error) {
	out, err := g.run(ctx, g.dir, "gh", "", "api", "user")
	if err != nil {
		return "", fmt.Errorf("current user: %w", err)
	}
	var v struct {
		Login string `json:"login"`
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		return "", fmt.Errorf("parse current user: %w", err)
	}
	return v.Login, nil
}
