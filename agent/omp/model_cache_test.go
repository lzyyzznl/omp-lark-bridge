package omp

import (
	"context"
	"testing"
	"time"

	"github.com/lzyyzznl/omp-lark-bridge/core"
)

// A 3s card deadline that loses the race against a cold omp spawn must not
// replace the cached model list with the single-model fallback — that turned
// one slow discovery into a minute of one-option dropdowns.
func TestAvailableModelsDoesNotCacheFallback(t *testing.T) {
	a := &Agent{model: "fallback/model"}
	stale := []core.ModelOption{{Name: "p/a"}, {Name: "p/b"}}
	a.modelsCache = stale
	a.modelsCachedAt = time.Now().Add(-2 * modelCacheTTL)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // discovery fails instantly, like an expired card deadline

	got := a.AvailableModels(ctx)
	if len(got) != 2 || got[0].Name != "p/a" {
		t.Fatalf("AvailableModels = %v, want the stale cached list", got)
	}
	if len(a.modelsCache) != 2 {
		t.Fatalf("cache overwritten by fallback: %v", a.modelsCache)
	}
}

// Warm cache must skip discovery entirely, otherwise every /model open pays
// the omp spawn again.
func TestAvailableModelsFreshCacheSkipsDiscovery(t *testing.T) {
	a := &Agent{model: "fallback/model"}
	a.modelsCache = []core.ModelOption{{Name: "p/fresh"}}
	a.modelsCachedAt = time.Now()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	got := a.AvailableModels(ctx)
	if len(got) != 1 || got[0].Name != "p/fresh" {
		t.Fatalf("AvailableModels = %v, want the fresh cached list", got)
	}
}
