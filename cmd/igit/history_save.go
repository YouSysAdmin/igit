package main

import (
	"path/filepath"

	"github.com/yousysadmin/igit/internal/session"
)

type histReq struct {
	opts        options
	annotations string
	gitRoot     string
	workDir     string
	files       []string
	pr          int    // pull or merge request number of a request review, 0 otherwise
	prHead      string // head commit of that request
	prKind      string // history scope prefix of that request, "pr" or "mr"
}

// historyParams describes where a session's history lives and what identifies
// it. saveHistory writes with it and --resume lists with it. For non-git
// single-file --only mode the full file path is the header and the parent
// directory basename the history subdirectory.
func historyParams(r histReq) session.Params {
	histPath := r.workDir
	if histPath == "" {
		histPath = r.gitRoot
	}
	var histSubDir string
	if r.gitRoot == "" && len(r.opts.Review.Only) == 1 {
		if abs, err := filepath.Abs(r.opts.Review.Only[0]); err == nil {
			histPath = abs
			histSubDir = filepath.Base(filepath.Dir(abs))
		}
	}
	return session.Params{
		Annotations:    r.annotations,
		Path:           histPath,
		Ref:            r.opts.scopeRef(),
		Staged:         r.opts.Review.Staged,
		GitRoot:        r.gitRoot,
		AnnotatedFiles: r.files,
		SubDir:         histSubDir,
		PR:             r.pr,
		PRKind:         r.prKind,
		Commit:         r.prHead,
	}
}

// historyService returns the history store configured by --history-dir and
// --history-max, or nil when the history is disabled.
func historyService(opts options) *session.Service {
	if opts.Review.HistoryMax <= 0 {
		return nil
	}
	svc := session.New(opts.Review.HistoryDir)
	svc.MaxEntries = opts.Review.HistoryMax
	return svc
}

// saveHistory records the session's annotations under its review scope (a
// session that ends without annotations clears the entry).
func saveHistory(r histReq) {
	if svc := historyService(r.opts); svc != nil {
		svc.Save(historyParams(r))
	}
}
