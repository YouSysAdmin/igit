package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yousysadmin/igit/internal/annot"
	"github.com/yousysadmin/igit/internal/keymap"
	"github.com/yousysadmin/igit/internal/tui/mocks"
)

func TestNewModel_OutputPath(t *testing.T) {
	tests := []struct {
		name string
		path string
	}{
		{name: "present", path: "/tmp/review.md"},
		{name: "empty", path: ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := testNewModel(t, &mocks.DiffSourceMock{}, annot.NewStore(), noopHighlighter(), ModelConfig{OutputPath: tc.path})
			assert.Equal(t, tc.path, m.session.outputPath)
			assert.Empty(t, m.output.hint)
		})
	}
}

func TestModel_HandleFlushOutput_EmptyStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.md")
	m := testNewModel(t, plainRenderer(), annot.NewStore(), noopHighlighter(), ModelConfig{})

	result, cmd := m.handleFlushOutput()
	model := result.(Model)
	assert.Equal(t, "No annotations to flush", model.output.hint)
	assert.Nil(t, cmd)
	assert.NoFileExists(t, path, "empty store must not create the output file")
}

func TestModel_HandleFlushOutput_Modes(t *testing.T) {
	tests := []struct {
		name       string
		withPath   bool
		wantHint   string
		wantOutput bool
	}{
		{name: "no destination", wantHint: "Output flush requires -o/--output or --output-dir"},
		{name: "output path", withPath: true, wantHint: "Wrote 1 annotation to output file", wantOutput: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := annot.NewStore()
			store.Add(annot.Annotation{File: "a.go", Line: 1, Type: "+", Comment: "note"})
			path := filepath.Join(t.TempDir(), "out.md")
			cfg := ModelConfig{}
			if tc.withPath {
				cfg.OutputPath = path
			}
			m := testNewModel(t, plainRenderer(), store, noopHighlighter(), cfg)

			result, cmd := m.handleFlushOutput()
			model := result.(Model)
			assert.Equal(t, tc.wantHint, model.output.hint)
			assert.Nil(t, cmd)
			if tc.wantOutput {
				assert.FileExists(t, path)
			} else {
				assert.NoFileExists(t, path)
			}
		})
	}
}

func TestModel_HandleFlushOutput_Success(t *testing.T) {
	tests := []struct {
		name     string
		anns     []annot.Annotation
		wantHint string
	}{
		{
			name:     "single",
			anns:     []annot.Annotation{{File: "a.go", Line: 1, Type: "+", Comment: "note"}},
			wantHint: "Wrote 1 annotation to output file",
		},
		{
			name: "multiple",
			anns: []annot.Annotation{
				{File: "a.go", Line: 1, Type: "+", Comment: "note"},
				{File: "b.go", Line: 5, Type: " ", Comment: "check"},
			},
			wantHint: "Wrote 2 annotations to output file",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := annot.NewStore()
			for _, a := range tc.anns {
				store.Add(a)
			}
			path := filepath.Join(t.TempDir(), "out.md")
			m := testNewModel(t, plainRenderer(), store, noopHighlighter(), ModelConfig{OutputPath: path})

			result, cmd := m.handleFlushOutput()
			model := result.(Model)
			assert.Equal(t, tc.wantHint, model.output.hint)
			assert.Nil(t, cmd)

			got, err := os.ReadFile(path) //nolint:gosec // path is a t.TempDir() file
			require.NoError(t, err)
			assert.Equal(t, store.FormatOutput(), string(got), "written file must match FormatOutput")
			assert.Equal(t, len(tc.anns), store.Count(), "flush must not mutate the store")
		})
	}
}

func TestModel_HandleFlushOutput_WriteError(t *testing.T) {
	store := annot.NewStore()
	store.Add(annot.Annotation{File: "a.go", Line: 1, Type: "+", Comment: "note"})
	blocker := filepath.Join(t.TempDir(), "blocker")
	require.NoError(t, os.WriteFile(blocker, []byte("x"), 0o600))
	path := filepath.Join(blocker, "out.md") // parent is a regular file: cannot be created
	m := testNewModel(t, plainRenderer(), store, noopHighlighter(), ModelConfig{OutputPath: path})

	result, cmd := m.handleFlushOutput()
	model := result.(Model)
	assert.Equal(t, "Flush failed", model.output.hint)
	assert.Nil(t, cmd)
	assert.NoFileExists(t, path)
}

