package app

import "embed"

// appAssets contains static browser assets embedded into the single Go binary.
//
//go:embed assets/app.css
var appAssets embed.FS
