package main

import (
	"flag"
	"fmt"
	"io"
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
	case "tools":
		runAgentsTools(args[1:])
	case "skills":
		runAgentsSkills(args[1:])
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
	configFile := fs.String("config", "", "path to config file (used with --from)")
	desc := fs.String("desc", "", "one-line description")
	model := fs.String("model", "", "model (blank = inherit)")
	tools := fs.String("tools", "", "tools, comma separated (blank = omp default set)")
	thinking := fs.String("thinking", "", "thinking level: off|minimal|low|medium|high|xhigh|max|auto")
	spawns := fs.String("spawns", "", "spawnable subagents, comma separated (* = any)")
	skills := fs.String("skills", "", "skills to autoload into context, comma separated")
	output := fs.String("output", "", "structured output schema (YAML block; @file or - for stdin)")
	prewalk := fs.String("prewalk", "", "prewalk: true|false|model-pattern")
	advisor := fs.String("advisor", "", "advisor role: true|false|model-pattern")
	blocking := fs.String("blocking", "", "blocking subagent: true|false")
	structured := fs.String("structured", "", "force structured output: true|false")
	readSummarize := fs.String("read-summarize", "", "summarize long reads: true|false")
	knowledge := fs.String("knowledge", "", "background knowledge doc (markdown; @file or - for stdin)")
	from := fs.String("from", "", "fork an existing agent (bundled/user/project) as the starting point")
	body := fs.String("body", "", "instruction body (@file or - for stdin)")
	name := ""
	if len(args) > 0 {
		name = strings.TrimSpace(args[0])
	}
	_ = fs.Parse(args[1:])
	if name == "" || strings.HasPrefix(name, "-") || !core.ValidAgentName(name) {
		fmt.Fprintln(os.Stderr, "Error: agent name must be [A-Za-z0-9_-]{1,64}: omp-lark-bridge agents new <name>")
		os.Exit(1)
	}

	// --from forks an existing definition wholesale; everything else is ignored.
	if *from != "" {
		info, err := core.CopyUserAgent(agentProjectWorkdirs(*configFile), *from, name)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Forked agent %q from %q (user scope): %s\n", info.Name, *from, info.Source)
		return
	}

	outputV, err := resolveTextArg(*output)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: --output: %v\n", err)
		os.Exit(1)
	}
	knowledgeV, err := resolveTextArg(*knowledge)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: --knowledge: %v\n", err)
		os.Exit(1)
	}
	bodyV, err := resolveTextArg(*body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: --body: %v\n", err)
		os.Exit(1)
	}

	info, err := core.CreateUserAgent(&core.OmpAgentFields{
		Name:          name,
		Description:   *desc,
		Model:         *model,
		Tools:         *tools,
		ThinkingLevel: *thinking,
		Spawns:        *spawns,
		Skills:        *skills,
		Output:        outputV,
		Prewalk:       *prewalk,
		Advisor:       *advisor,
		Blocking:      *blocking == "true",
		Structured:    *structured == "true",
		ReadSummarize: *readSummarize == "true",
		Knowledge:     knowledgeV,
		Body:          bodyV,
	})
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
	skills := fs.String("skills", "", "new skills list (^ to clear)")
	output := fs.String("output", "", "new output schema (@file or - for stdin; ^ to clear)")
	prewalk := fs.String("prewalk", "", "new prewalk: true|false|model-pattern")
	advisor := fs.String("advisor", "", "new advisor: true|false|model-pattern")
	blocking := fs.String("blocking", "", "new blocking: true|false (blank = keep)")
	structured := fs.String("structured", "", "new structured: true|false (blank = keep)")
	readSummarize := fs.String("read-summarize", "", "new readSummarize: true|false (blank = keep)")
	knowledge := fs.String("knowledge", "", "new knowledge doc (@file or - for stdin; ^ to clear)")
	body := fs.String("body", "", "new instruction body (@file or - for stdin; ^ to clear)")
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
	bodyV, err := resolveKeep(*body, curBody)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: --body: %v\n", err)
		os.Exit(1)
	}
	outputV, err := resolveKeep(*output, meta["output"])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: --output: %v\n", err)
		os.Exit(1)
	}
	knowledgeV, err := resolveKeep(*knowledge, meta["knowledge"])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: --knowledge: %v\n", err)
		os.Exit(1)
	}

	info, err := core.UpdateUserAgent(&core.OmpAgentFields{
		Name:          name,
		Description:   next(*desc, "description"),
		Model:         next(*model, "model"),
		Tools:         next(*tools, "tools"),
		ThinkingLevel: next(*thinking, "thinkingLevel"),
		Spawns:        next(*spawns, "spawns"),
		Skills:        next(*skills, "autoloadSkills"),
		Output:        outputV,
		Prewalk:       next(*prewalk, "prewalk"),
		Advisor:       next(*advisor, "advisor"),
		Blocking:      curBool(*blocking, meta["blocking"] == "true"),
		Structured:    curBool(*structured, meta["structured"] == "true"),
		ReadSummarize: curBool(*readSummarize, meta["readSummarize"] == "true"),
		Knowledge:     knowledgeV,
		Body:          bodyV,
	})
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

