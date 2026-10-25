package main

// Blank import activates web/embed.go's init(), which registers the embedded
// admin UI into the management server (core.RegisterWebAssets). Without it the
// frontend would not be available at http://127.0.0.1:<management port>/.
import _ "github.com/lzyyzznl/omp-lark-bridge/web"
