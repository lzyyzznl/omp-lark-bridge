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

	// Extended fields the UI/CLI now manage. Output/Knowledge hold raw
	// multi-line blocks (YAML schema / markdown); Skills is autoloadSkills CSV.
	Output        string `json:"output,omitempty"`
	Skills        string `json:"skills,omitempty"`
	Prewalk       string `json:"prewalk,omitempty"` // "" | "true" | "false" | model pattern
	Advisor       string `json:"advisor,omitempty"` // "" | "true" | "false" | model pattern
	Blocking      bool   `json:"blocking,omitempty"`
	Structured    bool   `json:"structured,omitempty"`
	ReadSummarize bool   `json:"read_summarize,omitempty"`
	Knowledge     string `json:"knowledge,omitempty"`

	path    string // absolute file path for user/project agents ("" for bundled)
	content string // full markdown when the item was produced by a detail read
}

// CronSkillsApplier is an optional interface agents implement to receive the
// skill allowlist for a one-shot delegated session (web "用专家执行"). The
// value is consumed by the next StartSession, like CronAgentApplier; "" clears
// the pin and means "no allowlist" (the session keeps all skills).
type CronSkillsApplier interface {
	ApplyCronSkills(skillsCSV string)
}

// OmpAgentSkills returns the skill allowlist (autoloadSkills frontmatter) for a
// named agent across the bundled and user scopes; projectWorkdirs extends it to
// project-scope agents. "" means the agent pins no skills — the spawned session
// keeps every discovered skill (matches the editor's "未勾选 = 全部" choice).
func OmpAgentSkills(projectWorkdirs map[string]string, name string) string {
	for _, a := range DiscoverOmpAgents(projectWorkdirs) {
		if a.Name != name || a.Skills == "" {
			continue
		}
		// Normalize to compact CSV: the stored autoloadSkills may keep spaces
		// (rendered for the editor), but omp's --skills flag wants comma-separated
		// globs with no stray whitespace (e.g. "git-*,docker").
		var parts []string
		for _, p := range strings.Split(a.Skills, ",") {
			if p = strings.TrimSpace(p); p != "" {
				parts = append(parts, p)
			}
		}
		return strings.Join(parts, ",")
	}
	return ""
}

// OmpAgentFields is the full editable model of an omp agent persona, written to
// `~/.omp/agent/agents/<name>.md` via RenderAgentMarkdown. `Knowledge` is an
// olb-managed background doc stored in frontmatter (omp ignores unknown keys);
// `Output`/`Skills`/`Prewalk`/`Advisor` map to omp's own frontmatter keys.
type OmpAgentFields struct {
	Name          string // required, [A-Za-z0-9_-]{1,64}
	Description   string
	Model         string // @role or provider/model; comma list for multiple
	Tools         string // comma-separated allowlist; "" = omp defaults
	ThinkingLevel string // off|minimal|low|medium|high|xhigh|max|auto; "" = inherit
	Spawns        string // comma-separated; "*" = any subagent
	Skills        string // comma-separated autoloadSkills
	Output        string // raw nested-YAML schema block
	Prewalk       string // "" | "true" | "false" | model pattern
	Advisor       string // "" | "true" | "false" | model pattern
	Blocking      bool
	Structured    bool
	ReadSummarize bool
	Knowledge     string // background/knowledge doc injected before body
	Body          string // instruction prompt body
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
			Skills:        meta["autoloadSkills"],
			Output:        meta["output"],
			Prewalk:       meta["prewalk"],
			Advisor:       meta["advisor"],
			Blocking:      meta["blocking"] == "true",
			Structured:    meta["structured"] == "true",
			ReadSummarize: meta["readSummarize"] == "true",
			Knowledge:     meta["knowledge"],
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
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		val = strings.TrimSpace(val)
		// A leaf value is `key: value` (maybe quoted). A bare key, or a
		// literal marker (`|`/`|-`), followed by indented lines is either a
		// YAML list (`  - item`) or a nested block (`output:`/`knowledge:`).
		if (val == "" || val == "|" || val == "|-") && i+1 < len(lines) && strings.HasPrefix(lines[i+1], "  ") {
			var listItems []string
			var block []string
			isList := false
			j := i + 1
			for ; j < len(lines); j++ {
				next := lines[j]
				if next == "" {
					continue // tolerate blank lines inside the block
				}
				if !strings.HasPrefix(next, "  ") {
					break
				}
				t := strings.TrimSpace(next)
				if strings.HasPrefix(t, "- ") {
					isList = true
					if it := strings.TrimSpace(strings.TrimPrefix(t, "- ")); it != "" {
						listItems = append(listItems, it)
					}
					continue
				}
				if isList {
					break // first non-item line ends the list
				}
				// Nested YAML object or literal block: keep every line minus
				// the shared 2-space indent so relative structure survives.
				block = append(block, strings.TrimPrefix(next, "  "))
			}
			if isList {
				meta[key] = strings.Join(listItems, ",")
			} else if len(block) > 0 {
				meta[key] = strings.TrimSpace(strings.Join(block, "\n"))
			}
			i = j - 1
			continue
		}
		// Plain leaf value; strip surrounding quotes (omp's own dumps quote
		// descriptions that contain colons).
		val = strings.Trim(strings.TrimSpace(val), `"'`)
		meta[key] = val
	}
	return meta, body
}

