package main

import (
	"testing"

	"github.com/lzyyzznl/omp-lark-bridge/config"
)

func TestOmpwebPortDefault(t *testing.T) {
	if got := ompwebPort(config.OmpWebConfig{}); got != config.DefaultOmpWebPort {
		t.Fatalf("default port = %d, want %d", got, config.DefaultOmpWebPort)
	}
	if got := ompwebPort(config.OmpWebConfig{Port: 40000}); got != 40000 {
		t.Fatalf("override port = %d, want 40000", got)
	}
}

func TestOmpwebURL(t *testing.T) {
	if got, want := ompwebURL(config.OmpWebConfig{Port: 30177}), "http://127.0.0.1:30177"; got != want {
		t.Fatalf("url = %q, want %q", got, want)
	}
}

func TestResolveOmpwebBinExplicit(t *testing.T) {
	bin, npx := resolveOmpwebBin(config.OmpWebConfig{Bin: "/opt/ompweb"})
	if bin != "/opt/ompweb" || npx {
		t.Fatalf("explicit bin = (%q,%v), want (/opt/ompweb,false)", bin, npx)
	}
}
