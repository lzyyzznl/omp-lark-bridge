// omp_agents.go exposes the omp agent catalog (bundled + user + project scope)
// and CRUD over the custom-agent markdown files (`~/.omp/agent/agents/*.md`
// and `<project workdir>/.omp/agents/*.md`) to the management API.
//
// Bundled agents ship inside the omp binary, so their definitions are fetched
// with `omp agents unpack --dir` (the CLI's own export command) into a cache
// directory that lives next to the user store (`~/.omp/agent/omp-lark-bridge-bundled`).
// Custom agents are plain Markdown with YAML frontmatter — omp's documented
// format — written here and read by omp at its next session start.
package core

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// OmpAgentInfo is the summary the web UI renders for one omp agent.
type OmpAgentInfo struct {
	Name          string `json:"name"`
	Description   string `json:"description"`
	Scope         string `json:"scope"`                    // "bundled" | "user" | "project"
	Model         string `json:"model"`                    // "" when the agent has no frontmatter model
	Tools         string `json:"tools"`                    // "" when unrestricted
	ThinkingLevel string `json:"thinking_level,omitempty"` // "" = inherit
	Spawns        string `json:"spawns,omitempty"`         // "" = unrestricted; "*" = any subagent
	Source        string `json:"source"`                   // display origin: "omp builtin" / file path
	ReadOnly      bool   `json:"read_only"`
	// Project is the project's display name for project-scope agents ("" else).
	Project string `json:"project,omitempty"`

	path    string // absolute file path for user/project agents ("" for bundled)
	content string // full markdown when the item was produced by a detail read
}

// IsEditableAgentScope reports whether the scope may be written/deleted through
// the management API. Only user scope is writable: bundled agents are read-only
// (they live inside the omp binary) and project scope is intentionally read-only
// here to keep the UI's lifecycle rules simple.
func IsEditableAgentScope(scope string) bool { return scope == "user" }

// agentFrontmatterFields is the durable subset of omp's frontmatter that the UI
// edits directly. Everything else (prewalk, advisor, enabled…) stays untouched
// because edits rewrite the whole file.
const agentFrontmatterFields = "name, description, model, tools, thinkingLevel, spawns"

// agentDiscoveryCache caches the bundled-agent directory across calls.
var agentDiscovery = struct {
	sync.Mutex
	cachedBundledPath string
}{}

// userAgentDir returns the user-scope agents directory, creating it.
func userAgentDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("omp agents: home dir: %w", err)
	}
	dir := filepath.Join(home, ".omp", "agent", "agents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("omp agents: mkdir %s: %w", dir, err)
	}
	return dir, nil
}

