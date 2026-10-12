// Package web embeds the same-origin delivery UI in the application binary.
package web

import (
	"embed"
	"net/http"
)

//go:embed index.html app.js styles.css
var assets embed.FS

// Handler serves bundled assets without requiring a working-directory layout.
func Handler() http.Handler { return http.FileServer(http.FS(assets)) }
