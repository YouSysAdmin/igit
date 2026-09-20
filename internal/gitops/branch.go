package gitops

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/yousysadmin/igit/internal/git"
)

// Branch is one local or remote-tracking branch.
type Branch struct {
	Name         string // short name: "main" or "origin/main"
	Upstream     string // "origin/main" for a local branch with tracking
	Hash         string // abbreviated commit
	Subject      string
	Ahead        int
	Behind       int
	UpstreamGone bool
	Head         bool // currently checked out
	Remote       bool // refs/remotes/...
	Date         time.Time
}

// Branches lists local branches first (most recent commit first), then
// remote-tracking branches, skipping the symbolic origin/HEAD.
func (g *Git) Branches(ctx context.Context) ([]Branch, error) {
	out, err := g.Run(ctx, git.RunOpts{Background: true}, "for-each-ref", "--sort=-committerdate",
		"--format=%(HEAD)%1f%(refname)%1f%(refname:short)%1f%(upstream:short)%1f%(upstream:track)%1f%(objectname:short)%1f%(committerdate:unix)%1f%(subject)%1f%(symref)",
		"refs/heads", "refs/remotes")
	if err != nil {
		return nil, err
	}
	return parseBranches(out), nil
}

var trackRe = regexp.MustCompile(`ahead (\d+)|behind (\d+)`)

func parseBranches(out string) []Branch {
	var local, remote []Branch
	for line := range strings.SplitSeq(strings.TrimRight(out, "\n"), "\n") {
		if line == "" {
			continue
		}
		f := strings.Split(line, "\x1f")
		if len(f) < 8 {
			continue
		}
		if len(f) > 8 && f[8] != "" {
			continue // symbolic ref such as origin/HEAD
		}
		b := Branch{
			Head:     f[0] == "*",
			Name:     f[2],
			Upstream: f[3],
			Hash:     f[5],
			Subject:  f[7],
			Remote:   strings.HasPrefix(f[1], "refs/remotes/"),
		}
		if ts, err := strconv.ParseInt(f[6], 10, 64); err == nil {
			b.Date = time.Unix(ts, 0)
		}
		track := f[4]
		if track == "[gone]" {
			b.UpstreamGone = true
		}
		for _, m := range trackRe.FindAllStringSubmatch(track, -1) {
			if m[1] != "" {
				b.Ahead, _ = strconv.Atoi(m[1])
			}
			if m[2] != "" {
				b.Behind, _ = strconv.Atoi(m[2])
			}
		}
		if b.Remote {
			remote = append(remote, b)
		} else {
			local = append(local, b)
		}
	}
	return append(local, remote...)
}

// Checkout switches to name (a branch, or a remote branch which git turns
// into a tracking branch). force discards local changes.
func (g *Git) Checkout(ctx context.Context, name string, force bool) error {
	args := []string{"checkout", "-q"}
	if force {
		args = append(args, "--force")
	}
	_, err := g.Run(ctx, git.RunOpts{}, append(args, name)...)
	return err
}

// CheckoutRemote checks out a remote-tracking branch as a local branch that
// tracks it. A plain checkout of "origin/main" resolves the ref to a commit and
// detaches HEAD, which is never what picking a branch from the list means. When
// the local branch is already there it is checked out as it stands, because
// --track would refuse to overwrite it.
func (g *Git) CheckoutRemote(ctx context.Context, remoteRef string, force bool) error {
	remotes, err := g.Remotes(ctx)
	if err != nil {
		return err
	}
	local := localBranchFor(remoteRef, remotes)
	if local == "" {
		return fmt.Errorf("checkout %s: no remote owns this branch", remoteRef)
	}
	if g.hasLocalBranch(ctx, local) {
		return g.Checkout(ctx, local, force)
	}
	args := []string{"checkout", "-q"}
	if force {
		args = append(args, "--force")
	}
	_, err = g.Run(ctx, git.RunOpts{}, append(args, "--track", remoteRef)...)
	return err
}

// localBranchFor strips the remote prefix from a remote-tracking branch name:
// "origin/feature" becomes "feature". A remote name may contain a slash, so the
// longest matching remote wins. Returns "" when no remote owns the name.
func localBranchFor(remoteRef string, remotes []string) string {
	best := ""
	for _, r := range remotes {
		if strings.HasPrefix(remoteRef, r+"/") && len(r) > len(best) {
			best = r
		}
	}
	if best == "" {
		return ""
	}
	return strings.TrimPrefix(remoteRef, best+"/")
}

