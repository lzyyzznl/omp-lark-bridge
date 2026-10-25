package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/lzyyzznl/omp-lark-bridge/config"
)

func runProjectCommand(args []string) {
	if len(args) == 0 {
		printProjectUsage()
		os.Exit(1)
	}

	switch args[0] {
	case "list":
		runProjectList(args[1:])
	case "create":
		runProjectCreate(args[1:])
	case "show":
		runProjectShow(args[1:])
	case "set":
		runProjectSet(args[1:])
	case "remove":
		runProjectRemove(args[1:])
	case "help", "--help", "-h":
		printProjectUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown project subcommand: %s\n\n", args[0])
		printProjectUsage()
		os.Exit(1)
	}
}

func printProjectUsage() {
	fmt.Println(`Usage: omp-lark-bridge project <command> [options]

项目是多飞书通道绑定到不同工作目录的载体：每个项目是一个独立的 omp agent
（独立 work_dir = 会话启动文件夹 + 独立飞书应用）。定时任务挂靠项目，
项目是最大颗粒度，一个项目下可有多个定时任务。

Commands:
  list                      列出全部项目
  create <name>             新建项目（agent=omp + feishu 平台槽位）
  show <name>               查看项目配置详情
  set <name>                修改项目参数
  remove <name>             从配置移除项目

Options:
  --config <path>            配置文件路径（默认自动发现）
  --work-dir <path>          Agent 工作目录（set 时传空串可清空）
  --model <model>            默认模型（如 deepseek/deepseek-flash）
  --admin-from <ids>         管理员 user ID（逗号分隔，或 *）
  --allow-from <ids>         允许使用用户（应用到项目所有 feishu 平台）
  --disabled-commands <list> 禁用命令列表（逗号分隔）

Examples:
  omp-lark-bridge project create my-ops --work-dir /srv/ops --model deepseek/deepseek-flash
  omp-lark-bridge project list
  omp-lark-bridge project show my-ops
  omp-lark-bridge project set my-ops --admin-from ou_xxx --allow-from ou_a,ou_b
  omp-lark-bridge project set my-ops --work-dir ""
  omp-lark-bridge project remove my-ops
  # 给项目绑定独立飞书应用：
  omp-lark-bridge feishu bind --project my-ops --app cli_xxx:sec_xxx`)
}

func runProjectList(args []string) {
	fs := flag.NewFlagSet("project list", flag.ExitOnError)
	configFile := fs.String("config", "", "path to config file")
	_ = fs.Parse(args)
	initConfigPath(*configFile)

	names, err := config.ListProjects()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	sort.Strings(names)
	if len(names) == 0 {
		fmt.Println("（没有项目）")
		return
	}
	fmt.Printf("共 %d 个项目：\n", len(names))
	for _, n := range names {
		wd := ""
		if d := config.GetProjectConfigDetails(n); d != nil {
			if s, ok := d["work_dir"].(string); ok {
				wd = s
			}
		}
		if wd != "" {
			fmt.Printf("  %-24s %s\n", n, wd)
		} else {
			fmt.Printf("  %-24s（未设 work_dir，用默认目录）\n", n)
		}
	}
}

