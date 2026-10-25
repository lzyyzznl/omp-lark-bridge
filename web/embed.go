// Package web embeds the omp-lark-bridge admin UI (built with Vite) into the
// binary so http://127.0.0.1:<management port>/ serves the dashboard directly —
// no separate static server or Node process needed.
//
// The embedded assets live under dist/, produced by `npm run build`. dist/ is
// gitignored (only a .keep placeholder is committed), so a fresh clone builds
// go cleanly even without the frontend: the go:embed directive matches .keep
// and `npm run build` (via Makefile) fills in the real assets before shipping.
package web

import (
	"embed"
	"io/fs"

	"github.com/lzyyzznl/omp-lark-bridge/core"
)

//go:embed all:dist
var assets embed.FS

func init() {
	sub, err := fs.Sub(assets, "dist")
	if err != nil {
		return // cannot happen with the go:embed above
	}
	core.RegisterWebAssets(sub)
}
