package main

import (
	"github.com/yousysadmin/igit/internal/tui"
)

// reviewInfoInputs bundles the runtime values supplied alongside the parsed
// CLI options when constructing a ReviewInfoConfig. The fields are all
// produced post-parseArgs by the diff-source setup. Collecting them in a
// single struct keeps reviewInfoFromOptions's signature stable as the popup
// gains more inputs and removes the same-typed-string swap risk of the prior
// positional-args shape.
type reviewInfoInputs struct {
	workDir  string
	isGit    bool   // a repository was found, so the ref and staged scopes mean something
	label    string // header override, e.g. the pull request being reviewed
	baseline string // what an incremental request review starts from, empty otherwise
}

// reviewInfoFromOptions builds the production *ReviewInfoConfig threaded into
// ModelConfig.ReviewInfo. Production always returns a non-nil config so the
// review-info subsystem activates. focused tests pass nil directly to
// ModelConfig and bypass this constructor entirely (nil = subsystem off).
func reviewInfoFromOptions(opts options, in reviewInfoInputs) *tui.ReviewInfoConfig {
	ref := opts.ref()
	effectiveStaged := opts.Review.Staged && in.isGit
	return &tui.ReviewInfoConfig{
		Label:          in.label,
		Standalone:     !in.isGit,
		WorkDir:        in.workDir,
		Ref:            ref,
		Baseline:       in.baseline,
		Staged:         effectiveStaged,
		Only:           append([]string(nil), opts.Review.Only...),
		Include:        append([]string(nil), opts.Include...),
		Exclude:        append([]string(nil), opts.Exclude...),
		Compact:        opts.Review.Compact,
		CompactContext: opts.Review.CompactContext,
	}
}
