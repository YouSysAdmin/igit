package tui

//go:generate moq -out mocks/repo.go -pkg mocks -skip-ensure -fmt goimports . Repo

import (
	"context"
	"os/exec"

	"github.com/yousysadmin/igit/internal/conflict"
	"github.com/yousysadmin/igit/internal/git"
	"github.com/yousysadmin/igit/internal/gitops"
)

// StatusReader reads the working tree state and per-file staging diffs.
type StatusReader interface {
	Status(ctx context.Context) (gitops.Status, error)
	StagingDiff(ctx context.Context, spec gitops.DiffSpec) (string, error)
}

// Stager changes the index and worktree.
type Stager interface {
	StageFiles(ctx context.Context, paths []string) error
	StageAll(ctx context.Context) error
	UnstageFiles(ctx context.Context, paths []string, unborn bool) error
	UnstageAll(ctx context.Context, unborn bool) error
	Discard(ctx context.Context, f gitops.StatusEntry, scope gitops.DiscardScope, unborn bool) error
	ApplyLines(ctx context.Context, req gitops.LineRequest) error
}

// Committer creates and amends commits.
type Committer interface {
	Commit(ctx context.Context, msg string, o gitops.CommitOpts) error
	CommitEditorCmd(o gitops.CommitOpts, seed string) (cmd *exec.Cmd, cleanup func(), err error)
	CommitFileCmd(o gitops.CommitOpts, msg string) (cmd *exec.Cmd, cleanup func(), err error)
	LastCommitMessage(ctx context.Context) (string, error)
	CommitMessages(ctx context.Context, n int) ([]string, error)
	HasStagedChanges(ctx context.Context) (bool, error)
	GPGSignEnabled(ctx context.Context) bool
}

// Stasher manages the stash.
type Stasher interface {
	Stashes(ctx context.Context) ([]gitops.StashEntry, error)
	StashPush(ctx context.Context, o gitops.StashPushOpts) error
	StashPop(ctx context.Context, index int) error
	StashApply(ctx context.Context, index int) error
	StashDrop(ctx context.Context, index int) error
	StashFiles(ctx context.Context, index int) ([]git.FileEntry, error)
	StashStagedSupported(ctx context.Context) bool
}

// Brancher lists and manipulates branches.
type Brancher interface {
	Branches(ctx context.Context) ([]gitops.Branch, error)
	Checkout(ctx context.Context, name string, force bool) error
	CheckoutRemote(ctx context.Context, remoteRef string, force bool) error
	ResolveConflict(ctx context.Context, path string, ours bool) error
	ConflictRegions(path string) ([]conflict.Region, error)
	ResolveConflictRegion(path string, index int, choice conflict.Choice) error
	UnstageDuringMerge(ctx context.Context, path string, unborn bool) error
	AbortOperation(ctx context.Context, state gitops.RepoState) error
	ContinueCmd(state gitops.RepoState) (*exec.Cmd, error)
	CreateBranch(ctx context.Context, name, base string, checkout bool) error
	DeleteBranch(ctx context.Context, name string, force bool) error
	RenameBranch(ctx context.Context, oldName, newName string) error
	Merge(ctx context.Context, name string, o gitops.MergeOpts) error
	Rebase(ctx context.Context, onto string, o gitops.RebaseOpts) error
	Reset(ctx context.Context, ref string, mode gitops.ResetMode) error
	SetUpstream(ctx context.Context, branch, remote, remoteBranch string) error
	Remotes(ctx context.Context) ([]string, error)
}

// LogReader reads the commit history.
type LogReader interface {
	Log(ctx context.Context, ref string, limit int) ([]gitops.Commit, error)
	CommitFiles(ctx context.Context, hash string) ([]git.FileEntry, error)
	RangeDiff(ctx context.Context, ref string, contextLines int) (string, error)
}

// Syncer builds the interactive push/pull/fetch commands.
type Syncer interface {
	PushCmd(o gitops.PushOpts) *exec.Cmd
	PullCmd(o gitops.PullOpts) *exec.Cmd
	FetchCmd(all, prune bool) *exec.Cmd
}

// Repo is everything commit mode needs from the git write layer. *gitops.Git
// implements it. tests use the generated mock.
type Repo interface {
	StatusReader
	Stager
	Committer
	Stasher
	Brancher
	LogReader
	Syncer
}