// bundledAgentDir returns the cache dir where `omp agents unpack` materializes
// the bundled definitions. It refreshes the cache on the first call after the
// process starts so omp upgrades are picked up on daemon restart. Falls back to
// an empty dir when the omp binary is missing so the listing stays usable.
func bundledAgentDir() string {
	agentDiscovery.Lock()
	defer agentDiscovery.Unlock()
	if agentDiscovery.cachedBundledPath != "" {
		return agentDiscovery.cachedBundledPath
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	dir := filepath.Join(home, ".omp", "agent", "omp-lark-bridge-bundled")
	if err := os.RemoveAll(dir); err != nil {
		return dir
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return dir
	}
	if _, err := exec.LookPath("omp"); err != nil {
		return dir // no binary: leave empty, listing shows nothing for bundled
	}
	cmd := exec.Command("omp", "agents", "unpack", "--dir", dir, "--force")
	if out, err := cmd.CombinedOutput(); err != nil {
		// Keep the dir empty rather than returning stale definitions.
		_ = os.RemoveAll(dir)
		_ = os.MkdirAll(dir, 0o755)
		slog.Warn("omp agents: unpack bundled failed", "error", err, "output", strings.TrimSpace(string(out)))
	}
	agentDiscovery.cachedBundledPath = dir
	return dir
}

// discoverOmpAgents returns every omp agent visible to this machine across the
// bundled / user / project scopes. projectWorkdirs maps a project display name
// to its workdir (from the registered engines); entries whose agents are
// unavailable report no project agents.
func DiscoverOmpAgents(projectWorkdirs map[string]string) []*OmpAgentInfo {
	seen := map[string]*OmpAgentInfo{} // name → strongest definition

	add := func(a *OmpAgentInfo) {
		// Precedence: a custom definition wins over bundled; project wins over
		// user (matches omp's own resolution order).
		if existing, ok := seen[a.Name]; ok {
			if scopeRank(a.Scope) <= scopeRank(existing.Scope) {
				seen[a.Name] = a
			}
			return
		}
		seen[a.Name] = a
	}

	// Bundled.
	if dir := bundledAgentDir(); dir != "" {
		for _, a := range readAgentDir(dir, "bundled", "omp builtin", "") {
			a.ReadOnly = true
			add(a)
		}
	}

	// User.
	if dir, err := userAgentDir(); err == nil {
		for _, a := range readAgentDir(dir, "user", "", "") {
			add(a)
		}
	}

	// Project (one dir per registered project).
	names := make([]string, 0, len(projectWorkdirs))
	for name := range projectWorkdirs {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		wd := projectWorkdirs[name]
		if wd == "" {
			continue
		}
		dir := filepath.Join(wd, ".omp", "agents")
		for _, a := range readAgentDir(dir, "project", "", name) {
			a.ReadOnly = true
			a.Project = name
			add(a)
		}
	}

	out := make([]*OmpAgentInfo, 0, len(seen))
	for _, a := range seen {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// scopeRank orders scopes so project (custom & project-local) dominates user,
// which dominates bundled — omp's documented precedence.
func scopeRank(scope string) int {
	switch scope {
	case "project":
		return 0
	case "user":
		return 1
	default:
		return 2
	}
}

// readAgentDir lists *.md files in dir as agent summaries. project is the
// project display name ("" for non-project scopes); source overrides the
// default display source (used for bundled).
func readAgentDir(dir, scope, source, project string) []*OmpAgentInfo {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []*OmpAgentInfo
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		meta, _ := ParseAgentMarkdown(string(data))
		name := meta["name"]
		if name == "" {
			name = strings.TrimSuffix(e.Name(), ".md")
		}
		info := &OmpAgentInfo{
			Name:          name,
			Description:   meta["description"],
			Model:         meta["model"],
			Tools:         meta["tools"],
			ThinkingLevel: meta["thinkingLevel"],
			Spawns:        meta["spawns"],
			Scope:         scope,
			Source:        source,
			Project:       project,
			path:          path,
		}
		if info.Source == "" {
			info.Source = path
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// parseAgentMarkdown splits omp agent markdown into its YAML frontmatter map
// and the instruction body. Both may be empty for a malformed file: callers
// fall back to the file name for the agent name.
func ParseAgentMarkdown(content string) (map[string]string, string) {
	meta := map[string]string{}
	body := strings.TrimSpace(content)
	if !strings.HasPrefix(strings.TrimSpace(content), "---") {
		return meta, body
	}
	rest := strings.TrimPrefix(content, "---")
	rest = strings.TrimPrefix(rest, "\n")
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return meta, body
	}
	fm := rest[:end]
	body = strings.TrimSpace(rest[end+len("\n---"):])
	lines := strings.Split(fm, "\n")
	for i, line := range lines {
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.Trim(strings.TrimSpace(val), `"'`)
		if key == "" {
			continue
		}
		// A bare `spawns:` key carries a YAML list on the following lines
		// (`spawns:\n  - scout`); flatten it to a comma-separated string so the
		// UI can round-trip it.
		if key == "spawns" && val == "" {
			var items []string
			for j := i + 1; j < len(lines); j++ {
				item, isItem := strings.CutPrefix(strings.TrimSpace(lines[j]), "- ")
				if !isItem {
					break
				}
				if it := strings.TrimSpace(item); it != "" {
					items = append(items, it)
				}
			}
			meta[key] = strings.Join(items, ",")
			continue
		}
		meta[key] = val
	}
	return meta, body
}

// renderAgentMarkdown builds the user-editable markdown file from the frontmatter
// fields the UI manages plus the instruction body.
func RenderAgentMarkdown(name, description, model, tools, thinkingLevel, spawns, body string) string {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("name: " + name + "\n")
	if description != "" {
		b.WriteString("description: " + description + "\n")
	}
	if model != "" {
		b.WriteString("model: " + model + "\n")
	}
	if tools != "" {
		b.WriteString("tools: " + tools + "\n")
	}
	if thinkingLevel != "" {
		b.WriteString("thinkingLevel: " + thinkingLevel + "\n")
	}
	if s := renderSpawns(spawns); s != "" {
		b.WriteString(s + "\n")
	}
	b.WriteString("---\n\n")
	b.WriteString(strings.TrimSpace(body))
	b.WriteString("\n")
	return b.String()
}

// renderSpawns serializes a comma-separated spawns list back to YAML: a single
// token ("*" or one agent) stays inline, multiple agents become a list.
func renderSpawns(spawns string) string {
	spawns = strings.TrimSpace(spawns)
	if spawns == "" {
		return ""
	}
	var items []string
	for _, it := range strings.Split(spawns, ",") {
		if it = strings.TrimSpace(it); it != "" {
			items = append(items, it)
		}
	}
	switch len(items) {
	case 0:
		return ""
	case 1:
		if items[0] == "*" {
			return `spawns: "*"`
		}
		return "spawns: " + items[0]
	}
	var b strings.Builder
	b.WriteString("spawns:\n")
	for _, it := range items {
		b.WriteString("  - " + it + "\n")
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// findOmpAgent locates a single agent by name across scopes. It returns the
// strongest definition (bundled if no custom shadows it), or nil.
func (m *ManagementServer) findOmpAgent(projectWorkdirs map[string]string, name string) *OmpAgentInfo {
	if name == "" || !ValidAgentName(name) {
		return nil
	}
	var best *OmpAgentInfo
	for _, a := range DiscoverOmpAgents(projectWorkdirs) {
		if a.Name != name {
			continue
		}
		if best == nil || scopeRank(a.Scope) < scopeRank(best.Scope) {
			best = a
		}
	}
	return best
}

// projectWorkdirs snapshots the registered engines' agent workdirs by project.
// A nil entry for an engine whose agent has no GetWorkDir is skipped.
func (m *ManagementServer) projectWorkdirs() map[string]string {
	out := map[string]string{}
	m.mu.RLock()
	defer m.mu.RUnlock()
	for name, e := range m.engines {
		if e == nil || e.agent == nil {
			continue
		}
		if w, ok := e.agent.(interface{ GetWorkDir() string }); ok {
			out[name] = w.GetWorkDir()
		}
	}
	return out
}

// createUserAgent writes a new custom agent into the user scope
// (`~/.omp/agent/agents/<name>.md`) and refuses to overwrite an existing file.
func CreateUserAgent(name, description, model, tools, thinkingLevel, spawns, body string) (*OmpAgentInfo, error) {
	if !ValidAgentName(name) {
		return nil, fmt.Errorf("invalid agent name %q (want [A-Za-z0-9_-]{1,64})", name)
	}
	dir, err := userAgentDir()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, name+".md")
	if _, err := os.Stat(path); err == nil {
		return nil, fmt.Errorf("an agent named %q already exists (user scope); edit it instead", name)
	}
	content := RenderAgentMarkdown(name, description, model, tools, thinkingLevel, spawns, body)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return nil, fmt.Errorf("write agent %s: %w", path, err)
	}
	info := &OmpAgentInfo{
		Name: name, Description: description, Model: model, Tools: tools,
		ThinkingLevel: thinkingLevel, Spawns: spawns,
		Scope: "user", Source: path, path: path, content: content,
	}
	return info, nil
}

// updateUserAgent replaces the user-scope file for name. The name itself never
// changes through an update (rename = delete + create).
func UpdateUserAgent(name, description, model, tools, thinkingLevel, spawns, body string) (*OmpAgentInfo, error) {
	if !ValidAgentName(name) {
		return nil, fmt.Errorf("invalid agent name %q (want [A-Za-z0-9_-]{1,64})", name)
	}
	dir, err := userAgentDir()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, name+".md")
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("agent %q not found in user scope", name)
	}
	content := RenderAgentMarkdown(name, description, model, tools, thinkingLevel, spawns, body)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return nil, fmt.Errorf("write agent %s: %w", path, err)
	}
	info := &OmpAgentInfo{
		Name: name, Description: description, Model: model, Tools: tools,
		ThinkingLevel: thinkingLevel, Spawns: spawns,
		Scope: "user", Source: path, path: path, content: content,
	}
	return info, nil
}

// deleteUserAgent removes the user-scope definition for name.
func DeleteUserAgent(name string) error {
	dir, err := userAgentDir()
	if err != nil {
		return err
	}
	path := filepath.Join(dir, name+".md")
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("agent %q not found in user scope", name)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove agent %s: %w", path, err)
	}
	return nil
}

// agentFileContent returns the full markdown source of an agent detail. For
// cached bundled items it re-reads the cache file; for custom agents it reads
// from disk so frontmatter the UI does not edit (prewalk, advisor…) is shown.
func ReadAgentDetail(info *OmpAgentInfo) (string, error) {
	if info.content != "" {
		return info.content, nil
	}
	if info.path == "" {
		return "", fmt.Errorf("agent %q has no readable source", info.Name)
	}
	data, err := os.ReadFile(info.path)
	if err != nil {
		return "", fmt.Errorf("read agent %s: %w", info.path, err)
	}
	return string(data), nil
}
