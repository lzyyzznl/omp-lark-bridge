package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/lzyyzznl/omp-lark-bridge/config"
	"github.com/lzyyzznl/omp-lark-bridge/core"
)

// runAgentsCommand manages omp agent personas from the CLI, mirroring the web
// UI's agent 人格 page: list/show across bundled+user+project scopes, and
// create/edit/delete for the user scope (~/.omp/agent/agents/<name>.md).
func runAgentsCommand(args []string) {
	if len(args) == 0 {
		printAgentsUsage()
		os.Exit(1)
	}
	switch args[0] {
	case "list":
		runAgentsList(args[1:])
	case "show":
		runAgentsShow(args[1:])
	case "new", "create":
		runAgentsNew(args[1:])
	case "edit":
		runAgentsEdit(args[1:])
	case "del", "delete", "rm", "remove":
		runAgentsDel(args[1:])
	case "help", "--help", "-h":
		printAgentsUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown agents subcommand: %s\n\n", args[0])
		printAgentsUsage()
		os.Exit(1)
	}
}

// agentProjectWorkdirs maps every configured project to its work_dir (when set),
// so project-scope agents are discoverable the same way the running daemon sees
// them. A missing/unparseable config yields an empty map, not an error.
func agentProjectWorkdirs(configFile string) map[string]string {
	initConfigPath(configFile)
	cfg, err := config.Load(config.ConfigPath)
	if err != nil {
		return nil
	}
	out := map[string]string{}
	for _, p := range cfg.Projects {
		if wd, _ := p.Agent.Options["work_dir"].(string); wd != "" {
			out[p.Name] = wd
		}
	}
	return out
}

func runAgentsList(args []string) {
	fs := flag.NewFlagSet("agents list", flag.ExitOnError)
	configFile := fs.String("config", "", "path to config file")
	_ = fs.Parse(args)

	agents := core.DiscoverOmpAgents(agentProjectWorkdirs(*configFile))
	if agents == nil {
		agents = []*core.OmpAgentInfo{}
	}
	sort.Slice(agents, func(i, j int) bool { return agents[i].Scope+agents[i].Name < agents[j].Scope+agents[j].Name })

	if len(agents) == 0 {
		fmt.Println("No agents found.")
		return
	}

	fmt.Printf("%-20s %-9s %-22s %-9s %-14s %s\n", "NAME", "SCOPE", "MODEL", "THINKING", "SPAWNS", "DESCRIPTION")
	for _, a := range agents {
		model := a.Model
		if model == "" {
			model = "-"
		}
		thinking := a.ThinkingLevel
		if thinking == "" {
			thinking = "-"
		}
		spawns := a.Spawns
		if spawns == "" {
			spawns = "-"
		}
		desc := a.Description
		if len(desc) > 40 {
			desc = desc[:40] + "…"
		}
		scope := a.Scope
		if a.Project != "" {
			scope = a.Project + "(project)"
		}
		fmt.Printf("%-20s %-9s %-22s %-9s %-14s %s\n", a.Name, scope, model, thinking, spawns, desc)
	}
}

func runAgentsShow(args []string) {
	fs := flag.NewFlagSet("agents show", flag.ExitOnError)
	configFile := fs.String("config", "", "path to config file")
	name := ""
	if len(args) > 0 {
		name = strings.TrimSpace(args[0])
	}
	_ = fs.Parse(args[1:])
	if name == "" || strings.HasPrefix(name, "-") {
		fmt.Fprintln(os.Stderr, "Error: agent name is required: omp-lark-bridge agents show <name>")
		os.Exit(1)
	}

	var found *core.OmpAgentInfo
	for _, a := range core.DiscoverOmpAgents(agentProjectWorkdirs(*configFile)) {
		if a.Name == name {
			found = a
			break
		}
	}
	if found == nil {
		fmt.Fprintf(os.Stderr, "Error: agent %q not found\n", name)
		os.Exit(1)
	}
	content, err := core.ReadAgentDetail(found)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(content)
}

