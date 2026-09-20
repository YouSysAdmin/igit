package gitops

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/yousysadmin/igit/internal/git"
)

// RepoState names an operation git left half-finished in the working tree. The
// sequencer files under the git directory are the only record of it, porcelain
// status does not report one.
type RepoState string

const (
	StateNone       RepoState = ""
	StateMerging    RepoState = "merging"
	StateRebasing   RepoState = "rebasing"
	StateCherryPick RepoState = "cherry-picking"
	StateReverting  RepoState = "reverting"
	StateBisecting  RepoState = "bisecting"
)

// InProgress describes the unfinished operation, if any. Step and Total are
// set for a rebase, which is the only one that counts its way through a list.
type InProgress struct {
	State RepoState
	Step  int
	Total int
}

// Active reports whether an operation is waiting to be finished or abandoned.
func (p InProgress) Active() bool { return p.State != StateNone }

// Label names the operation for the status bar: "rebasing 2/5" or "merging".
func (p InProgress) Label() string {
	if !p.Active() {
		return ""
	}
	if p.Total > 0 {
		return fmt.Sprintf("%s %d/%d", p.State, p.Step, p.Total)
	}
	return string(p.State)
}

// CanContinue reports whether the operation has a --continue step. A bisect is
// advanced with good/bad rather than continued, so it only offers the reset.
func (p InProgress) CanContinue() bool {
	return p.Active() && p.State != StateBisecting
}

// gitDir resolves the repository's git directory, which is not always
// workDir/.git: a worktree or a submodule keeps it elsewhere.
func (g *Git) gitDir(ctx context.Context) (string, error) {
	out, err := g.Run(ctx, git.RunOpts{Background: true}, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// InProgress reports the operation the working tree is in the middle of, or a
// zero value when there is none. Cheaper than a full Status: one ref lookup and
// a handful of stats.
func (g *Git) InProgress(ctx context.Context) InProgress { return g.readInProgress(ctx) }

// readInProgress reports the unfinished operation. A rebase is checked first:
// it leaves CHERRY_PICK_HEAD behind while a step conflicts, so the more
// specific state has to win.
func (g *Git) readInProgress(ctx context.Context) InProgress {
	dir, err := g.gitDir(ctx)
	if err != nil {
		return InProgress{}
	}
	exists := func(name string) bool {
		_, statErr := os.Stat(filepath.Join(dir, name))
		return statErr == nil
	}
	switch {
	case exists("rebase-merge"):
		step, total := rebaseProgress(dir, "rebase-merge", "msgnum", "end")
		return InProgress{State: StateRebasing, Step: step, Total: total}
	case exists("rebase-apply"):
		step, total := rebaseProgress(dir, "rebase-apply", "next", "last")
		return InProgress{State: StateRebasing, Step: step, Total: total}
	case exists("CHERRY_PICK_HEAD"):
		return InProgress{State: StateCherryPick}
	case exists("REVERT_HEAD"):
		return InProgress{State: StateReverting}
	case exists("MERGE_HEAD"):
		return InProgress{State: StateMerging}
	case exists("BISECT_LOG"):
		return InProgress{State: StateBisecting}
	}
	return InProgress{}
}

// rebaseProgress reads the step counters a rebase keeps in its state directory.
// Both are absent on the very first step, which reports as no progress.
func rebaseProgress(gitDir, stateDir, stepFile, totalFile string) (step, total int) {
	read := func(name string) int {
		raw, err := os.ReadFile(filepath.Join(gitDir, stateDir, name)) //nolint:gosec // path built from the resolved git dir
		if err != nil {
			return 0
		}
		n, convErr := strconv.Atoi(strings.TrimSpace(string(raw)))
		if convErr != nil {
			return 0
		}
		return n
	}
	return read(stepFile), read(totalFile)
}

// abortArgs is the git command that abandons the operation and restores the
// branch. Bisect is reset rather than aborted.
func abortArgs(state RepoState) []string {
	switch state {
	case StateMerging:
		return []string{"merge", "--abort"}
	case StateRebasing:
		return []string{"rebase", "--abort"}
	case StateCherryPick:
		return []string{"cherry-pick", "--abort"}
	case StateReverting:
		return []string{"revert", "--abort"}
	case StateBisecting:
		return []string{"bisect", "reset"}
	case StateNone:
	}
	return nil
}

// AbortOperation abandons the unfinished operation. It touches no editor, so it
// runs like any other non-interactive command.
func (g *Git) AbortOperation(ctx context.Context, state RepoState) error {
	args := abortArgs(state)
	if args == nil {
		return errors.New("nothing to abort")
	}
	_, err := g.Run(ctx, git.RunOpts{}, args...)
	return err
}

// ContinueCmd is the command that finishes the current step. It opens the
// commit message in $EDITOR, so the caller runs it through the interactive
// path the way it runs an editor commit.
func (g *Git) ContinueCmd(state RepoState) (*exec.Cmd, error) {
	var args []string
	switch state {
	case StateMerging:
		args = []string{"merge", "--continue"}
	case StateRebasing:
		args = []string{"rebase", "--continue"}
	case StateCherryPick:
		args = []string{"cherry-pick", "--continue"}
	case StateReverting:
		args = []string{"revert", "--continue"}
	case StateBisecting, StateNone:
		return nil, errors.New("nothing to continue")
	}
	return g.Command(nil, args...), nil
}