func TestModel_ActionFlushOutput_Dispatch(t *testing.T) {
	store := annot.NewStore()
	store.Add(annot.Annotation{File: "a.go", Line: 1, Type: "+", Comment: "note"})
	path := filepath.Join(t.TempDir(), "out.md")
	m := testNewModel(t, plainRenderer(), store, noopHighlighter(), ModelConfig{OutputPath: path})

	result, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'O'}})
	model := result.(Model)
	assert.Equal(t, "Wrote 1 annotation to output file", model.output.hint)
	assert.FileExists(t, path, "O key must flush annotations to the output file")
}

func TestModel_OutputHint_ShownInStatusBar(t *testing.T) {
	m := testModel([]string{"a.go"}, nil)
	m.output.hint = "test output hint"
	assert.Equal(t, "test output hint", m.transientHint())
}

func TestModel_OutputHint_ClearsOnNextKey(t *testing.T) {
	m := testModel([]string{"a.go"}, nil)
	m.output.hint = "some hint"

	result, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	model := result.(Model)
	assert.Empty(t, model.output.hint, "any key press must clear the output hint")
}

func keyRunes(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

func TestModel_EffectiveOutputPath_Precedence(t *testing.T) {
	m := testNewModel(t, plainRenderer(), annot.NewStore(), noopHighlighter(), ModelConfig{
		OutputPath:        "/flag.md",
		DefaultOutputPath: func() string { return "/dir/default.md" },
	})
	assert.Equal(t, "/flag.md", m.effectiveOutputPath(), "--output beats --output-dir")
	m.output.path = "/session.md"
	assert.Equal(t, "/session.md", m.effectiveOutputPath(), "session path wins")
	assert.Equal(t, "/session.md", m.OutputPath())

	m2 := testNewModel(t, plainRenderer(), annot.NewStore(), noopHighlighter(), ModelConfig{
		DefaultOutputPath: func() string { return "/dir/default.md" },
	})
	assert.Equal(t, "/dir/default.md", m2.effectiveOutputPath())

	m3 := testNewModel(t, plainRenderer(), annot.NewStore(), noopHighlighter(), ModelConfig{})
	assert.Empty(t, m3.effectiveOutputPath())
	assert.Empty(t, m3.OutputPath())
}

func TestModel_SaveAsPrefill(t *testing.T) {
	m := testNewModel(t, plainRenderer(), annot.NewStore(), noopHighlighter(), ModelConfig{
		SaveAsPath: func() string { return "/cwd/repo-ts.md" },
	})
	assert.Equal(t, "/cwd/repo-ts.md", m.saveAsPrefill(), "suggestion used when nothing is configured")
	m.session.outputPath = "/flag.md"
	assert.Equal(t, "/flag.md", m.saveAsPrefill(), "configured path beats suggestion")
	m4 := testNewModel(t, plainRenderer(), annot.NewStore(), noopHighlighter(), ModelConfig{})
	assert.Empty(t, m4.saveAsPrefill())
}

func TestModel_StartSaveAs_EmptyStore(t *testing.T) {
	m := testNewModel(t, plainRenderer(), annot.NewStore(), noopHighlighter(), ModelConfig{})
	result, cmd := m.startSaveAs()
	model := result.(Model)
	assert.Nil(t, cmd)
	assert.False(t, model.output.saving)
	assert.Equal(t, "No annotations to save", model.output.hint)
}

func TestModel_SaveAs_PromptFlow(t *testing.T) {
	store := annot.NewStore()
	store.Add(annot.Annotation{File: "a.go", Line: 1, Type: "+", Comment: "note"})
	dir := t.TempDir()
	prefill := filepath.Join(dir, "prefill.md")
	m := testNewModel(t, plainRenderer(), store, noopHighlighter(), ModelConfig{
		SaveAsPath: func() string { return prefill },
	})
	m.layout.width = 100

	// ctrl+s opens the prompt with the suggestion prefilled
	result, _ := m.dispatchAction(keymap.ActionSaveAs)
	model := result.(Model)
	require.True(t, model.output.saving)
	assert.Equal(t, prefill, model.output.input.Value())
	assert.Contains(t, model.statusBarText(), "save to: "+prefill)

	// typing edits the path. mouse events are swallowed while the prompt is open
	result, _ = model.Update(keyRunes("x"))
	model = result.(Model)
	assert.True(t, model.output.saving)
	assert.Equal(t, prefill+"x", model.output.input.Value())
	result, cmd := model.handleMouse(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown})
	model = result.(Model)
	assert.Nil(t, cmd)
	assert.True(t, model.output.saving, "mouse must not dismiss the prompt")

	// enter writes the file, records the path and reports it
	result, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = result.(Model)
	assert.False(t, model.output.saving)
	assert.FileExists(t, prefill+"x")
	assert.Equal(t, prefill+"x", model.OutputPath())
	assert.Equal(t, "Wrote 1 annotation to "+prefill+"x", model.output.hint)

	// a later O flush reuses the chosen path
	store.Add(annot.Annotation{File: "a.go", Line: 2, Type: "+", Comment: "more"})
	result, _ = model.handleFlushOutput()
	model = result.(Model)
	assert.Equal(t, "Wrote 2 annotations to output file", model.output.hint)
	got, err := os.ReadFile(prefill + "x") //nolint:gosec // test reads a file under t.TempDir
	require.NoError(t, err)
	assert.Equal(t, store.FormatOutput(), string(got))
}

