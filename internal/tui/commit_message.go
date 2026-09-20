package tui

import (
	"context"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/yousysadmin/igit/internal/gitops"
	"github.com/yousysadmin/igit/internal/tui/overlay"
)

// commitHistoryLen is how many recent commit messages the prompt offers.
const commitHistoryLen = 20

// commitPromptMsg carries what the commit prompt needs: whether anything is
// staged, the message history and, for an amend, HEAD's message.
type commitPromptMsg struct {
	amend     bool
	hasStaged bool
	history   []string
	last      string
	err       error
}

// prepareCommitPrompt gathers prompt data off the UI goroutine. the prompt
// opens when commitPromptMsg arrives.
func (c *CommitModel) prepareCommitPrompt(amend bool) tea.Cmd {
	repo := c.repo
	return func() tea.Msg {
		ctx := context.Background()
		has, err := repo.HasStagedChanges(ctx)
		if err != nil {
			return commitPromptMsg{amend: amend, err: err}
		}
		history, err := repo.CommitMessages(ctx, commitHistoryLen)
		if err != nil {
			return commitPromptMsg{amend: amend, err: err}
		}
		msg := commitPromptMsg{amend: amend, hasStaged: has, history: history}
		if amend {
			msg.last, err = repo.LastCommitMessage(ctx)
			if err != nil {
				return commitPromptMsg{amend: amend, err: err}
			}
		}
		return msg
	}
}

func (c *CommitModel) handleCommitPrompt(msg commitPromptMsg) tea.Cmd {
	if msg.err != nil {
		c.showError("commit", msg.err)
		return nil
	}
	if !msg.amend && !msg.hasStaged {
		c.hint = "nothing staged: stage changes first"
		return nil
	}
	spec := overlay.PromptSpec{
		ID:          "commit",
		Title:       "commit message",
		Placeholder: "summary",
		History:     msg.history,
		Editor:      "edit full message in $EDITOR",
	}
	if msg.amend {
		spec.ID = "amend-edit"
		spec.Title = "amend: new message"
		spec.Initial = firstLineOf(msg.last)
		c.pending = pendingAction{id: "amend-edit", seed: msg.last}
	}
	c.overlay.OpenPrompt(spec)
	return nil
}

func firstLineOf(s string) string {
	head, _, _ := strings.Cut(s, "\n")
	return head
}

// commitOpts returns the configured commit options, with amend when asked.
func (c *CommitModel) commitOpts(amend bool) gitops.CommitOpts {
	return gitops.CommitOpts{Amend: amend, NoVerify: c.noVerify, Signoff: c.signoff}
}

// handlePromptSubmitted records the commit typed into the prompt.
func (c *CommitModel) handlePromptSubmitted(id, value string) tea.Cmd {
	msg := strings.TrimSpace(value)
	p := c.pending
	c.pending = pendingAction{}
	if cmd, ok := c.handleHistoryPrompt(id, value, p); ok {
		return cmd
	}
	switch id {
	case "commit":
		return c.commitWith(msg, c.commitOpts(false))
	case "amend-edit":
		// the prompt edits the subject only. keep the previous body when the
		// subject changed but a body existed
		if body := bodyOf(p.seed); body != "" {
			msg += "\n\n" + body
		}
		return c.commitWith(msg, c.commitOpts(true))
	}
	return nil
}

func bodyOf(s string) string {
	_, rest, found := strings.Cut(s, "\n")
	if !found {
		return ""
	}
	return strings.TrimSpace(rest)
}

// handlePromptEditor continues a prompt in $EDITOR: the typed text seeds the
// editor buffer of an interactive git commit.
func (c *CommitModel) handlePromptEditor(id, value string) tea.Cmd {
	p := c.pending
	c.pending = pendingAction{}
	seed := value
	if id == "amend-edit" {
		if body := bodyOf(p.seed); body != "" {
			seed = value + "\n\n" + body
		}
		return c.commitInEditorWith(c.commitOpts(true), seed)
	}
	return c.commitInEditor(seed)
}

// commitWith runs a non-interactive commit, or an interactive one when GPG
// signing may need the terminal for a passphrase.
func (c *CommitModel) commitWith(msg string, opts gitops.CommitOpts) tea.Cmd {
	name := "commit"
	if opts.Amend {
		name = "amend"
	}
	if msg != "" && c.repo.GPGSignEnabled(context.Background()) {
		cmd, cleanup, err := c.repo.CommitFileCmd(opts, msg)
		if err != nil {
			c.hint = "commit: " + err.Error()
			return nil
		}
		return c.runProcess(name, cmd, cleanup)
	}
	return c.runOp(name, func(ctx context.Context) error { return c.repo.Commit(ctx, msg, opts) })
}

// commitInEditor opens an interactive git commit whose message editor is
// seeded with seed (empty = git's default template).
func (c *CommitModel) commitInEditor(seed string) tea.Cmd {
	return c.commitInEditorWith(c.commitOpts(false), seed)
}

func (c *CommitModel) commitInEditorWith(opts gitops.CommitOpts, seed string) tea.Cmd {
	cmd, cleanup, err := c.repo.CommitEditorCmd(opts, seed)
	if err != nil {
		c.hint = "commit: " + err.Error()
		return nil
	}
	name := "commit"
	if opts.Amend {
		name = "amend"
	}
	return c.runProcess(name, cmd, cleanup)
}

// runProcess hands an interactive git command to the process runner and
// cleans up after it exits.
func (c *CommitModel) runProcess(name string, cmd *exec.Cmd, cleanup func()) tea.Cmd {
	restore := c.mouseTracking
	return c.run(cmd, func(runErr error) tea.Msg {
		if cleanup != nil {
			cleanup()
		}
		return commitProcessDoneMsg{name: name, err: runErr, restoreMouse: restore}
	})
}

// openAmendMenu offers the two amend flavors.
func (c *CommitModel) openAmendMenu() {
	if c.status.Head.Unborn {
		c.hint = "nothing to amend: no commits yet"
		return
	}
	c.overlay.OpenMenu(overlay.MenuSpec{Title: "amend HEAD", Items: []overlay.MenuItem{
		{Key: 'k', Label: "amend with staged changes, keep message", ID: "amend-keep"},
		{Key: 'e', Label: "amend and edit the message", ID: "amend-edit"},
	}})
}
