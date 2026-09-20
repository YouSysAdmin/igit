// Package git is the read side of igit's git layer: the file lists and unified
// diffs both modes render, blame, the commit log behind the info popup, the
// review-info stats, and the path filters (--include, --exclude, --only) that
// scope a session. Standalone reviews with no repository are served by the
// same interfaces through FileReader.
//
// The write side (status, staging, commits, branches, stashes, sync) lives in
// internal/gitops, which builds on the types declared here.
package git