func TestModel_SaveAs_EscCancels(t *testing.T) {
	store := annot.NewStore()
	store.Add(annot.Annotation{File: "a.go", Line: 1, Type: "+", Comment: "note"})
	path := filepath.Join(t.TempDir(), "never.md")
	m := testNewModel(t, plainRenderer(), store, noopHighlighter(), ModelConfig{SaveAsPath: func() string { return path }})
	result, _ := m.startSaveAs()
	result, _ = result.(Model).Update(tea.KeyMsg{Type: tea.KeyEsc})
	model := result.(Model)
	assert.False(t, model.output.saving)
	assert.Equal(t, "Save canceled", model.output.hint)
	assert.NoFileExists(t, path)
	assert.Empty(t, model.OutputPath())
}

func TestModel_SaveAs_EmptyPathCancels(t *testing.T) {
	store := annot.NewStore()
	store.Add(annot.Annotation{File: "a.go", Line: 1, Type: "+", Comment: "note"})
	m := testNewModel(t, plainRenderer(), store, noopHighlighter(), ModelConfig{})
	result, _ := m.startSaveAs()
	model := result.(Model)
	require.True(t, model.output.saving)
	assert.Empty(t, model.output.input.Value())
	result, _ = model.handleSaveAsKey(tea.KeyMsg{Type: tea.KeyEnter})
	model = result.(Model)
	assert.False(t, model.output.saving)
	assert.Equal(t, "Save canceled", model.output.hint)
}

func TestModel_SaveAs_NoStatusBarWritesDirectly(t *testing.T) {
	store := annot.NewStore()
	store.Add(annot.Annotation{File: "a.go", Line: 1, Type: "+", Comment: "note"})
	path := filepath.Join(t.TempDir(), "direct.md")
	m := testNewModel(t, plainRenderer(), store, noopHighlighter(), ModelConfig{
		NoStatusBar: true,
		SaveAsPath:  func() string { return path },
	})
	result, cmd := m.startSaveAs()
	model := result.(Model)
	assert.Nil(t, cmd)
	assert.False(t, model.output.saving)
	assert.FileExists(t, path)
	assert.Equal(t, path, model.OutputPath())

	m2 := testNewModel(t, plainRenderer(), store, noopHighlighter(), ModelConfig{NoStatusBar: true})
	result, _ = m2.startSaveAs()
	assert.Equal(t, "No output path configured", result.(Model).output.hint)
}

func TestModel_SaveAs_WriteError(t *testing.T) {
	store := annot.NewStore()
	store.Add(annot.Annotation{File: "a.go", Line: 1, Type: "+", Comment: "note"})
	blocker := filepath.Join(t.TempDir(), "blocker")
	require.NoError(t, os.WriteFile(blocker, []byte("x"), 0o600))
	m := testNewModel(t, plainRenderer(), store, noopHighlighter(), ModelConfig{})
	model := m.writeAnnotationsTo(filepath.Join(blocker, "out.md"))
	assert.Contains(t, model.output.hint, "Save failed")
	assert.Empty(t, model.OutputPath())
}

func TestModel_HandleFlushOutput_DefaultOutputDirPinsPath(t *testing.T) {
	store := annot.NewStore()
	store.Add(annot.Annotation{File: "a.go", Line: 1, Type: "+", Comment: "note"})
	calls := 0
	dir := t.TempDir()
	m := testNewModel(t, plainRenderer(), store, noopHighlighter(), ModelConfig{
		DefaultOutputPath: func() string {
			calls++
			return filepath.Join(dir, fmt.Sprintf("out-%d.md", calls))
		},
	})
	result, _ := m.handleFlushOutput()
	model := result.(Model)
	assert.Equal(t, filepath.Join(dir, "out-1.md"), model.OutputPath())
	result, _ = model.handleFlushOutput()
	model = result.(Model)
	assert.Equal(t, filepath.Join(dir, "out-1.md"), model.OutputPath(), "second flush reuses the pinned file")
	assert.NoFileExists(t, filepath.Join(dir, "out-2.md"))
}