// renderAgentMarkdown builds the user-editable markdown file from the frontmatter
// fields the UI manages plus the instruction body.
func RenderAgentMarkdown(f *OmpAgentFields) string {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("name: " + f.Name + "\n")
	if f.Description != "" {
		b.WriteString("description: " + f.Description + "\n")
	}
	if f.Model != "" {
		b.WriteString("model: " + f.Model + "\n")
	}
	if f.Tools != "" {
		b.WriteString("tools: " + f.Tools + "\n")
	}
	if f.ThinkingLevel != "" {
		b.WriteString("thinkingLevel: " + f.ThinkingLevel + "\n")
	}
	if f.Skills != "" {
		b.WriteString("autoloadSkills: " + f.Skills + "\n")
	}
	if f.Blocking {
		b.WriteString("blocking: true\n")
	}
	if f.Structured {
		b.WriteString("structured: true\n")
	}
	if f.ReadSummarize {
		b.WriteString("readSummarize: true\n")
	}
	if s := renderSpawns(f.Spawns); s != "" {
		b.WriteString(s + "\n")
	}
	if f.Prewalk != "" {
		b.WriteString("prewalk: " + f.Prewalk + "\n")
	}
	if f.Advisor != "" {
		b.WriteString("advisor: " + f.Advisor + "\n")
	}
	if strings.TrimSpace(f.Output) != "" {
		writeYAMLBlock(&b, "output", f.Output)
	}
	if strings.TrimSpace(f.Knowledge) != "" {
		writeLiteralBlock(&b, "knowledge", f.Knowledge)
	}
	b.WriteString("---\n")
	if body := strings.TrimSpace(f.Body); body != "" {
		b.WriteString("\n" + body + "\n")
	}
	return b.String()
}

// writeYAMLBlock writes a multi-line value as an indented YAML object under key
// (used for the nested `output` schema): each line keeps its relative indent,
// shifted 2 spaces so it nests under the key.
func writeYAMLBlock(b *strings.Builder, key, value string) {
	b.WriteString(key + ":\n")
	for _, ln := range strings.Split(strings.TrimSuffix(strings.TrimSpace(value), "\n"), "\n") {
		b.WriteString("  " + ln + "\n")
	}
}

