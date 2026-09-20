package tui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yousysadmin/igit/internal/annot"
	"github.com/yousysadmin/igit/internal/git"
	"github.com/yousysadmin/igit/internal/tui/mocks"
	"github.com/yousysadmin/igit/internal/tui/overlay"
	"github.com/yousysadmin/igit/internal/tui/style"
)

func TestModel_ReviewInfoStats(t *testing.T) {
	entries := []git.FileEntry{{Path: "a.go", Status: git.FileAdded}, {Path: "b.go", Status: git.FileModified}}
	r := &mocks.DiffSourceMock{
		ChangedFilesFunc: func(string, bool) ([]git.FileEntry, error) { return entries, nil },
		FileDiffFunc: func(req git.FileDiffRequest) ([]git.DiffLine, error) {
			switch req.Path {
			case "a.go":
				return []git.DiffLine{{ChangeType: git.ChangeAdd}, {ChangeType: git.ChangeAdd}}, nil
			case "b.go":
				return []git.DiffLine{{ChangeType: git.ChangeRemove}, {ChangeType: git.ChangeContext}}, nil
			default:
				return nil, nil
			}
		},
	}
	m := testNewModel(t, r, annot.NewStore(), noopHighlighter(), ModelConfig{ReviewInfo: &ReviewInfoConfig{}})
	m.setReviewEntries(entries)
	cmd := m.loadReviewStats(entries)
	require.NotNil(t, cmd)
	msg := cmd().(reviewStatsLoadedMsg)

	result, _ := m.Update(msg)
	model := result.(Model)
	assert.True(t, model.review.statsLoaded)
	assert.Equal(t, 2, model.review.adds)
	assert.Equal(t, 1, model.review.removes)
	assert.False(t, model.review.partial)
	assert.Equal(t, "A1 M1", model.reviewStatusText())
	assert.Equal(t, "+2/-1", model.reviewLinesText())

	spec := model.buildInfoSpec()
	// mode and stats moved from body rows to popup borders post-redesign.
	assert.Equal(t, "working tree changes", spec.HeaderText, "header summarizes the review mode")
	assert.Contains(t, spec.FooterText, "+2/-1", "footer carries aggregate +/- stats")
	assert.Contains(t, spec.FooterText, "A1 M1", "footer carries status histogram")
	assert.Contains(t, spec.FooterText, "2 files", "footer carries file count")
}

func TestModel_ReviewStatsLazyOnFirstOpen(t *testing.T) {
	// stats are deferred until the user opens the review-info overlay,
	// filesLoadedMsg must not compute them.
	entries := []git.FileEntry{{Path: "a.go", Status: git.FileModified}}
	calls := 0
	r := &mocks.DiffSourceMock{
		ChangedFilesFunc: func(string, bool) ([]git.FileEntry, error) { return entries, nil },
		FileDiffFunc: func(git.FileDiffRequest) ([]git.DiffLine, error) {
			calls++
			return []git.DiffLine{{ChangeType: git.ChangeAdd}}, nil
		},
	}
	m := testNewModel(t, r, annot.NewStore(), noopHighlighter(), ModelConfig{ReviewInfo: &ReviewInfoConfig{}})

	// drive the files-loaded path directly so FileDiff calls reflect post-load state
	result, _ := m.Update(filesLoadedMsg{entries: entries})
	m = result.(Model)
	assert.False(t, m.review.statsRequested, "stats must not be requested before the overlay is opened")

	// reset the FileDiff call counter - only the lazy stats fetch should bump it
	calls = 0

	statsCmd := m.triggerReviewStats()
	require.NotNil(t, statsCmd, "first open must produce a stats fetch command")
	assert.True(t, m.review.statsRequested)

	// run the lazy stats fetch - now FileDiff is called for review aggregation
	statsMsg := statsCmd().(reviewStatsLoadedMsg)
	assert.Equal(t, len(entries), calls, "lazy stats fetch must call FileDiff once per entry")
	result, _ = m.Update(statsMsg)
	m = result.(Model)
	assert.True(t, m.review.statsLoaded)

	// second open does NOT re-fetch
	assert.Nil(t, m.triggerReviewStats(), "second open within the same load generation must not re-fetch")
}

