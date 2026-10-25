package core

import (
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// handleFSDirs lists immediate subdirectories of a server path, used by the
// web UI's work_dir picker (browsing the filesystem instead of typing a path).
// The management API is token-protected and serves a local operator.
func (m *ManagementServer) handleFSDirs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		mgmtError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	p := strings.TrimSpace(r.URL.Query().Get("path"))
	if p == "" {
		p = "/"
	} else if !strings.HasPrefix(p, "/") {
		// Relative path: resolve against the operator's home directory.
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, p)
		}
	}
	info, err := os.Stat(p)
	if err != nil || !info.IsDir() {
		mgmtError(w, http.StatusBadRequest, "not a directory: "+p)
		return
	}
	entries, err := os.ReadDir(p)
	if err != nil {
		mgmtError(w, http.StatusInternalServerError, "read dir: "+err.Error())
		return
	}
	dirs := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dirs = append(dirs, map[string]any{
			"name": e.Name(),
			"path": filepath.Join(p, e.Name()),
		})
	}
	sort.Slice(dirs, func(i, j int) bool {
		return dirs[i]["name"].(string) < dirs[j]["name"].(string)
	})
	mgmtJSON(w, http.StatusOK, map[string]any{
		"path":   p,
		"parent": filepath.Dir(p),
		"dirs":   dirs,
	})
}
