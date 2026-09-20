package theme

import "embed"

// galleryFS holds the bundled theme files shipped with the binary.
//
//go:embed gallery/*
var galleryFS embed.FS