func TestModel_ReviewStatsEarlyInfoOpenFetchesAfterFilesLoad(t *testing.T) {
	entries := []git.FileEntry{{Path: "a.go", Status: git.FileModified}}
	r := &mocks.DiffSourceMock{
		ChangedFilesFunc: func(string, bool) ([]git.FileEntry, error) { return entries, nil },
		FileDiffFunc: func(git.FileDiffRequest) ([]git.DiffLine, error) {
			return []git.DiffLine{{ChangeType: git.ChangeAdd}}, nil
		},
	}
	m := testNewModel(t, r, annot.NewStore(), noopHighlighter(), ModelConfig{ReviewInfo: &ReviewInfoConfig{}})

	cmd := m.handleInfo()
	assert.Nil(t, cmd, "files are not loaded yet, so the early open should defer stats fetch")
	assert.True(t, m.review.statsRequested)
	assert.False(t, m.review.statsLoaded)

	result, cmd := m.Update(filesLoadedMsg{entries: entries})
	m = result.(Model)
	require.NotNil(t, cmd, "filesLoaded after an early info open must include the deferred stats fetch")

	batch, ok := cmd().(tea.BatchMsg)
	require.True(t, ok, "initial file load and deferred stats fetch should be batched")
	var gotStats *reviewStatsLoadedMsg
	for _, inner := range batch {
		if msg, ok := inner().(reviewStatsLoadedMsg); ok {
			gotStats = &msg
			break
		}
	}
	require.NotNil(t, gotStats, "batch must include a reviewStatsLoadedMsg")
	assert.Equal(t, 1, gotStats.Adds)
	assert.Equal(t, 0, gotStats.Removes)
}

func TestModel_ReviewStatsStaleSeqDropped(t *testing.T) {
	m := testModel(nil, nil)
	m.review.cfg = &ReviewInfoConfig{}
	m.review.statsLoadSeq = 5

	// stale message (older seq) is ignored
	stale := reviewStatsLoadedMsg{seq: 4, Adds: 99, Removes: 99, Err: errors.New("stale")}
	result, _ := m.Update(stale)
	model := result.(Model)
	assert.False(t, model.review.statsLoaded, "stale stats must not flip statsLoaded")
	assert.Equal(t, 0, model.review.adds, "stale stats must not populate counts")
	assert.Equal(t, 0, model.review.removes)
	require.NoError(t, model.review.err)

	// fresh message (matching seq) is accepted
	fresh := reviewStatsLoadedMsg{seq: 5, Adds: 7, Removes: 3}
	result, _ = m.Update(fresh)
	model = result.(Model)
	assert.True(t, model.review.statsLoaded)
	assert.Equal(t, 7, model.review.adds)
	assert.Equal(t, 3, model.review.removes)
}

func TestModel_ReviewStatsErrorState(t *testing.T) {
	m := testModel(nil, nil)
	m.review.cfg = &ReviewInfoConfig{}
	m.review.statsLoadSeq = 1

	msg := reviewStatsLoadedMsg{seq: 1, Err: errors.New("boom")}
	result, _ := m.Update(msg)
	model := result.(Model)

	assert.True(t, model.review.statsLoaded)
	assert.Equal(t, "stats unavailable", model.reviewLinesText())
	assert.Contains(t, model.reviewRows(), overlay.InfoRow{Label: "stats", Value: "unavailable: boom"})
}

func TestModel_ReviewStatsAddedFallback(t *testing.T) {
	// FileAdded entries with empty unstaged diff must retry against the index
	// (staged=true). When that fallback succeeds, lines count toward stats.
	entries := []git.FileEntry{{Path: "newfile.go", Status: git.FileAdded}}
	r := &mocks.DiffSourceMock{
		ChangedFilesFunc: func(string, bool) ([]git.FileEntry, error) { return entries, nil },
		FileDiffFunc: func(req git.FileDiffRequest) ([]git.DiffLine, error) {
			if req.Staged {
				return []git.DiffLine{{ChangeType: git.ChangeAdd}, {ChangeType: git.ChangeAdd}}, nil
			}
			return nil, nil
		},
	}
	m := testNewModel(t, r, annot.NewStore(), noopHighlighter(), ModelConfig{ReviewInfo: &ReviewInfoConfig{}})
	m.setReviewEntries(entries)

	cmd := m.loadReviewStats(entries)
	require.NotNil(t, cmd)
	stats := cmd().(reviewStatsLoadedMsg)
	assert.Equal(t, 2, stats.Adds, "added-file fallback to staged diff must contribute to stats")
	assert.False(t, stats.Partial)
}