// writeLiteralBlock writes a markdown value as a YAML literal block (`|`),
// preserving blank lines. Used for `knowledge`, an omp-ignored field.
func writeLiteralBlock(b *strings.Builder, key, value string) {
	b.WriteString(key + ": |\n")
	for _, ln := range strings.Split(strings.TrimSuffix(value, "\n"), "\n") {
		b.WriteString("  " + ln + "\n")
	}
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
// agentInfoFrom builds the API-facing summary for a user-scope agent written by
// CreateUserAgent/UpdateUserAgent.
func agentInfoFrom(f *OmpAgentFields, path, content string) *OmpAgentInfo {
	return &OmpAgentInfo{
		Name:          f.Name,
		Description:   f.Description,
		Model:         f.Model,
		Tools:         f.Tools,
		ThinkingLevel: f.ThinkingLevel,
		Spawns:        f.Spawns,
		Skills:        f.Skills,
		Output:        f.Output,
		Prewalk:       f.Prewalk,
		Advisor:       f.Advisor,
		Blocking:      f.Blocking,
		Structured:    f.Structured,
		ReadSummarize: f.ReadSummarize,
		Knowledge:     f.Knowledge,
		Scope:         "user",
		Source:        path,
		path:          path,
		content:       content,
	}
}

// createUserAgent writes a new custom agent into the user scope
// (`~/.omp/agent/agents/<name>.md`) and refuses to overwrite an existing file.
func CreateUserAgent(f *OmpAgentFields) (*OmpAgentInfo, error) {
	if !ValidAgentName(f.Name) {
		return nil, fmt.Errorf("invalid agent name %q (want [A-Za-z0-9_-]{1,64})", f.Name)
	}
	dir, err := userAgentDir()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, f.Name+".md")
	if _, err := os.Stat(path); err == nil {
		return nil, fmt.Errorf("an agent named %q already exists (user scope); edit it instead", f.Name)
	}
	content := RenderAgentMarkdown(f)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return nil, fmt.Errorf("write agent %s: %w", path, err)
	}
	return agentInfoFrom(f, path, content), nil
}

// updateUserAgent replaces the user-scope file for name. The name itself never
// changes through an update (rename = delete + create).
func UpdateUserAgent(f *OmpAgentFields) (*OmpAgentInfo, error) {
	if !ValidAgentName(f.Name) {
		return nil, fmt.Errorf("invalid agent name %q (want [A-Za-z0-9_-]{1,64})", f.Name)
	}
	dir, err := userAgentDir()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, f.Name+".md")
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("agent %q not found in user scope", f.Name)
	}
	content := RenderAgentMarkdown(f)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return nil, fmt.Errorf("write agent %s: %w", path, err)
	}
	return agentInfoFrom(f, path, content), nil
}

// CopyUserAgent forks any visible agent (bundled/user/project) into a new
// user-scope definition under a fresh name, preserving every frontmatter field
// and the instruction body.
func CopyUserAgent(projectWorkdirs map[string]string, sourceName, newName string) (*OmpAgentInfo, error) {
	if !ValidAgentName(newName) {
		return nil, fmt.Errorf("invalid agent name %q (want [A-Za-z0-9_-]{1,64})", newName)
	}
	var src *OmpAgentInfo
	for _, a := range DiscoverOmpAgents(projectWorkdirs) {
		if a.Name == sourceName {
			src = a
			break
		}
	}
	if src == nil {
		return nil, fmt.Errorf("source agent %q not found", sourceName)
	}
	content, err := ReadAgentDetail(src)
	if err != nil {
		return nil, fmt.Errorf("read source agent %q: %w", sourceName, err)
	}
	meta, body := ParseAgentMarkdown(content)
	return CreateUserAgent(&OmpAgentFields{
		Name:          newName,
		Description:   meta["description"],
		Model:         meta["model"],
		Tools:         meta["tools"],
		ThinkingLevel: meta["thinkingLevel"],
		Spawns:        meta["spawns"],
		Skills:        meta["autoloadSkills"],
		Output:        meta["output"],
		Prewalk:       meta["prewalk"],
		Advisor:       meta["advisor"],
		Blocking:      meta["blocking"] == "true",
		Structured:    meta["structured"] == "true",
		ReadSummarize: meta["readSummarize"] == "true",
		Knowledge:     meta["knowledge"],
		Body:          body,
	})
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
