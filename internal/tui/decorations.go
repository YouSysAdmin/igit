package tui

import "github.com/yousysadmin/igit/internal/git"

// decorations is the host's side of the render boundary. Everything the pane
// draws on top of a plain diff comes through here: the rows attached under a
// line, and the per-row gutter marks. The pane builds one snapshot per render
// and then draws from it alone, so the render path itself never reads the
// annotation store, the stage plan or the host's selection.
//
// The other half of the boundary is the painters in annotrender.go, which the
// pane calls to draw what a set holds. Together they are what a package split
// has to turn into an interface.
//
// decorations are the extra rows a host attaches under the diff rows the pane
// draws. Review mode fills them from the annotation store and the pull-request
// comments it loaded. Commit mode leaves them empty: its diffs carry staged or
// historical line numbers, which review annotations do not address.
//
// The pane builds them once per render and reads nothing else about where they
// came from, so hiding them is a property of the set rather than a flag every
// render path has to remember.
type decorations struct {
	comments    map[annotLineKey]string // annotation body, by line and change type
	fileComment string                  // file-level annotation body, empty when there is none or its body is empty
	hasFile     bool                    // a file-level annotation exists, even with an empty body
	remotes     map[string][]RemoteNote // remote request comments, by annotation key
	editor      decorEditor             // the live editor, the part no cache key can compare

	// marks are the per-row gutter state the host owns: the rows it has
	// selected and the ones it has marked for the commit. Both are bound to the
	// host as it stood when the set was built, so a render reads one snapshot.
	selected func(idx int) bool
	staged   func(idx int) bool
}

// isSelected reports whether the host has selected the row.
func (d decorations) isSelected(idx int) bool { return d.selected != nil && d.selected(idx) }

// isStaged reports whether the host has marked the row for the commit.
func (d decorations) isStaged(idx int) bool { return d.staged != nil && d.staged(idx) }

// decorEditor is the state of the annotation being typed. It decides which row
// is uncacheable and where the cursor sits, so the pane needs no other view of
// the editing session.
type decorEditor struct {
	active   bool // an annotation is being typed
	onFile   bool // the one being typed is the file-level annotation
	onCursor bool // the diff cursor sits on an annotation row rather than on its line
}

// editsRow reports whether the live editor occupies the row, which therefore
// cannot be cached: the widget carries its own value and cursor position.
func (d decorations) editsRow(idx, cursor int) bool {
	return d.editor.active && !d.editor.onFile && idx == cursor
}

// empty reports whether there is nothing to draw under any line.
func (d decorations) empty() bool { return len(d.comments) == 0 && len(d.remotes) == 0 }

// decorations collects what the loaded file has attached to it. A host that
// hides annotations gets an empty set, so no caller downstream re-checks the flag.
func (m Model) decorations() decorations {
	ed := decorEditor{active: m.annot.annotating, onFile: m.annot.fileAnnotating, onCursor: m.nav.onAnnotationRow}
	// the method values below bind this copy of m, so one render sees one
	// consistent snapshot of the host
	d := decorations{
		comments: map[annotLineKey]string{}, editor: ed,
		selected: m.isSelectedLine, staged: m.stageMarked,
	}
	if m.host.annotationsHidden {
		return d
	}
	all := m.store.Get(m.file.name)
	d.comments = make(map[annotLineKey]string, len(all))
	d.remotes = m.remoteByKey
	for _, a := range all {
		if a.Line == 0 {
			d.fileComment = a.Comment
			d.hasFile = true
			continue
		}
		d.comments[annotLineKey{line: a.Line, changeType: git.ChangeType(a.Type)}] = a.Comment
	}
	return d
}

// forLine returns what is attached to one diff line: the annotation body when
// there is one, and the remote comments on the same line.
func (m Model) decorationsFor(d decorations, dl git.DiffLine) (comment string, has bool, remotes []RemoteNote) {
	if dl.ChangeType == git.ChangeDivider {
		return "", false, nil
	}
	num := m.diffLineNum(dl)
	comment, has = d.comments[annotLineKey{line: num, changeType: dl.ChangeType}]
	return comment, has, d.remotes[m.annotationKey(num, string(dl.ChangeType))]
}
