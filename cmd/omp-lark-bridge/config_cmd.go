package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	ccconnect "github.com/lzyyzznl/omp-lark-bridge"
	"github.com/lzyyzznl/omp-lark-bridge/config"
)

func runConfig(args []string) {
	if len(args) == 0 {
		printConfigUsage()
		os.Exit(1)
	}
	switch args[0] {
	case "example":
		fmt.Print(ccconnect.ConfigExampleTOML)
	case "format", "fmt":
		runConfigFormat(args[1:])
	case "path":
		fmt.Println(resolveConfigPath(""))
	case "get":
		runConfigGet(args[1:])
	case "set":
		runConfigSet(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "Unknown config subcommand: %s\n", args[0])
		printConfigUsage()
		os.Exit(1)
	}
}

// splitConfigArgs pulls --config/-config out of args wherever it appears, so
// `config set key value --config path` works (the flag package stops parsing at
// the first positional argument) while leaving the positionals in order.
func splitConfigArgs(args []string) (configPath string, positional []string, err error) {
	for i := 0; i < len(args); i++ {
		switch arg := args[i]; {
		case arg == "--config" || arg == "-config":
			if i+1 >= len(args) {
				return "", nil, fmt.Errorf("%s requires a path", arg)
			}
			configPath = args[i+1]
			i++
		case strings.HasPrefix(arg, "--config="):
			configPath = strings.TrimPrefix(arg, "--config=")
		case strings.HasPrefix(arg, "-config="):
			configPath = strings.TrimPrefix(arg, "-config=")
		default:
			positional = append(positional, arg)
		}
	}
	return configPath, positional, nil
}

// runConfigGet prints one scalar setting: `config get management.token`.
func runConfigGet(args []string) {
	configPath, positional, err := splitConfigArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if len(positional) != 1 {
		fmt.Fprintln(os.Stderr, "Usage: omp-lark-bridge config get <dotted.key> [--config <path>]")
		os.Exit(1)
	}

	path := resolveConfigPath(configPath)
	cfg, err := config.LoadRaw(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}
	key := positional[0]
	value, err := config.LookupScalar(cfg, key)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if !config.IsScalar(value.Kind()) {
		fmt.Fprintf(os.Stderr, "Error: %s is not a scalar setting\n", key)
		os.Exit(1)
	}
	fmt.Println(config.ScalarString(value))
}

// runConfigSet writes one scalar setting: `config set management.token 1`.
// Comments and formatting of the rest of the file are preserved; the running
// instance keeps the old value until it is restarted.
func runConfigSet(args []string) {
	configPath, positional, err := splitConfigArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if len(positional) != 2 {
		fmt.Fprintln(os.Stderr, "Usage: omp-lark-bridge config set <dotted.key> <value> [--config <path>]")
		os.Exit(1)
	}

	path := resolveConfigPath(configPath)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "Config file not found: %s\n", path)
		os.Exit(1)
	}
	cfg, err := config.LoadRaw(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}
	key := positional[0]
	field, err := config.LookupScalar(cfg, key)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if !config.IsScalar(field.Kind()) {
		fmt.Fprintf(os.Stderr, "Error: %s is not a scalar setting (tables and arrays are edited in the file)\n", key)
		os.Exit(1)
	}
	literal, err := config.ScalarLiteral(field.Kind(), positional[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s: %v\n", key, err)
		os.Exit(1)
	}
	if err := config.SetScalarInFile(path, key, literal); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Re-read so the printed value is what the next start will actually use.
	reloaded, err := config.LoadRaw(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reloading config: %v\n", err)
		os.Exit(1)
	}
	value, err := config.LookupScalar(reloaded, key)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("%s = %s (%s)\n", key, config.ScalarString(value), path)
	fmt.Println("running instance keeps the old value until `omp-lark-bridge daemon restart`")
}

func runConfigFormat(args []string) {
	fs := flag.NewFlagSet("config format", flag.ExitOnError)
	configPath := fs.String("config", "", "path to config file (default: auto-detect)")
	_ = fs.Parse(args)

	path := resolveConfigPath(*configPath)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "Config file not found: %s\n", path)
		os.Exit(1)
	}

	if err := config.FormatConfigFile(path); err != nil {
		fmt.Fprintf(os.Stderr, "Error formatting config: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Formatted %s\n", path)
}

func printConfigUsage() {
	fmt.Fprintf(os.Stderr, `Usage: omp-lark-bridge config <subcommand>

Subcommands:
  example    Print a complete annotated config.toml example
  format     Format the config file (alias: fmt)
  path       Print the resolved config file path
  get        Print one scalar setting (e.g. management.token)
  set        Write one scalar setting, keeping comments/formatting

Flags for 'format', 'get' and 'set':
  --config <path>   Path to config file (default: auto-detect)

Examples:
  omp-lark-bridge config example              Print example config
  omp-lark-bridge config example > config.toml  Save example config
  omp-lark-bridge config format               Format default config file
  omp-lark-bridge config fmt --config /path/to/config.toml
  omp-lark-bridge config get management.port  Print one setting
  omp-lark-bridge config set management.token 1   Set the dashboard token
`)
}
