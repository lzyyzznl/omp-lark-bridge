package omp

import (
	"path/filepath"
	"testing"
)

func TestSanitizeSessionName(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", "default"},
		{"s1", "s1"},
		{"a/b/../c", "a-b----c"},
		{"my session:1", "my-session-1"},
		{"..", "--"},
		{"对话session-1", "--session-1"},
	}
	for _, c := range cases {
		if got := sanitizeSessionName(c.in); got != c.want {
			t.Errorf("sanitizeSessionName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	// A sanitized name must never escape the session root.
	root := sanitizeSessionName("a/../../etc/passwd")
	if filepath.Base(root) != root {
		t.Errorf("sanitized name escapes its directory: %q", root)
	}
}

func TestSanitizeSessionName_StableAndUnique(t *testing.T) {
	a := sanitizeSessionName("feishu:u1:s1")
	b := sanitizeSessionName("feishu:u1:s1")
	if a != b {
		t.Errorf("sanitization not stable: %q vs %q", a, b)
	}
	if sanitizeSessionName("s1") == sanitizeSessionName("s2") {
		t.Error("distinct sessions must not collide")
	}
}

func TestSplitModel(t *testing.T) {
	cases := []struct {
		in          string
		wantProvider string
		wantID      string
		wantOK      bool
	}{
		{"router", "", "", false},
		{"", "", "", false},
		{"/id", "", "", false},
		{"p/", "", "", false},
		{"anthropic/claude-sonnet-4", "anthropic", "claude-sonnet-4", true},
		{"router/deepseek-v3", "router", "deepseek-v3", true},
	}
	for _, c := range cases {
		prov, id, ok := splitModel(c.in)
		if ok != c.wantOK || prov != c.wantProvider || id != c.wantID {
			t.Errorf("splitModel(%q) = (%q, %q, %v), want (%q, %q, %v)",
				c.in, prov, id, ok, c.wantProvider, c.wantID, c.wantOK)
		}
	}
}

func TestExtForMime(t *testing.T) {
	cases := map[string]string{
		"image/png":   ".png",
		"image/jpeg":  ".jpg",
		"image/jpg":   ".jpg",
		"image/gif":   ".gif",
		"image/webp":  ".webp",
		"application/octet-stream": ".img",
		"": ".img",
	}
	for mime, want := range cases {
		if got := extForMime(mime); got != want {
			t.Errorf("extForMime(%q) = %q, want %q", mime, got, want)
		}
	}
}