func runProjectCreate(args []string) {
	fs := flag.NewFlagSet("project create", flag.ExitOnError)
	configFile := fs.String("config", "", "path to config file")
	workDir := fs.String("work-dir", "", "agent work dir (session start folder)")
	model := fs.String("model", "", "default model for the project agent")
	// Go's flag stops at the first non-flag arg, so <name> is peeled off first.
	name := ""
	if len(args) > 0 {
		name = strings.TrimSpace(args[0])
	}
	_ = fs.Parse(args[1:])
	if name == "" || strings.HasPrefix(name, "-") {
		fmt.Fprintln(os.Stderr, "Error: project name is required: omp-lark-bridge project create <name>")
		os.Exit(1)
	}
	initConfigPath(*configFile)

	res, err := config.EnsureProjectWithFeishuPlatform(config.EnsureProjectWithFeishuOptions{
		ProjectName:  name,
		PlatformType: "feishu",
		AgentType:    "omp",
		WorkDir:      strings.TrimSpace(*workDir),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if *model != "" {
		if err := config.SaveAgentModel(name, strings.TrimSpace(*model)); err != nil {
			fmt.Fprintf(os.Stderr, "Error setting model: %v\n", err)
			os.Exit(1)
		}
	}

	if res.Created {
		fmt.Printf("✅ 项目 %q 已创建（agent=omp，feishu 平台槽位就绪）\n", name)
	} else {
		fmt.Printf("ℹ️  项目 %q 已存在；已确保 feishu 平台槽位\n", name)
	}
	if strings.TrimSpace(*workDir) != "" {
		fmt.Printf("   会话启动文件夹（work_dir）: %s\n", *workDir)
	}
	if *model != "" {
		fmt.Printf("   默认模型: %s\n", *model)
	}
	fmt.Printf("\n下一步：\n  1. 绑定独立飞书应用:  omp-lark-bridge feishu bind --project %s --app app_id:app_secret\n", name)
	fmt.Printf("  2. 重启: omp-lark-bridge（重启后该项目的飞书机器人上线）\n")
}

func runProjectShow(args []string) {
	fs := flag.NewFlagSet("project show", flag.ExitOnError)
	configFile := fs.String("config", "", "path to config file")
	// Go's flag stops at the first non-flag arg, so <name> is peeled off first.
	name := ""
	if len(args) > 0 {
		name = strings.TrimSpace(args[0])
	}
	_ = fs.Parse(args[1:])
	if name == "" || strings.HasPrefix(name, "-") {
		fmt.Fprintln(os.Stderr, "Error: project name is required: omp-lark-bridge project show <name>")
		os.Exit(1)
	}
	initConfigPath(*configFile)
	cfg, err := config.Load(config.ConfigPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	var proj *config.ProjectConfig
	for i := range cfg.Projects {
		if cfg.Projects[i].Name == name {
			proj = &cfg.Projects[i]
			break
		}
	}
	if proj == nil {
		fmt.Fprintf(os.Stderr, "Error: project %q not found\n", name)
		os.Exit(1)
	}

	fmt.Printf("项目:          %s\n", proj.Name)
	fmt.Printf("agent:         %s\n", proj.Agent.Type)
	if proj.Mode != "" {
		fmt.Printf("mode:          %s\n", proj.Mode)
	}
	workDir, _ := proj.Agent.Options["work_dir"].(string)
	if workDir == "" {
		fmt.Println("work_dir:      （未设置，用默认目录）")
	} else {
		fmt.Printf("work_dir:      %s\n", workDir)
	}
	if m, _ := proj.Agent.Options["model"].(string); m != "" {
		fmt.Printf("model:         %s\n", m)
	}
	if proj.AdminFrom != "" {
		fmt.Printf("admin_from:    %s\n", proj.AdminFrom)
	}
	if len(proj.DisabledCommands) > 0 {
		fmt.Printf("disabled_cmds: %s\n", strings.Join(proj.DisabledCommands, ", "))
	}
	for i, p := range proj.Platforms {
		af, _ := p.Options["allow_from"].(string)
		fmt.Printf("platform[%d]:   type=%s allow_from=%q\n", i+1, p.Type, af)
		if len(p.Options) > 0 {
			hasCreds := false
			for _, k := range []string{"app_id", "app_secret", "token", "webhook"} {
				if v, ok := p.Options[k]; ok && fmt.Sprintf("%v", v) != "" {
					hasCreds = true
				}
			}
			if hasCreds {
				fmt.Printf("               （已配置凭证，隐藏）\n")
			}
		}
	}
}

func runProjectSet(args []string) {
	fs := flag.NewFlagSet("project set", flag.ExitOnError)
	configFile := fs.String("config", "", "path to config file")
	workDir := fs.String("work-dir", "", "new agent work dir (empty string clears it)")
	model := fs.String("model", "", "default model")
	adminFrom := fs.String("admin-from", "", "admin user id(s), comma separated or *")
	allowFrom := fs.String("allow-from", "", "allowed user id(s), applied to all feishu platforms")
	disabledCmds := fs.String("disabled-commands", "", "disabled commands, comma separated")
	// Go's flag stops at the first non-flag arg, so the positional <name> must
	// be peeled off before parsing the remaining flags.
	name := ""
	if len(args) > 0 {
		name = strings.TrimSpace(args[0])
	}
	_ = fs.Parse(args[1:])
	if name == "" || strings.HasPrefix(name, "-") {
		fmt.Fprintln(os.Stderr, "Error: project name is required: omp-lark-bridge project set <name> [options]")
		os.Exit(1)
	}
	initConfigPath(*configFile)

	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	if len(set) == 0 {
		fmt.Fprintln(os.Stderr, "Error: no options given (--work-dir / --model / --admin-from / --allow-from / --disabled-commands)")
		os.Exit(1)
	}

	update := config.ProjectSettingsUpdate{}
	if set["work-dir"] {
		update.WorkDir = workDir
	}
	if set["admin-from"] {
		update.AdminFrom = adminFrom
	}
	if set["disabled-commands"] {
		parts := splitCSV(*disabledCmds)
		update.DisabledCommands = parts
	}
	if set["allow-from"] {
		// Apply to every feishu/lark platform of the project.
		cfg, err := config.Load(config.ConfigPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		paf := map[string]string{}
		for i := range cfg.Projects {
			if cfg.Projects[i].Name == name {
				for _, p := range cfg.Projects[i].Platforms {
					typ := strings.ToLower(strings.TrimSpace(p.Type))
					if typ == "" {
						continue
					}
					paf[typ] = *allowFrom
				}
			}
		}
		if len(paf) == 0 {
			fmt.Fprintf(os.Stderr, "Error: project %q has no feishu/lark platforms to apply allow_from\n", name)
			os.Exit(1)
		}
		update.PlatformAllowFrom = paf
	}

	if err := config.SaveProjectSettings(name, update); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if set["model"] {
		if err := config.SaveAgentModel(name, strings.TrimSpace(*model)); err != nil {
			fmt.Fprintf(os.Stderr, "Error setting model: %v\n", err)
			os.Exit(1)
		}
	}
	if len(set) == 1 && set["model"] {
		// model already printed the only change; skip extra line
	}
	fmt.Printf("✅ 项目 %q 已更新：%s\n", name, strings.Join(keysOfBoolMap(set), ", "))
	fmt.Println("   重启 omp-lark-bridge 后生效")
}

func runProjectRemove(args []string) {
	fs := flag.NewFlagSet("project remove", flag.ExitOnError)
	configFile := fs.String("config", "", "path to config file")
	yes := fs.Bool("yes", false, "skip confirmation")
	// Go's flag stops at the first non-flag arg, so <name> is peeled off first.
	name := ""
	if len(args) > 0 {
		name = strings.TrimSpace(args[0])
	}
	_ = fs.Parse(args[1:])
	if name == "" || strings.HasPrefix(name, "-") {
		fmt.Fprintln(os.Stderr, "Error: project name is required: omp-lark-bridge project remove <name>")
		os.Exit(1)
	}
	initConfigPath(*configFile)

	if !*yes {
		fmt.Printf("确认从配置移除项目 %q ？此操作不可撤销（y/N）: ", name)
		var ans string
		if _, err := fmt.Scanln(&ans); err != nil && ans == "" {
			fmt.Fprintln(os.Stderr, "\n已取消")
			os.Exit(1)
		}
		ans = strings.ToLower(strings.TrimSpace(ans))
		if ans != "y" && ans != "yes" {
			fmt.Println("已取消")
			return
		}
	}

	if err := config.RemoveProject(name); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✅ 项目 %q 已从配置移除（config.toml 中该项目块已删除）\n", name)
	fmt.Println("   重启 omp-lark-bridge 后生效；其飞书应用不再由本进程托管")
}

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := parts[:0]
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func keysOfBoolMap(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, "--"+k)
	}
	sort.Strings(out)
	return out
}
