package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// PickItem is one row of a PickList: ID comes back as the choice, Label is
// what the filter matches, Detail is the muted text after the label.
type PickItem struct {
	ID     string
	Label  string
	Detail string
}

// PickList is a small standalone bubbletea program that lets the user choose
// one item before the main TUI starts (for example the pull request to review
// when the current branch has none). Typing filters the rows, up/down or
// ctrl+p/ctrl+n move, enter picks, esc leaves with no choice.
type PickList struct {
	title    string
	items    []PickItem
	visible  []int // indices into items that match the filter
	cursor   int
	input    textinput.Model
	chosen   string
	canceled bool
	width    int
	height   int
}

// NewPickList builds the picker with every item visible.
func NewPickList(title string, items []PickItem) PickList {
	ti := textinput.New()
	ti.Placeholder = "type to filter"
	ti.Prompt = "/ "
	ti.Focus()
	p := PickList{title: title, items: items, input: ti, width: 80, height: 24}
	p.refilter()
	return p
}

// Chosen returns the picked item's ID, "" when the picker was canceled.
func (p PickList) Chosen() string { return p.chosen }

// Canceled reports whether the user left without choosing.
func (p PickList) Canceled() bool { return p.canceled }

// Init starts the cursor blink of the filter input.
func (p PickList) Init() tea.Cmd { return textinput.Blink }

// Update handles keys: navigation, enter, esc. everything else edits the filter.
func (p PickList) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		p.width, p.height = msg.Width, msg.Height
		return p, nil
	case tea.KeyMsg:
		switch msg.Type { //nolint:exhaustive // the remaining keys edit the filter
		case tea.KeyEsc, tea.KeyCtrlC:
			p.canceled = true
			return p, tea.Quit
		case tea.KeyEnter:
			if len(p.visible) > 0 {
				p.chosen = p.items[p.visible[p.cursor]].ID
				return p, tea.Quit
			}
			return p, nil
		case tea.KeyUp, tea.KeyCtrlP:
			p.move(-1)
			return p, nil
		case tea.KeyDown, tea.KeyCtrlN:
			p.move(1)
			return p, nil
		}
		var cmd tea.Cmd
		p.input, cmd = p.input.Update(msg)
		p.refilter()
		return p, cmd
	}
	var cmd tea.Cmd
	p.input, cmd = p.input.Update(msg)
	return p, cmd
}

func (p *PickList) move(delta int) {
	if len(p.visible) == 0 {
		p.cursor = 0
		return
	}
	p.cursor = (p.cursor + delta + len(p.visible)) % len(p.visible)
}

// refilter recomputes the visible rows for the current filter text and keeps
// the cursor on a valid row.
func (p *PickList) refilter() {
	q := strings.ToLower(strings.TrimSpace(p.input.Value()))
	p.visible = p.visible[:0]
	for i, it := range p.items {
		if q == "" || strings.Contains(strings.ToLower(it.Label+" "+it.Detail), q) {
			p.visible = append(p.visible, i)
		}
	}
	if p.cursor >= len(p.visible) {
		p.cursor = max(0, len(p.visible)-1)
	}
}

// View renders the title, the filter line and the visible rows with a cursor.
func (p PickList) View() string {
	bold := lipgloss.NewStyle().Bold(true)
	muted := lipgloss.NewStyle().Faint(true)
	var b strings.Builder
	b.WriteString(bold.Render(p.title))
	b.WriteString("\n")
	b.WriteString(p.input.View())
	b.WriteString("\n\n")
	rows := max(1, p.height-5)
	start := 0
	if p.cursor >= rows {
		start = p.cursor - rows + 1
	}
	if len(p.visible) == 0 {
		b.WriteString(muted.Render("  no matches"))
	}
	for i := start; i < len(p.visible) && i < start+rows; i++ {
		it := p.items[p.visible[i]]
		marker := "  "
		if i == p.cursor {
			marker = "> "
		}
		line := marker + it.Label
		if it.Detail != "" {
			line += "  " + muted.Render(it.Detail)
		}
		if i == p.cursor {
			line = bold.Render(marker+it.Label) + strings.TrimPrefix(line, marker+it.Label)
		}
		b.WriteString(truncateCells(line, p.width))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(muted.Render(fmt.Sprintf("%d of %d  ·  enter pick  esc cancel  ↑/↓ move", len(p.visible), len(p.items))))
	return b.String()
}

// truncateCells cuts a rendered line to width cells without breaking escape
// sequences: lipgloss measures the visible width.
func truncateCells(s string, width int) string {
	if width <= 0 || lipgloss.Width(s) <= width {
		return s
	}
	return lipgloss.NewStyle().MaxWidth(width).Render(s)
}
