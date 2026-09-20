package gitops

import "os/exec"

// PushOpts tunes `git push`.
type PushOpts struct {
	Remote         string // "" = the configured upstream remote
	Branch         string // remote branch to push the current branch to, "" = same name
	SetUpstream    bool   // --set-upstream
	ForceWithLease bool   // --force-with-lease
}

// PullOpts tunes `git pull`.
type PullOpts struct {
	Remote string
	Branch string
	FFOnly bool
	Rebase bool
}

// PushCmd builds an interactive push so credential and ssh prompts reach the
// terminal. the caller runs it through tea.ExecProcess.
func (g *Git) PushCmd(o PushOpts) *exec.Cmd {
	args := []string{"push"}
	if o.ForceWithLease {
		args = append(args, "--force-with-lease")
	}
	if o.SetUpstream {
		args = append(args, "--set-upstream")
	}
	if o.Remote != "" {
		args = append(args, o.Remote)
		if o.Branch != "" {
			args = append(args, "HEAD:"+o.Branch)
		} else {
			args = append(args, "HEAD")
		}
	}
	return g.Command(nil, args...)
}

// PullCmd builds an interactive pull. GIT_SEQUENCE_EDITOR is disabled so a
// rebase pull never opens an editor.
func (g *Git) PullCmd(o PullOpts) *exec.Cmd {
	args := []string{"pull", "--no-edit"}
	if o.FFOnly {
		args = append(args, "--ff-only")
	}
	if o.Rebase {
		args = append(args, "--rebase")
	}
	if o.Remote != "" {
		args = append(args, o.Remote)
		if o.Branch != "" {
			args = append(args, o.Branch)
		}
	}
	return g.Command([]string{"GIT_SEQUENCE_EDITOR=:"}, args...)
}

// FetchCmd builds an interactive fetch.
func (g *Git) FetchCmd(all, prune bool) *exec.Cmd {
	args := []string{"fetch", "--no-write-fetch-head"}
	if all {
		args = append(args, "--all")
	}
	if prune {
		args = append(args, "--prune")
	}
	return g.Command(nil, args...)
}
