package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yousysadmin/igit/internal/annot"
	"github.com/yousysadmin/igit/internal/git"
	"github.com/yousysadmin/igit/internal/gitops"
	"github.com/yousysadmin/igit/internal/keymap"
	"github.com/yousysadmin/igit/internal/tui/overlay"
)

// e2eRepo creates an isolated git repository with one committed file that is
// modified in the worktree in two separate places, plus an untracked file.
func e2eRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	run := func(args ...string) {
		cmd := exec.Command("git", args...) //nolint:gosec // test helper with fixed arguments
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
	}
	run("init", "-q", "-b", "main")
	run("config", "user.email", "t@e.com")
	run("config", "user.name", "T")
	run("config", "commit.gpgsign", "false")
	write := func(name, content string) {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600))
	}
	write("a.txt", "l1\nl2\nl3\nl4\nl5\nl6\nl7\nl8\n")
	write("whole.txt", "w\n")
	run("add", ".")
	run("commit", "-q", "-m", "init")
	write("a.txt", "L1\nl2\nl3\nl4\nl5\nl6\nMID\nl8\nL9\n")
	write("whole.txt", "W\n")
	write("new.txt", "new\n")
	return dir
}

// drain runs cmd and feeds every resulting message back into the app until
// no command is left, so async loads complete synchronously in tests.
func drain(t *testing.T, app App, cmd tea.Cmd) App {
	t.Helper()
	queue := []tea.Cmd{cmd}
	for len(queue) > 0 {
		c := queue[0]
		queue = queue[1:]
		if c == nil {
			continue
		}
		msg := c()
		switch m := msg.(type) {
		case nil:
		case tea.BatchMsg:
			queue = append(queue, m...)
		default:
			next, follow := app.Update(msg)
			var ok bool
			app, ok = next.(App)
			require.True(t, ok)
			queue = append(queue, follow)
		}
	}
	return app
}

func keys(t *testing.T, app App, ks ...string) App {
	t.Helper()
	for _, k := range ks {
		var msg tea.KeyMsg
		switch k {
		case "enter":
			msg = tea.KeyMsg{Type: tea.KeyEnter}
		case "esc":
			msg = tea.KeyMsg{Type: tea.KeyEsc}
		case "alt+g":
			msg = altG()
		default:
			msg = keyRunes(k)
		}
		next, cmd := app.Update(msg)
		var ok bool
		app, ok = next.(App)
		require.True(t, ok)
		app = drain(t, app, cmd)
	}
	return app
}

func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...) //nolint:gosec // test helper with fixed arguments
	cmd.Dir = dir
	out, err := cmd.Output()
	require.NoError(t, err)
	return string(out)
}