// hasLocalBranch reports whether refs/heads/<name> exists.
func (g *Git) hasLocalBranch(ctx context.Context, name string) bool {
	_, err := g.Run(ctx, git.RunOpts{Background: true, OkExitCodes: []int{1}}, "show-ref", "--verify", "--quiet", "refs/heads/"+name)
	return err == nil
}

// CreateBranch creates name from base ("" = HEAD), optionally checking it out.
func (g *Git) CreateBranch(ctx context.Context, name, base string, checkout bool) error {
	var args []string
	if checkout {
		args = []string{"checkout", "-q", "-b", name}
	} else {
		args = []string{"branch", name}
	}
	if base != "" {
		args = append(args, base)
	}
	_, err := g.Run(ctx, git.RunOpts{}, args...)
	return err
}

// DeleteBranch deletes a local branch. force deletes unmerged branches too.
func (g *Git) DeleteBranch(ctx context.Context, name string, force bool) error {
	flag := "-d"
	if force {
		flag = "-D"
	}
	_, err := g.Run(ctx, git.RunOpts{}, "branch", flag, name)
	return err
}

// RenameBranch renames a local branch.
func (g *Git) RenameBranch(ctx context.Context, oldName, newName string) error {
	_, err := g.Run(ctx, git.RunOpts{}, "branch", "--move", oldName, newName)
	return err
}

// MergeOpts tunes `git merge`.
type MergeOpts struct {
	FFOnly bool
	NoFF   bool
}

// Merge merges name into the current branch without opening an editor.
func (g *Git) Merge(ctx context.Context, name string, o MergeOpts) error {
	args := []string{"merge", "--no-edit"}
	switch {
	case o.FFOnly:
		args = append(args, "--ff-only")
	case o.NoFF:
		args = append(args, "--no-ff")
	}
	_, err := g.Run(ctx, git.RunOpts{ExtraEnv: []string{"GIT_MERGE_AUTOEDIT=no"}}, append(args, name)...)
	return err
}

// RebaseOpts tunes `git rebase`.
type RebaseOpts struct {
	// Autostash stashes a dirty worktree for the duration of the rebase and
	// restores it afterwards, instead of git refusing to start.
	Autostash bool
}

// Rebase replays the current branch onto onto. Conflicts stop it with the
// worktree mid-rebase, which InProgress reports and AbortOperation or
// ContinueCmd settle, the same way a conflicted merge is handled.
func (g *Git) Rebase(ctx context.Context, onto string, o RebaseOpts) error {
	args := []string{"rebase"}
	if o.Autostash {
		args = append(args, "--autostash")
	}
	// the sequence editor is never needed for a plain rebase, pin it to a no-op
	// so a configured interactive editor cannot capture the terminal
	_, err := g.Run(ctx, git.RunOpts{ExtraEnv: []string{"GIT_SEQUENCE_EDITOR=:", "GIT_EDITOR=:"}}, append(args, onto)...)
	return err
}

// ResetMode is how far `git reset` unwinds: the branch pointer only, the index
// too, or the worktree as well.
type ResetMode string

const (
	ResetSoft  ResetMode = "--soft"  // move HEAD, keep the index and the worktree
	ResetMixed ResetMode = "--mixed" // move HEAD and the index, keep the worktree
	ResetHard  ResetMode = "--hard"  // move HEAD, the index and the worktree, discarding changes
)

// Reset points the current branch at ref. ResetHard discards uncommitted work,
// so callers confirm it first.
func (g *Git) Reset(ctx context.Context, ref string, mode ResetMode) error {
	if mode == "" {
		mode = ResetMixed
	}
	_, err := g.Run(ctx, git.RunOpts{}, "reset", string(mode), ref)
	return err
}

// SetUpstream points branch at remote/remoteBranch.
func (g *Git) SetUpstream(ctx context.Context, branch, remote, remoteBranch string) error {
	_, err := g.Run(ctx, git.RunOpts{}, "branch", fmt.Sprintf("--set-upstream-to=%s/%s", remote, remoteBranch), branch)
	return err
}

// Remotes lists the configured remote names.
func (g *Git) Remotes(ctx context.Context) ([]string, error) {
	out, err := g.Run(ctx, git.RunOpts{Background: true}, "remote")
	if err != nil {
		return nil, err
	}
	var names []string
	for line := range strings.SplitSeq(strings.TrimSpace(out), "\n") {
		if line != "" {
			names = append(names, line)
		}
	}
	return names, nil
}
