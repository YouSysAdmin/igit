package main

import (
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/yousysadmin/igit/internal/fsutil"
	"github.com/yousysadmin/igit/internal/session"
)

type finalizeReq struct {
	opts          options
	annotations   string
	files         []string
	discarded     bool
	gitRoot       string
	workDir       string
	signaled      bool
	sessionOutput string // path chosen in-session via save-as, wins over every flag
	repoLabel     string // session label for output-dir file naming
	prNumber      int    // pull or merge request under review, 0 otherwise, recorded in the history entry
	prHead        string // head commit of that request
	prKind        string // history scope prefix of that request, "pr" or "mr"
	restored      bool   // session continues a saved history entry
	prPosted      bool   // the annotations were posted as a review, they live on the request now
	prNote        string // printed on stderr after the output: the posted pull-request review, if any
	stdout        io.Writer
	stderr        io.Writer
	now           func() time.Time // nil = time.Now
}

// finalize persists the review after p.Run() joins. A discarded review writes
// nothing. A session that ends without annotations writes nothing either,
// unless it continued a saved entry: then the history save clears that entry,
// so deleting every annotation is not forgotten. a signal-driven exit
// (r.signaled) stops after the history, never reaching the output handoff,
// while a graceful exit also writes the annotation output.
func finalize(r finalizeReq) error {
	if r.discarded {
		return nil
	}
	if r.annotations == "" && !r.restored {
		return nil
	}
	// a posted review lives on the request now and comes back as a remote
	// comment. keeping the local copy would show it twice next session and
	// post it again with the next verdict, so the entry is cleared instead
	histAnnotations := r.annotations
	if r.prPosted {
		histAnnotations = ""
	}
	saveHistory(histReq{opts: r.opts, annotations: histAnnotations, gitRoot: r.gitRoot, workDir: r.workDir, files: r.files, pr: r.prNumber, prHead: r.prHead, prKind: r.prKind})
	if r.annotations == "" || r.signaled {
		return nil
	}
	if r.prNote != "" && r.stderr != nil {
		defer func() { _, _ = fmt.Fprintln(r.stderr, r.prNote) }()
	}
	return writeAnnotationOutput(annotationOutputReq{
		opts:          r.opts,
		output:        r.annotations,
		sessionOutput: r.sessionOutput,
		repoLabel:     r.repoLabel,
		stdout:        r.stdout,
		stderr:        r.stderr,
		now:           r.now,
	})
}

type annotationOutputReq struct {
	opts          options
	output        string
	sessionOutput string
	repoLabel     string
	stdout        io.Writer
	stderr        io.Writer
	now           func() time.Time
}

// writeAnnotationOutput delivers the final annotation stream. Destination
// precedence: (1) a path chosen in-session with save-as, (2) --output, (3)
// --output-dir, which writes a timestamped file AND prints to stdout so
// pipelines keep working, (4) stdout only.
func writeAnnotationOutput(r annotationOutputReq) error {
	switch {
	case r.sessionOutput != "":
		if err := writeOutputFile(r.sessionOutput, r.output); err != nil {
			return err
		}
		return nil
	case r.opts.Review.Output != "":
		if err := writeOutputFile(r.opts.Review.Output, r.output); err != nil {
			return err
		}
		return nil
	case r.opts.Review.OutputDir != "":
		path := outputDirPath(r.opts.Review.OutputDir, r.repoLabel, r.now)
		if err := writeOutputFile(path, r.output); err != nil {
			return err
		}
		if r.stderr != nil {
			_, _ = fmt.Fprintf(r.stderr, "annotations saved to %s\n", path)
		}
	}
	if _, err := fmt.Fprint(r.stdout, r.output); err != nil {
		return fmt.Errorf("write output: %w", err)
	}
	return nil
}

// writeOutputFile writes content atomically, expanding ~ and creating the
// parent directory so a config-level output directory need not pre-exist.
func writeOutputFile(path, content string) error {
	path = fsutil.ExpandHome(path)
	if err := fsutil.MkdirAllFor(path); err != nil {
		return fmt.Errorf("write output: %w", err)
	}
	if err := fsutil.AtomicWriteFile(path, []byte(content)); err != nil {
		return fmt.Errorf("write output: %w", err)
	}
	return nil
}

// outputPathFuncs builds the two closures the review model uses to name output
// files: defaultFn yields the --output-dir destination ("" when unset) and
// saveAsFn the save-as prompt prefill, which falls back to a timestamped file
// in the working directory.
func outputPathFuncs(opts options, repoLabel, workDir string) (defaultFn, saveAsFn func() string) {
	defaultFn = func() string {
		if opts.Review.OutputDir == "" {
			return ""
		}
		return outputDirPath(opts.Review.OutputDir, repoLabel, nil)
	}
	saveAsFn = func() string {
		if p := defaultFn(); p != "" {
			return p
		}
		return filepath.Join(workDir, session.OutputFileName(repoLabel, time.Now()))
	}
	return defaultFn, saveAsFn
}

// outputDirPath returns the timestamped file inside dir for this session.
func outputDirPath(dir, repoLabel string, now func() time.Time) string {
	if now == nil {
		now = time.Now
	}
	return filepath.Join(fsutil.ExpandHome(dir), session.OutputFileName(repoLabel, now()))
}