func runAgentsNew(args []string) {
	fs := flag.NewFlagSet("agents new", flag.ExitOnError)
	desc := fs.String("desc", "", "one-line description")
	model := fs.String("model", "", "model (blank = inherit)")
	tools := fs.String("tools", "", "tools, comma separated (blank = all)")
	thinking := fs.String("thinking", "", "thinking level: auto|low|medium|high (blank = inherit)")
	spawns := fs.String("spawns", "", "spawnable subagents, comma separated (* = any)")
	body := fs.String("body", "", "instruction body")
	name := ""
	if len(args) > 0 {
		name = strings.TrimSpace(args[0])
	}
	_ = fs.Parse(args[1:])
	if name == "" || strings.HasPrefix(name, "-") || !core.ValidAgentName(name) {
		fmt.Fprintln(os.Stderr, "Error: agent name must be [A-Za-z0-9_-]{1,64}: omp-lark-bridge agents new <name>")
		os.Exit(1)
	}

	info, err := core.CreateUserAgent(name, *desc, *model, *tools, *thinking, *spawns, *body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Created agent %q (user scope): %s\n", info.Name, info.Source)
}

func runAgentsEdit(args []string) {
	fs := flag.NewFlagSet("agents edit", flag.ExitOnError)
	configFile := fs.String("config", "", "path to config file")
	desc := fs.String("desc", "", "new one-line description")
	model := fs.String("model", "", "new model")
	tools := fs.String("tools", "", "new tools, comma separated")
	thinking := fs.String("thinking", "", "new thinking level")
	spawns := fs.String("spawns", "", "new spawns list")
	body := fs.String("body", "", "new instruction body")
	name := ""
	if len(args) > 0 {
		name = strings.TrimSpace(args[0])
	}
	_ = fs.Parse(args[1:])
	if name == "" || strings.HasPrefix(name, "-") {
		fmt.Fprintln(os.Stderr, "Error: agent name is required: omp-lark-bridge agents edit <name>")
		os.Exit(1)
	}

	// Load the current user-scope definition; every flag left blank keeps the
	// existing value. bundled/project agents are not editable through this path.
	var found *core.OmpAgentInfo
	for _, a := range core.DiscoverOmpAgents(agentProjectWorkdirs(*configFile)) {
		if a.Name == name {
			found = a
			break
		}
	}
	if found == nil {
		fmt.Fprintf(os.Stderr, "Error: agent %q not found\n", name)
		os.Exit(1)
	}
	if !core.IsEditableAgentScope(found.Scope) {
		fmt.Fprintf(os.Stderr, "Error: agent %q is %s scope (read-only); create a custom agent instead\n", name, found.Scope)
		os.Exit(1)
	}
	content, err := core.ReadAgentDetail(found)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	meta, curBody := core.ParseAgentMarkdown(content)

	next := func(flagVal, key string) string {
		if flagVal != "" {
			return flagVal
		}
		return meta[key]
	}
	nextBody := curBody
	if *body != "" {
		nextBody = *body
	}

	info, err := core.UpdateUserAgent(name, next(*desc, "description"), next(*model, "model"),
		next(*tools, "tools"), next(*thinking, "thinkingLevel"), next(*spawns, "spawns"), nextBody)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Updated agent %q (user scope): %s\n", info.Name, info.Source)
}

func runAgentsDel(args []string) {
	fs := flag.NewFlagSet("agents del", flag.ExitOnError)
	name := ""
	if len(args) > 0 {
		name = strings.TrimSpace(args[0])
	}
	_ = fs.Parse(args[1:])
	if name == "" || strings.HasPrefix(name, "-") {
		fmt.Fprintln(os.Stderr, "Error: agent name is required: omp-lark-bridge agents del <name>")
		os.Exit(1)
	}
	if err := core.DeleteUserAgent(name); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Deleted agent %q (user scope)\n", name)
}

func printAgentsUsage() {
	fmt.Println(`Usage: omp-lark-bridge agents <command> [options]

管理 omp agent 人格（与 Web 面板「agent 人格」页一致）：
列出全部内置/自定义/项目 agent，或对自定义（user 范围）agent 增改删。

Commands:
  list                     列出全部可见 agent（内置 + 自定义 + 项目三档）
  show <name>              查看某 agent 的完整 markdown 源码
  new <name>               新建自定义 agent（user 范围）
  edit <name>              修改自定义 agent（未指定的字段保持不变）
  del <name>               删除自定义 agent

Options:
  --config <path>          配置文件路径（决定项目范围 agent 的扫描；默认自动发现）
  --desc <text>            描述（一句话）
  --model <name>           模型（空 = 继承主会话；如 @slow / deepseek/deepseek-flash）
  --tools <list>           可用工具（逗号分隔；空 = 全部）
  --thinking <level>       思考强度：auto|low|medium|high（空 = 继承）
  --spawns <list>          可派生子代理（逗号分隔；* = 任意；空 = 不限制）
  --body <text>            指令正文（人格 instructions）

Examples:
  omp-lark-bridge agents list
  omp-lark-bridge agents new release-auditor --desc "检查发布就绪" --thinking high --spawns scout --body "检查发布范围并汇报阻塞项。"
  omp-lark-bridge agents edit release-auditor --thinking medium --desc "检查发布就绪与回滚"
  omp-lark-bridge agents show scout
  omp-lark-bridge agents del release-auditor`)
}
