// Package web embeds the static web UI into the daemon binary so
// deployment is a single file with no separate asset directory to copy.
package web

import "embed"

//go:embed index.html login.html app.js login.js style.css
var FS embed.FS
