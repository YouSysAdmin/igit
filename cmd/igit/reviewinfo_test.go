package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestReviewInfoFromOptions(t *testing.T) {
	t.Run("without a repository the review is standalone", func(t *testing.T) {
		info := reviewInfoFromOptions(options{}, reviewInfoInputs{workDir: "/tmp"})
		assert.True(t, info.Standalone)
	})

	t.Run("inside a repository the review is not standalone", func(t *testing.T) {
		info := reviewInfoFromOptions(options{}, reviewInfoInputs{workDir: "/repo", isGit: true})
		assert.False(t, info.Standalone)
	})

	t.Run("staged is ignored without a git repository", func(t *testing.T) {
		info := reviewInfoFromOptions(options{Review: reviewOptions{Staged: true}}, reviewInfoInputs{workDir: "/repo"})
		assert.False(t, info.Staged)
	})

	t.Run("staged is preserved for git", func(t *testing.T) {
		info := reviewInfoFromOptions(options{Review: reviewOptions{Staged: true}}, reviewInfoInputs{workDir: "/repo", isGit: true})
		assert.True(t, info.Staged)
	})

	t.Run("list slices are decoupled from caller", func(t *testing.T) {
		opts := options{Review: reviewOptions{Only: []string{"a", "b"}}}
		info := reviewInfoFromOptions(opts, reviewInfoInputs{})
		// mutating the original must not affect the captured slice
		opts.Review.Only[0] = "x"
		assert.Equal(t, []string{"a", "b"}, info.Only, "Only must be defensively copied")
	})

	t.Run("constructor returns non-nil config", func(t *testing.T) {
		// reviewInfoFromOptions is the production constructor. it must always
		// return a non-nil pointer so the review-info subsystem activates.
		// Focused tests pass nil directly to ModelConfig.ReviewInfo and bypass
		// this constructor entirely. nil is the off-switch.
		info := reviewInfoFromOptions(options{}, reviewInfoInputs{})
		assert.NotNil(t, info)
	})
}