func TestModel_ReviewStatsAddedFallbackErrorMarksPartial(t *testing.T) {
	// when the staged-fallback fetch itself errors, the file must be flagged
	// as partial rather than silently treated as zero-line.
	entries := []git.FileEntry{{Path: "newfile.go", Status: git.FileAdded}}
	r := &mocks.DiffSourceMock{
		ChangedFilesFunc: func(string, bool) ([]git.FileEntry, error) { return entries, nil },
		FileDiffFunc: func(req git.FileDiffRequest) ([]git.DiffLine, error) {
			if req.Staged {
				return nil, errors.New("staged unavailable")
			}
			return nil, nil
		},
	}
	m := testNewModel(t, r, annot.NewStore(), noopHighlighter(), ModelConfig{ReviewInfo: &ReviewInfoConfig{}})
	m.setReviewEntries(entries)

	stats := m.loadReviewStats(entries)().(reviewStatsLoadedMsg)
	assert.True(t, stats.Partial, "staged-fallback error must mark stats as partial")
	require.NoError(t, stats.Err, "fallback failures must not surface as a fatal err")

	result, _ := m.Update(stats)
	model := result.(Model)
	assert.Contains(t, model.reviewLinesText(), "(partial)", "reviewLinesText must annotate partial state")
}

func TestModel_ReviewStatsUntrackedFallbackOutsideWorkDir(t *testing.T) {
	// untracked file paths must not escape workDir even if they contain "..".
	// the safeWorkDirPath guard rejects, partial flag is set, and the file
	// contributes zero lines instead of being read off-tree.
	entries := []git.FileEntry{{Path: "../../etc/passwd", Status: git.FileUntracked}}
	r := &mocks.DiffSourceMock{
		ChangedFilesFunc: func(string, bool) ([]git.FileEntry, error) { return entries, nil },
		FileDiffFunc:     func(git.FileDiffRequest) ([]git.DiffLine, error) { return nil, nil },
	}
	m := testNewModel(t, r, annot.NewStore(), noopHighlighter(), ModelConfig{
		WorkDir:    t.TempDir(),
		ReviewInfo: &ReviewInfoConfig{},
	})
	m.setReviewEntries(entries)

	stats := m.loadReviewStats(entries)().(reviewStatsLoadedMsg)
	assert.True(t, stats.Partial, "path-escape must mark partial")
	assert.Equal(t, 0, stats.Adds)
	assert.Equal(t, 0, stats.Removes)
}

func TestReviewLinesText_NoChangedLines(t *testing.T) {
	m := testModel(nil, nil)
	m.review.cfg = &ReviewInfoConfig{}
	m.review.statsLoaded = true
	assert.Equal(t, "no changed lines", m.reviewLinesText())

	m.review.partial = true
	assert.Equal(t, "no changed lines (partial)", m.reviewLinesText())
}

func TestReviewHeaderText(t *testing.T) {
	tests := []struct {
		name string
		cfg  *ReviewInfoConfig
		want string
	}{
		{name: "nil config returns empty string", cfg: nil, want: ""},
		{name: "staged", cfg: &ReviewInfoConfig{Staged: true}, want: "staged changes"},
		{name: "working tree", cfg: &ReviewInfoConfig{}, want: "working tree changes"},
		{name: "ref range", cfg: &ReviewInfoConfig{Ref: "main..feature"}, want: "ref range: main..feature"},
		{name: "single ref", cfg: &ReviewInfoConfig{Ref: "HEAD~3"}, want: "changes against HEAD~3"},
		{name: "standalone file-only review", cfg: &ReviewInfoConfig{Standalone: true}, want: "standalone files"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := testModel(nil, nil)
			m.review.cfg = tt.cfg
			assert.Equal(t, tt.want, m.reviewHeaderText())
		})
	}
}

