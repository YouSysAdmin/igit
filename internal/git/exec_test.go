package git

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupRunnerRepo creates an isolated git repository with one committed file
// and returns a Runner for it. Global and system git config are disabled so the
// developer's settings (gpg signing, hooks, autocrlf) cannot leak in.
func setupRunnerRepo(t *testing.T) *Runner {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	runnerGit(t, dir, "init", "-q", "-b", "main")
	runnerGit(t, dir, "config", "user.email", "test@example.com")
	runnerGit(t, dir, "config", "user.name", "Test")
	writeRunnerFile(t, dir, "a.txt", "one\ntwo\nthree\n")
	runnerGit(t, dir, "add", "a.txt")
	runnerGit(t, dir, "commit", "-q", "-m", "init")
	return NewRunner(dir)
}

func runnerGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...) //nolint:gosec // test helper with fixed arguments
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
}

func writeRunnerFile(t *testing.T, dir, name, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600))
}

func TestRun_capturesStdoutAndStderrSeparately(t *testing.T) {
	r := setupRunnerRepo(t)
	out, err := r.Run(t.Context(), RunOpts{}, "rev-parse", "--abbrev-ref", "HEAD")
	require.NoError(t, err)
	assert.Equal(t, "main\n", out)

	_, err = r.Run(t.Context(), RunOpts{}, "rev-parse", "--verify", "refs/heads/nope")
	require.Error(t, err)
	var gerr *Error
	require.ErrorAs(t, err, &gerr)
	assert.Equal(t, []string{"rev-parse", "--verify", "refs/heads/nope"}, gerr.Args)
	assert.Equal(t, 128, gerr.ExitCode)
	assert.Contains(t, gerr.Stderr, "fatal")
	assert.True(t, strings.HasPrefix(gerr.Error(), "git rev-parse --verify refs/heads/nope: fatal"), gerr.Error())
	assert.Contains(t, gerr.Detail(), "$ git rev-parse")
	var exitErr *exec.ExitError
	assert.ErrorAs(t, err, &exitErr, "Unwrap exposes the exec error")
}

func TestRun_okExitCodesAndStdin(t *testing.T) {
	r := setupRunnerRepo(t)
	// diff --quiet exits 1 when there are changes. with OkExitCodes that is a success
	writeRunnerFile(t, r.WorkDir(), "a.txt", "one\nTWO\nthree\n")
	_, err := r.Run(t.Context(), RunOpts{}, "diff", "--quiet")
	require.Error(t, err)
	_, err = r.Run(t.Context(), RunOpts{OkExitCodes: []int{1}}, "diff", "--quiet")
	require.NoError(t, err)

	// stdin reaches the process
	out, err := r.Run(t.Context(), RunOpts{Stdin: strings.NewReader("blob content")}, "hash-object", "--stdin")
	require.NoError(t, err)
	assert.Len(t, strings.TrimSpace(out), 40)
}

func TestRun_missingBinaryOrDir(t *testing.T) {
	r := NewRunner(filepath.Join(t.TempDir(), "missing"))
	_, err := r.Run(t.Context(), RunOpts{}, "status")
	require.Error(t, err)
	var gerr *Error
	require.ErrorAs(t, err, &gerr)
	assert.Equal(t, -1, gerr.ExitCode)
	assert.NotEmpty(t, gerr.Error())
	assert.NotEmpty(t, gerr.Detail())
}

func TestError_messages(t *testing.T) {
	e := &Error{Args: []string{"add", "x"}, Stderr: "fatal: pathspec 'x' did not match\nsecond line\n", ExitCode: 128}
	assert.Equal(t, "git add x: fatal: pathspec 'x' did not match", e.Error())
	assert.Equal(t, "$ git add x\nfatal: pathspec 'x' did not match\nsecond line", e.Detail())
	e2 := &Error{Args: []string{"add"}, ExitCode: 3}
	assert.Equal(t, "git add: exit status 3", e2.Error())
	assert.Equal(t, "$ git add\n", e2.Detail())
	e3 := &Error{Args: []string{"add"}, Err: errors.New("boom")}
	assert.Equal(t, "git add: boom", e3.Error())
	assert.Equal(t, "$ git add\nboom", e3.Detail())
}

func TestEnv(t *testing.T) {
	r := NewRunner(t.TempDir())
	env := r.env(true, []string{"X=1"}, true)
	assert.Contains(t, env, "GIT_LITERAL_PATHSPECS=1")
	assert.Contains(t, env, "GIT_TERMINAL_PROMPT=0")
	assert.Contains(t, env, "GIT_OPTIONAL_LOCKS=0")
	assert.Equal(t, "X=1", env[len(env)-1])
	interactive := r.env(false, nil, false)
	assert.NotContains(t, interactive, "GIT_TERMINAL_PROMPT=0")
	assert.NotContains(t, interactive, "GIT_OPTIONAL_LOCKS=0")
}

func TestCommand_isInteractive(t *testing.T) {
	r := setupRunnerRepo(t)
	cmd := r.Command([]string{"GIT_SEQUENCE_EDITOR=:"}, "commit", "--amend")
	assert.Equal(t, r.WorkDir(), cmd.Dir)
	assert.Equal(t, []string{"git", "commit", "--amend"}, cmd.Args)
	assert.Contains(t, cmd.Env, "GIT_SEQUENCE_EDITOR=:")
	assert.NotContains(t, cmd.Env, "GIT_TERMINAL_PROMPT=0")
}
