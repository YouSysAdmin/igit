// Package tui implements the bubbletea UI of igit: the review mode with
// inline annotations and the commit mode with staging.
//
// [App] is the root model. It owns the mode switch and delegates to the
// review [Model] (a value, methods split across model.go, view.go, diffview.go,
// diffnav.go, annotate.go and their neighbors) and to the CommitModel
// (commit*.go). Review state is grouped into sub-structs by concern (m.cfg,
// m.layout, m.file, m.modes, m.nav, m.search, m.annot, m.stage, m.pr), but the
// methods stay on Model.
//
// Sub-packages own the parts that stand on their own: [style] for colors and
// ANSI rendering, [sidepane] for the file tree and the generic list, [overlay]
// for popups (one at a time, coordinated by a Manager), [worddiff] for
// intra-line diffing and highlight insertion.
//
// Everything outside the UI is reached through consumer-side interfaces
// declared in model.go and repo.go: [DiffSource], [SyntaxHighlighter], [Blamer],
// [ThemeCatalog], [ExternalEditor], [Repo], [PRReviewer]. The concrete
// implementations (internal/vcs, internal/gitops, internal/theme,
// internal/extcmd, internal/forge) are wired in cmd/igit.
package tui
