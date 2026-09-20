package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
)

// literalPathspecs makes git treat every path argument as a literal filename.
// Without it a tracked file whose name begins with ":(" or contains a glob
// character is parsed as a pathspec expression and selects a different file,
// or no file at all.
const literalPathspecs = "GIT_LITERAL_PATHSPECS=1"

// conflictingPathspecEnv lists the global pathspec settings git refuses to combine
// with literal mode: with either of them set truthy in the environment every
// command dies with "global 'literal' pathspec setting is incompatible with all
// other global pathspec settings". GIT_NOGLOB_PATHSPECS is absent on purpose,
// git accepts it alongside literal mode.
var conflictingPathspecEnv = []string{"GIT_GLOB_PATHSPECS=", "GIT_ICASE_PATHSPECS="}

// GitEnv returns the environment for a child git process: the current environment
// with literal pathspec handling forced on and the settings that conflict with it
// removed. Paths reach git straight from the file listing or from the user, so a
// file actually named ":(top)x" or "*.go" must select itself rather than be parsed
// as a pathspec expression. nothing here relies on pathspec magic. Dropping the
// conflicting entries is harmless, since neither affects a literal path.
func GitEnv() []string {
	env := os.Environ()
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		if slices.ContainsFunc(conflictingPathspecEnv, func(p string) bool { return strings.HasPrefix(kv, p) }) {
			continue
		}
		out = append(out, kv)
	}
	return append(out, literalPathspecs)
}

// Runner executes git inside one working tree. Both the read layer in this
// package and the write layer in internal/gitops go through it, so the
// environment and the error shape are the same in review and commit mode.
type Runner struct {
	workDir string
}

// NewRunner returns a Runner bound to workDir (the repository root or any
// directory inside it).
func NewRunner(workDir string) *Runner { return &Runner{workDir: workDir} }

// WorkDir returns the directory commands run in.
func (r *Runner) WorkDir() string { return r.workDir }

// RunOpts tunes one non-interactive invocation.
type RunOpts struct {
	Stdin       io.Reader // fed to the process, e.g. a patch or a commit message
	ExtraEnv    []string  // appended to the standard environment
	Background  bool      // sets GIT_OPTIONAL_LOCKS=0 for polling reads that must not block on index.lock
	OkExitCodes []int     // exit codes that are not failures (e.g. 1 for `diff --quiet`)
	// GlobPathspecs keeps git's default pathspec magic instead of forcing
	// GIT_LITERAL_PATHSPECS=1. `git stash push` runs sub-commands with its own
	// pathspecs internally and silently drops untracked files under the
	// literal setting.
	GlobPathspecs bool
}

// Run executes git with the standard environment, capturing stdout and stderr
// separately. A non-zero exit that is not in OkExitCodes becomes an *Error.
func (r *Runner) Run(ctx context.Context, o RunOpts, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...) //nolint:gosec // args are constructed internally, never from user text
	cmd.Dir = r.workDir
	cmd.Env = r.env(o.Background, o.ExtraEnv, true)
	if o.GlobPathspecs {
		cmd.Env = slices.DeleteFunc(cmd.Env, func(kv string) bool { return strings.HasPrefix(kv, "GIT_LITERAL_PATHSPECS=") })
	}
	cmd.Stdin = o.Stdin
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if err == nil {
		return stdout.String(), nil
	}
	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		code := exitErr.ExitCode()
		if slices.Contains(o.OkExitCodes, code) {
			return stdout.String(), nil
		}
		return "", &Error{Args: args, Stderr: stderr.String(), ExitCode: code, Err: err}
	}
	return "", &Error{Args: args, Stderr: stderr.String(), ExitCode: -1, Err: err}
}

// Command returns an interactive git command bound to the working tree, for
// operations that must own the terminal (editor commits, push/pull with
// credential prompts). GIT_TERMINAL_PROMPT stays enabled. The caller runs it
// through tea.ExecProcess.
func (r *Runner) Command(extraEnv []string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(context.Background(), "git", args...) //nolint:gosec // args are constructed internally, never from user text
	cmd.Dir = r.workDir
	cmd.Env = r.env(false, extraEnv, false)
	return cmd
}

// env builds the process environment: the literal-pathspec environment plus,
// for non-interactive runs, GIT_TERMINAL_PROMPT=0, and GIT_OPTIONAL_LOCKS=0
// for background polling.
func (r *Runner) env(background bool, extra []string, noPrompt bool) []string {
	env := GitEnv()
	if noPrompt {
		env = append(env, "GIT_TERMINAL_PROMPT=0")
	}
	if background {
		env = append(env, "GIT_OPTIONAL_LOCKS=0")
	}
	return append(env, extra...)
}

// Error is a failed git invocation. Error() is short enough for a status
// line. Detail() carries the full stderr for a popup.
type Error struct {
	Args     []string
	Stderr   string
	ExitCode int
	Err      error
}

// Error returns "git <args>: <first stderr line>".
func (e *Error) Error() string {
	msg := firstLine(e.Stderr)
	if msg == "" && e.Err != nil {
		msg = e.Err.Error()
	}
	if msg == "" {
		msg = fmt.Sprintf("exit status %d", e.ExitCode)
	}
	return "git " + strings.Join(e.Args, " ") + ": " + msg
}

// Unwrap exposes the underlying exec error.
func (e *Error) Unwrap() error { return e.Err }

// Detail returns the command line followed by the complete stderr output.
func (e *Error) Detail() string {
	var b strings.Builder
	b.WriteString("$ git " + strings.Join(e.Args, " ") + "\n")
	if s := strings.TrimRight(e.Stderr, "\n"); s != "" {
		b.WriteString(s)
	} else if e.Err != nil {
		b.WriteString(e.Err.Error())
	}
	return b.String()
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if before, _, ok := strings.Cut(s, "\n"); ok {
		return before
	}
	return s
}