func runAgentsTools(args []string) {
	fs := flag.NewFlagSet("agents tools", flag.ExitOnError)
	_ = fs.Parse(args)
	tools := core.OmpTools()
	if len(tools) == 0 {
		fmt.Println("No tools available.")
		return
	}
	fmt.Println("可用工具（白名单；创建人格时用 --tools 逗号分隔选取）：")
	for _, t := range tools {
		fmt.Printf("  %-14s %s\n", t.Name, t.Description)
	}
}

func runAgentsSkills(args []string) {
	fs := flag.NewFlagSet("agents skills", flag.ExitOnError)
	dir := fs.String("dir", "", "agent work directory to resolve skills from (default: current dir)")
	_ = fs.Parse(args)
	wd := *dir
	if wd == "" {
		var err error
		if wd, err = os.Getwd(); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	}
	skills, err := core.OmpSkillList(wd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if len(skills) == 0 {
		fmt.Println("No skills found.")
		return
	}
	fmt.Println("当前可用技能（人格勾选后仅注入这些技能；--skills 逗号分隔，留空 = 全部）：")
	for _, s := range skills {
		hidden := ""
		if s.Hide {
			hidden = " (hidden)"
		}
		fmt.Printf("  %-40s %s%s\n", s.Name, s.Description, hidden)
	}
}
func printAgentsUsage() {
	fmt.Println(`Usage: omp-lark-bridge agents <command> [options]

管理 omp agent 人格（与 Web 面板「agent 人格」页一致）：
列出全部内置/自定义/项目 agent，或对自定义（user 范围）agent 增改删。

Commands:
  list                     列出全部可见 agent（内置 + 自定义 + 项目三档）
  show <name>              查看某 agent 的完整 markdown 源码
  new <name>               新建自定义 agent（user 范围；--from 可从内置 fork）
  edit <name>              修改自定义 agent（未指定的字段保持原值；^ 清除多行字段）
  del <name>               删除自定义 agent
  tools                    列出创建人格可选用的工具白名单（--tools 的取值来源）
  skills [--dir <path>]    列出当前可用技能（--skills 的取值来源；默认当前目录）

Options:
  --config <path>          配置文件路径（决定项目范围 agent 的扫描；默认自动发现）
  --desc <text>            描述（一句话）
  --model <name>           模型（空 = 继承主会话；如 @slow / deepseek/deepseek-flash）
  --tools <list>           可用工具（逗号分隔；空 = omp 默认集）
  --thinking <level>       思考强度：off|minimal|low|medium|high|xhigh|max|auto
  --spawns <list>          可派生子代理（逗号分隔；* = 任意）
  --skills <list>          技能白名单（逗号分隔；勾选 = 仅这些技能，留空 = 全部）
  --output <yaml>          结构化输出 schema（多行；@file / - 读 stdin）
  --prewalk <v>            prewalk：true|false|模型模式
  --advisor <v>            advisor：true|false|模型模式
  --blocking <v>           阻塞型子代理：true|false
  --structured <v>         强制结构化输出：true|false
  --read-summarize <v>     长文档摘要：true|false
  --knowledge <md>         背景/知识文档（markdown；@file / - 读 stdin）
  --from <name>            new 时从既有 agent fork（内置/自定义/项目均可）
  --body <text>            指令正文（人格 instructions；@file / - 读 stdin）

填空参数约定（edit）：
  --output/--knowledge/--body 传 "^" 清除；空表示保持原值。
  --blocking/--structured/--read-summarize 空表示保持原值。

Examples:
  omp-lark-bridge agents tools
  omp-lark-bridge agents skills --dir /path/to/project
  omp-lark-bridge agents list
  omp-lark-bridge agents new release-auditor --from reviewer --desc "检查发布就绪"
  omp-lark-bridge agents new release-auditor --desc "检查发布就绪" --thinking high --skills gh --body "检查发布范围并汇报阻塞项。"
  omp-lark-bridge agents edit release-auditor --thinking medium --body @prompt.md
  omp-lark-bridge agents show scout
  omp-lark-bridge agents del release-auditor`)
}

// resolveTextArg returns arg verbatim unless it names an external source:
// "-" reads stdin, "@path" reads the file at path.
func resolveTextArg(arg string) (string, error) {
	if arg == "" {
		return "", nil
	}
	if arg == "-" {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", fmt.Errorf("read stdin: %w", err)
		}
		return string(data), nil
	}
	if strings.HasPrefix(arg, "@") {
		p := strings.TrimPrefix(arg, "@")
		data, err := os.ReadFile(p)
		if err != nil {
			return "", fmt.Errorf("read %s: %w", p, err)
		}
		return string(data), nil
	}
	return arg, nil
}

// resolveKeep resolves an edit-time multi-line flag: blank keeps the current
// value, "^" clears it, otherwise resolveTextArg.
func resolveKeep(arg, current string) (string, error) {
	if arg == "" {
		return current, nil
	}
	if arg == "^" {
		return "", nil
	}
	return resolveTextArg(arg)
}

// curBool resolves an edit-time boolean flag: blank keeps the current value.
func curBool(arg string, current bool) bool {
	if arg == "" {
		return current
	}
	return arg == "true"
}