func TestApp_E2E_ReviewMarksFlowIntoCommitMode(t *testing.T) {
	dir := e2eRepo(t)
	src := git.NewGit(dir)
	repo := gitops.New(dir)
	store := annot.NewStore()
	review := testModelConfig(store)
	review.DiffSource = src
	review.WorkDir = dir
	review.Applicable.StagePlan = true
	review.LoadUntracked = src.UntrackedFiles
	review.ShowUntracked = true
	commit := &CommitConfig{Repo: repo, DiffPane: review, Keymap: keymap.DefaultCommit(), Overlay: overlay.NewManager(), RepoRoot: dir}
	app, err := NewApp(AppConfig{Review: review, Commit: commit, PlanApplier: repo})
	require.NoError(t, err)
	app, _ = updateApp(t, app, tea.WindowSizeMsg{Width: 140, Height: 40})
	app = drain(t, app, app.Init())
	require.True(t, app.review.filesLoaded)
	assert.Equal(t, []string{"a.txt", "new.txt", "whole.txt"}, app.review.tree.VisibleFiles())

	// a.txt is selected and loaded. focus the diff and mark the MID block
	app.review.tree.SelectByPath("a.txt")
	next, loadCmd := app.review.loadSelectedIfChanged()
	app.review = next.(Model)
	app = drain(t, app, loadCmd)
	require.Equal(t, "a.txt", app.review.file.name)
	app = keys(t, app, "l")
	// find the MID row and mark its block
	for i, dl := range app.review.file.lines {
		if dl.Content == "MID" {
			app.review.nav.diffCursor = i
		}
	}
	app = keys(t, app, "s")
	assert.True(t, app.review.StagePlan().Has("a.txt"))
	// mark whole.txt as a whole from the tree
	app = keys(t, app, "h")
	app.review.tree.SelectByPath("whole.txt")
	app = keys(t, app, "S")
	assert.True(t, app.review.StagePlan().IsWhole("whole.txt"))
	assert.Contains(t, app.review.transientHint(), "marked whole.txt")
	app.review.stage.hint = ""
	view := app.View()
	assert.Contains(t, view, "stage: 2")
	assert.Contains(t, view, "a.txt +")
	assert.Contains(t, view, "whole.txt +")

	// c applies the plan and lands in commit mode on the Staged section
	app = keys(t, app, "c")
	assert.Equal(t, ModeCommit, app.Mode())
	assert.True(t, app.review.StagePlan().Empty(), "everything was staged")
	cached := gitOutput(t, dir, "diff", "--cached", "--no-color", "-U0")
	assert.Contains(t, cached, "-l7\n+MID\n")
	assert.NotContains(t, cached, "+L1\n", "the first block was not marked")
	assert.Contains(t, cached, "-w\n+W\n")
	fr, ok := app.commit.cursorFile()
	require.True(t, ok)
	assert.True(t, fr.staged)
	view = app.View()
	assert.Contains(t, view, "Staged (2)")
	assert.Contains(t, view, "Unstaged (2)")
	assert.Contains(t, view, "staged 2 marked file(s)")

	// stage the untracked file from commit mode and commit everything
	app.commit.list.SelectByKey("u:new.txt")
	app = keys(t, app, " ")
	assert.Contains(t, gitOutput(t, dir, "status", "--porcelain"), "A  new.txt")
	app = keys(t, app, "c")
	require.Equal(t, overlay.KindPrompt, app.commit.overlay.Kind())
	app = keys(t, app, "feat: e2e", "enter")
	assert.Contains(t, gitOutput(t, dir, "log", "-1", "--pretty=%s"), "feat: e2e")
	assert.Contains(t, gitOutput(t, dir, "status", "--porcelain"), " M a.txt", "unmarked block stays unstaged")

	// back to review: the file list refreshes without the committed files
	app = keys(t, app, "alt+g")
	assert.Equal(t, ModeReview, app.Mode())
	assert.Equal(t, []string{"a.txt"}, app.review.tree.VisibleFiles())
}

func TestApp_E2E_HistoryTabsAgainstRepo(t *testing.T) {
	dir := e2eRepo(t)
	run := func(args ...string) {
		cmd := exec.Command("git", args...) //nolint:gosec // test helper with fixed arguments
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
	}
	run("stash", "push", "-q", "-m", "smoke stash", "--", "whole.txt")
	run("branch", "side")
	src := git.NewGit(dir)
	repo := gitops.New(dir)
	review := testModelConfig(annot.NewStore())
	review.DiffSource = src
	review.WorkDir = dir
	commit := &CommitConfig{Repo: repo, DiffPane: review, Keymap: keymap.DefaultCommit(), Overlay: overlay.NewManager(), RepoRoot: dir}
	app, err := NewApp(AppConfig{Review: review, Commit: commit, PlanApplier: repo, StartMode: ModeCommit})
	require.NoError(t, err)
	app, _ = updateApp(t, app, tea.WindowSizeMsg{Width: 140, Height: 40})
	app = drain(t, app, app.Init())
	require.Equal(t, ModeCommit, app.Mode())

	app = keys(t, app, "3")
	view := app.View()
	assert.Contains(t, view, "(main, side) init", "log shows the commit with its decorations")
	assert.Contains(t, view, "a.txt", "every file of the commit is in the diff pane")
	assert.Contains(t, view, "whole.txt")
	app = keys(t, app, "enter")
	require.NotNil(t, app.commit.hist.detail)
	assert.Contains(t, app.View(), "whole.txt")

	app = keys(t, app, "esc", "4")
	view = app.View()
	assert.Contains(t, view, "{0} On main: smoke stash")
	assert.Contains(t, view, "whole.txt", "the stash diff covers its files")
	assert.Contains(t, app.commit.diff.file.name, "stash@{0}", "the header names the stash entry")

	app = keys(t, app, "2")
	view = app.View()
	assert.Contains(t, view, "* main")
	assert.Contains(t, view, "side")
}