func TestReviewFooterText(t *testing.T) {
	t.Run("includes files lines status", func(t *testing.T) {
		m := testModel(nil, nil)
		m.review.cfg = &ReviewInfoConfig{}
		m.review.entries = make([]git.FileEntry, 22)
		m.review.statusCounts = map[git.ChangeStatus]int{git.FileAdded: 2, git.FileModified: 20}
		m.review.adds = 231
		m.review.removes = 18
		m.review.statsLoaded = true

		got := m.reviewFooterText()
		assert.Contains(t, got, "22 files")
		assert.Contains(t, got, "+231/-18")
		assert.Contains(t, got, "A2 M20")
		assert.Contains(t, got, " · ", "segments joined with middot separator")
	})

	t.Run("empty config returns empty", func(t *testing.T) {
		m := testModel(nil, nil)
		m.review.cfg = nil
		assert.Empty(t, m.reviewFooterText())
	})

	t.Run("disabled config with files loaded still returns empty", func(t *testing.T) {
		// Enabled=false is the off-switch for the entire review-info subsystem.
		// once files load the footer must STILL stay empty so it cannot get
		// stuck on "loading..." (triggerReviewStats short-circuits when disabled,
		// so stats never resolve in this state).
		m := testModel(nil, nil)
		m.review.cfg = nil
		m.review.entries = make([]git.FileEntry, 5)
		m.review.statusCounts = map[git.ChangeStatus]int{git.FileModified: 5}
		assert.Empty(t, m.reviewFooterText())
	})
}

func TestModel_InfoOverlay_RefreshesOnStatsLoad(t *testing.T) {
	// when the user opens the info popup before the lazy stats
	// fetch lands, the popup must refresh inline so the "loading..." footer
	// flips to the totals - without this, the spec captured at open() time
	// stays stale forever and the user has to dismiss/reopen.
	entries := []git.FileEntry{{Path: "a.go", Status: git.FileModified}}
	r := &mocks.DiffSourceMock{
		ChangedFilesFunc: func(string, bool) ([]git.FileEntry, error) { return entries, nil },
		FileDiffFunc: func(git.FileDiffRequest) ([]git.DiffLine, error) {
			return []git.DiffLine{{ChangeType: git.ChangeAdd}, {ChangeType: git.ChangeAdd}, {ChangeType: git.ChangeRemove}}, nil
		},
	}
	mgr := overlay.NewManager()
	m := testNewModel(t, r, annot.NewStore(), noopHighlighter(), ModelConfig{
		Overlay:    mgr,
		ReviewInfo: &ReviewInfoConfig{},
	})
	// drive setReviewEntries directly to simulate post-files-load state
	m.setReviewEntries(entries)

	// open popup with stats unloaded - footer should report loading
	cmd := m.handleInfo()
	require.NotNil(t, cmd, "first open must trigger a stats fetch")
	require.True(t, mgr.Active())
	require.Equal(t, overlay.KindInfo, mgr.Kind())

	// run the lazy fetch and dispatch the result
	statsMsg := cmd().(reviewStatsLoadedMsg)
	result, _ := m.Update(statsMsg)
	m = result.(Model)

	// after the stats land, the open popup's spec must reflect the totals,
	// not the original "loading..." snapshot. Build a fresh bg of plain spaces
	// and call Compose. the rendered popup is overlaid in the middle.
	ctx := overlay.RenderCtx{Width: 100, Height: 20, Resolver: style.PlainResolver()}
	bgRow := strings.Repeat(" ", 100)
	bg := strings.Repeat(bgRow+"\n", 20)
	out := mgr.Compose(bg, ctx)
	assert.Contains(t, out, "+2/-1", "open popup must update inline once stats land")
	assert.NotContains(t, out, "loading…", "loading placeholder must clear after refresh")
}
