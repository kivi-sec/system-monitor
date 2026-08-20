// Package web embeds the static frontend (HTML/CSS/JS) directly into the
// compiled binary via go:embed, so the whole dashboard ships and runs as
// a single self-contained executable with no external files required.
package web

import "embed"

//go:embed index.html app.js styles.css
var Assets embed.FS
