package tui

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/yousysadmin/igit/internal/gitops"
	"github.com/yousysadmin/igit/internal/keymap"
	"github.com/yousysadmin/igit/internal/tui/overlay"
)

// syncDoneMsg reports a finished push/pull/fetch with the stderr git printed.
type syncDoneMsg struct {
	name         string
	err          error
	stderr       string
	restoreMouse bool
}

// openInProgressMenu offers the ways out of an unfinished merge, rebase,
// cherry-pick, revert or bisect.
func (c *CommitModel) openInProgressMenu() {
	op := c.status.InProgress
	if !op.Active() {
		c.hint = "nothing in progress: no merge, rebase or cherry-pick to finish"
		return
	}
	items := []overlay.MenuItem{}
	if op.CanContinue() {
		items = append(items, overlay.MenuItem{Key: 'c', Label: "continue, opens the message in $EDITOR", ID: "op-continue"})
	}
	label := "abort and restore the branch"
	if op.State == gitops.StateBisecting {
		label = "reset the bisect"
	}
	items = append(items, overlay.MenuItem{Key: 'a', Label: label, ID: "op-abort"})
	c.overlay.OpenMenu(overlay.MenuSpec{Title: op.Label(), Items: items})
}

// handleInProgressMenu acts on the abort/continue menu. ok is false for others.
func (c *CommitModel) handleInProgressMenu(choice string) (tea.Cmd, bool) {
	state := c.status.InProgress.State
	switch choice {
	case "op-abort":
		return c.runOp("abort "+string(state), func(ctx context.Context) error { return c.repo.AbortOperation(ctx, state) }), true
	case "op-continue":
		cmd, err := c.repo.ContinueCmd(state)
		if err != nil {
			c.hint = err.Error()
			return nil, true
		}
		return c.runSync("continue "+string(state), cmd), true
	}
	return nil, false
}

// handleSyncAction runs push, pull or fetch interactively so credential and
// ssh prompts reach the terminal. Push offers a menu when the branch has no
// upstream or when a force push may be wanted.
func (c *CommitModel) handleSyncAction(action keymap.Action) tea.Cmd {
	switch action { //nolint:exhaustive // only the sync actions reach here
	case keymap.ActionFetch:
		return c.runSync("fetch", c.repo.FetchCmd(true, true))
	case keymap.ActionPull:
		if c.status.Head.Upstream == "" {
			c.hint = "no upstream configured: set one with U on the Branches tab"
			return nil
		}
		return c.runSync("pull", c.repo.PullCmd(gitops.PullOpts{}))
	case keymap.ActionPush:
		c.openPushMenu()
	case keymap.ActionAbortOrContinue:
		c.openInProgressMenu()
	}
	return nil
}

// openPushMenu offers the push variants for the current branch.
func (c *CommitModel) openPushMenu() {
	if c.status.Head.Detached || c.status.Head.Unborn {
		c.hint = "nothing to push: check out a branch with commits first"
		return
	}
	items := []overlay.MenuItem{}
	if c.status.Head.Upstream == "" {
		items = append(items, overlay.MenuItem{Key: 'u', Label: "push and set upstream (origin/" + c.status.Head.Name + ")", ID: "push-upstream"})
	} else {
		items = append(items, overlay.MenuItem{Key: 'p', Label: "push to " + c.status.Head.Upstream, ID: "push"})
	}
	items = append(items, overlay.MenuItem{Key: 'f', Label: "force push (--force-with-lease)", ID: "push-force"})
	c.overlay.OpenMenu(overlay.MenuSpec{Title: "push " + c.status.Head.Name, Items: items})
}

// handlePushMenu acts on the push menu. ok is false for other menus.
func (c *CommitModel) handlePushMenu(choice string) (tea.Cmd, bool) {
	switch choice {
	case "push":
		return c.runSync("push", c.repo.PushCmd(gitops.PushOpts{})), true
	case "push-upstream":
		return c.runSync("push", c.repo.PushCmd(gitops.PushOpts{Remote: "origin", SetUpstream: true})), true
	case "push-force":
		remote := ""
		if c.status.Head.Upstream == "" {
			remote = "origin"
		}
		return c.runSync("force push", c.repo.PushCmd(gitops.PushOpts{Remote: remote, ForceWithLease: true})), true
	}
	return nil, false
}

// runSync executes an interactive git command, mirroring its stderr into a
// buffer so the result can be summarized afterwards.
func (c *CommitModel) runSync(name string, cmd *exec.Cmd) tea.Cmd {
	var buf bytes.Buffer
	if cmd.Stderr == nil {
		cmd.Stderr = io.MultiWriter(os.Stderr, &buf)
	}
	restore := c.mouseTracking
	return c.run(cmd, func(runErr error) tea.Msg {
		return syncDoneMsg{name: name, err: runErr, stderr: buf.String(), restoreMouse: restore}
	})
}

func (c *CommitModel) handleSyncDone(msg syncDoneMsg) tea.Cmd {
	var cmds []tea.Cmd
	if msg.restoreMouse {
		cmds = append(cmds, tea.EnableMouseCellMotion)
	}
	if msg.err != nil {
		c.overlay.OpenError(overlay.ErrorSpec{Title: msg.name + " failed", Summary: msg.err.Error(), Detail: strings.TrimSpace(msg.stderr)})
	} else {
		c.hint = msg.name + " done"
	}
	c.hist.loaded[tabBranches] = false // tracking info changed
	cmds = append(cmds, c.Refresh())
	return tea.Batch(cmds...)
}
