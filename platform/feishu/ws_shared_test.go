package feishu

import (
	"testing"
)

func TestSharedWSGroup_RegisterAndAllPlatforms(t *testing.T) {
	// Clean up global state for test isolation.
	cleanup := func() {
		sharedWSMu.Lock()
		defer sharedWSMu.Unlock()
		for k := range sharedWSGroups {
			delete(sharedWSGroups, k)
		}
	}
	cleanup()
	defer cleanup()

	p1 := &Platform{appID: "cli_test", domain: "feishu.cn"}
	p2 := &Platform{appID: "cli_test", domain: "feishu.cn"}

	// Register first platform — should be primary.
	g1, isPrimary1 := registerSharedWS(p1)
	if !isPrimary1 {
		t.Fatal("first platform should be primary")
	}
	if len(g1.allPlatforms()) != 1 {
		t.Fatalf("expected 1 platform, got %d", len(g1.allPlatforms()))
	}

	// Register second platform — should be secondary, same group.
	g2, isPrimary2 := registerSharedWS(p2)
	if isPrimary2 {
		t.Fatal("second platform should not be primary")
	}
	if g1 != g2 {
		t.Fatal("both platforms should share the same group")
	}
	if len(g1.allPlatforms()) != 2 {
		t.Fatalf("expected 2 platforms, got %d", len(g1.allPlatforms()))
	}
}

func TestSharedWSGroup_Unregister(t *testing.T) {
	cleanup := func() {
		sharedWSMu.Lock()
		defer sharedWSMu.Unlock()
		for k := range sharedWSGroups {
			delete(sharedWSGroups, k)
		}
	}
	cleanup()
	defer cleanup()

	p1 := &Platform{appID: "cli_test", domain: "feishu.cn"}
	p2 := &Platform{appID: "cli_test", domain: "feishu.cn"}

	g, _ := registerSharedWS(p1)
	registerSharedWS(p2)

	// Unregister first — one remains.
	remaining := unregisterSharedWS(p1)
	if remaining != 1 {
		t.Fatalf("expected 1 remaining, got %d", remaining)
	}
	platforms := g.allPlatforms()
	if len(platforms) != 1 || platforms[0] != p2 {
		t.Fatal("expected only p2 to remain")
	}

	// Unregister last — group deleted.
	remaining = unregisterSharedWS(p2)
	if remaining != 0 {
		t.Fatalf("expected 0 remaining, got %d", remaining)
	}
	sharedWSMu.Lock()
	_, exists := sharedWSGroups[sharedWSKey("cli_test", "feishu.cn")]
	sharedWSMu.Unlock()
	if exists {
		t.Fatal("group should be deleted when empty")
	}
}

func TestSharedWSGroup_DifferentAppIDs(t *testing.T) {
	cleanup := func() {
		sharedWSMu.Lock()
		defer sharedWSMu.Unlock()
		for k := range sharedWSGroups {
			delete(sharedWSGroups, k)
		}
	}
	cleanup()
	defer cleanup()

	p1 := &Platform{appID: "cli_aaa", domain: "feishu.cn"}
	p2 := &Platform{appID: "cli_bbb", domain: "feishu.cn"}

	g1, isPrimary1 := registerSharedWS(p1)
	g2, isPrimary2 := registerSharedWS(p2)

	if !isPrimary1 || !isPrimary2 {
		t.Fatal("different app_ids should each be primary")
	}
	if g1 == g2 {
		t.Fatal("different app_ids should have separate groups")
	}

	unregisterSharedWS(p1)
	unregisterSharedWS(p2)
}

// TestSharedWSGroup_PrimaryFailover verifies that when the primary exits the
// first remaining platform takes over as primary (so a fresh WebSocket can be
// started), and that platforms registered afterwards are still secondary.
func TestSharedWSGroup_PrimaryFailover(t *testing.T) {
	cleanup := func() {
		sharedWSMu.Lock()
		defer sharedWSMu.Unlock()
		for k := range sharedWSGroups {
			delete(sharedWSGroups, k)
		}
	}
	cleanup()
	defer cleanup()

	p1 := &Platform{appID: "cli_failover", domain: "feishu.cn"}
	p2 := &Platform{appID: "cli_failover", domain: "feishu.cn"}
	p3 := &Platform{appID: "cli_failover", domain: "feishu.cn"}

	g, isPrimary1 := registerSharedWS(p1)
	if !isPrimary1 {
		t.Fatal("first platform should be primary")
	}
	if _, isPrimary2 := registerSharedWS(p2); isPrimary2 {
		t.Fatal("second platform should not be primary")
	}
	if _, isPrimary3 := registerSharedWS(p3); isPrimary3 {
		t.Fatal("third platform should not be primary")
	}

	// Primary exits with members remaining -> first remaining platform is the
	// new primary, ready for promoteSharedWSPrimary to start its connection.
	if remaining := unregisterSharedWS(p1); remaining != 2 {
		t.Fatalf("remaining = %d, want 2", remaining)
	}
	g.mu.RLock()
	np := g.primary
	g.mu.RUnlock()
	if np != p2 {
		t.Fatalf("new primary = %v, want p2", np)
	}

	// A platform registering after failover must be secondary, not primary
	// (the previous deadlock where the group silently lost its event source).
	late := &Platform{appID: "cli_failover", domain: "feishu.cn"}
	if _, isPrimaryLate := registerSharedWS(late); isPrimaryLate {
		t.Fatal("late-registering platform should not become primary")
	}

	unregisterSharedWS(p2)
	unregisterSharedWS(p3)
	unregisterSharedWS(late)
}
