package gitops

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/yousysadmin/igit/internal/git"
)

// CommitOpts tunes `git commit`.
type CommitOpts struct {
	Amend      bool // --amend
	NoEdit     bool // --no-edit (with Amend: keep the message)
	NoVerify   bool // --no-verify: skip pre-commit and commit-msg hooks
	Signoff    bool // --signoff
	AllowEmpty bool // --allow-empty
}

func (o CommitOpts) args() []string {
	args := []string{"commit"}
	if o.Amend {
		args = append(args, "--amend")
	}
	if o.NoEdit {
		args = append(args, "--no-edit")
	}
	if o.NoVerify {
		args = append(args, "--no-verify")
	}
	if o.Signoff {
		args = append(args, "--signoff")
	}
	if o.AllowEmpty {
		args = append(args, "--allow-empty")
	}
	return args
}

// Commit records the index with msg read from stdin, so multi-line and
// unicode messages need no shell quoting. The message is cleaned up as
// whitespace only (trailing blanks, collapsed empty lines) so lines starting
// with # survive. An empty msg with NoEdit (amend) keeps the previous message.
// Hooks run unless NoVerify is set.
func (g *Git) Commit(ctx context.Context, msg string, o CommitOpts) error {
	args := o.args()
	opts := git.RunOpts{}
	if msg != "" {
		args = append(args, "--cleanup=whitespace", "-F", "-")
		opts.Stdin = strings.NewReader(msg)
	} else if !o.NoEdit {
		return errors.New("commit: empty message")
	}
	_, err := g.Run(ctx, opts, args...)
	return err
}

// CommitEditorCmd returns an interactive `git commit` that opens the user's
// editor for the message. the caller runs it through tea.ExecProcess. When
// seed is non-empty it is written to a temp file and offered as the initial
// message (--edit --file). cleanup removes that file and is safe to call
// unconditionally.
func (g *Git) CommitEditorCmd(o CommitOpts, seed string) (cmd *exec.Cmd, cleanup func(), err error) {
	args := o.args()
	cleanup = func() {}
	if seed != "" {
		f, ferr := os.CreateTemp("", "igit-commit-msg-*.txt")
		if ferr != nil {
			return nil, cleanup, fmt.Errorf("commit message temp file: %w", ferr)
		}
		if _, werr := f.WriteString(seed); werr != nil {
			_ = f.Close()
			_ = os.Remove(f.Name())
			return nil, cleanup, fmt.Errorf("commit message temp file: %w", werr)
		}
		if cerr := f.Close(); cerr != nil {
			_ = os.Remove(f.Name())
			return nil, cleanup, fmt.Errorf("commit message temp file: %w", cerr)
		}
		name := f.Name()
		cleanup = func() { _ = os.Remove(name) }
		args = append(args, "--edit", "--file="+name)
	}
	return g.Command(nil, args...), cleanup, nil
}

// LastCommitMessage returns HEAD's full message, or "" on an unborn branch.
func (g *Git) LastCommitMessage(ctx context.Context) (string, error) {
	out, err := g.Run(ctx, git.RunOpts{Background: true}, "log", "-1", "--pretty=%B")
	if err != nil {
		if strings.Contains(err.Error(), "does not have any commits") {
			return "", nil
		}
		return "", err
	}
	return strings.TrimRight(out, "\n"), nil
}

// CommitMessages returns the messages of the n most recent commits, newest
// first, for the commit prompt's history. An unborn branch yields none.
func (g *Git) CommitMessages(ctx context.Context, n int) ([]string, error) {
	if n <= 0 {
		return nil, nil
	}
	out, err := g.Run(ctx, git.RunOpts{Background: true}, "log", "-n", strconv.Itoa(n), "-z", "--pretty=format:%B")
	if err != nil {
		if strings.Contains(err.Error(), "does not have any commits") {
			return nil, nil
		}
		return nil, err
	}
	var msgs []string
	for m := range strings.SplitSeq(out, "\x00") {
		if m = strings.TrimRight(m, "\n"); m != "" {
			msgs = append(msgs, m)
		}
	}
	return msgs, nil
}

// HasStagedChanges reports whether the index differs from HEAD. On an unborn
// branch it reports whether the index has any entry.
func (g *Git) HasStagedChanges(ctx context.Context) (bool, error) {
	out, err := g.Run(ctx, git.RunOpts{Background: true, OkExitCodes: []int{1}}, "diff", "--cached", "--quiet", "--exit-code")
	if err == nil {
		// exit 0 (clean) and exit 1 (changes) both land here. tell them apart with a listing
		names, lerr := g.Run(ctx, git.RunOpts{Background: true}, "diff", "--cached", "--name-only")
		if lerr != nil {
			return false, lerr
		}
		return strings.TrimSpace(names) != "" || strings.TrimSpace(out) != "", nil
	}
	if strings.Contains(err.Error(), "does not have any commits") || strings.Contains(err.Error(), "bad revision") {
		names, lerr := g.Run(ctx, git.RunOpts{Background: true}, "ls-files", "--cached")
		if lerr != nil {
			return false, lerr
		}
		return strings.TrimSpace(names) != "", nil
	}
	return false, err
}

// GPGSignEnabled reports whether commit.gpgsign is on, in which case a commit
// may need the terminal for a passphrase prompt.
func (g *Git) GPGSignEnabled(ctx context.Context) bool {
	out, err := g.Run(ctx, git.RunOpts{Background: true, OkExitCodes: []int{1}}, "config", "--get", "--type=bool", "commit.gpgsign")
	return err == nil && strings.TrimSpace(out) == "true"
}

// CommitFileCmd returns an interactive `git commit -F <tmp>` that records msg
// without opening an editor but with the terminal attached, for repositories
// whose commit.gpgsign needs a passphrase prompt. cleanup removes the temp file.
func (g *Git) CommitFileCmd(o CommitOpts, msg string) (cmd *exec.Cmd, cleanup func(), err error) {
	f, ferr := os.CreateTemp("", "igit-commit-msg-*.txt")
	if ferr != nil {
		return nil, func() {}, fmt.Errorf("commit message temp file: %w", ferr)
	}
	name := f.Name()
	cleanup = func() { _ = os.Remove(name) }
	if _, werr := f.WriteString(msg); werr != nil {
		_ = f.Close()
		cleanup()
		return nil, func() {}, fmt.Errorf("commit message temp file: %w", werr)
	}
	if cerr := f.Close(); cerr != nil {
		cleanup()
		return nil, func() {}, fmt.Errorf("commit message temp file: %w", cerr)
	}
	args := append(o.args(), "--cleanup=whitespace", "-F", name)
	return g.Command(nil, args...), cleanup, nil
}
