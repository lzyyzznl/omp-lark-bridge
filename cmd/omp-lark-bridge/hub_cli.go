package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// runHubCommand implements `omp-lark-bridge hub pair <server_url> <code>`.
// The center's one-time code is exchanged for the shared hub secret and the
// device token, which are written to a root-only state file. The OLB then
// keeps an outbound tunnel to the center; it never needs an inbound port.
func runHubCommand(args []string) {
	if len(args) == 0 || args[0] != "pair" {
		fmt.Fprintln(os.Stderr, "Usage: omp-lark-bridge hub pair <server_url> <code> [--name NAME] [--state-file PATH]")
		os.Exit(2)
	}
	if len(args) < 3 {
		fmt.Fprintln(os.Stderr, "Usage: omp-lark-bridge hub pair <server_url> <code> [--name NAME] [--state-file PATH]")
		os.Exit(2)
	}
	serverURL := strings.TrimRight(strings.TrimSpace(args[1]), "/")
	code := strings.ToUpper(strings.TrimSpace(args[2]))
	if serverURL == "" || code == "" {
		fmt.Fprintln(os.Stderr, "server_url and code are required")
		os.Exit(2)
	}

	fs := flag.NewFlagSet("hub pair", flag.ContinueOnError)
	name := fs.String("name", "", "device display name")
	stateFile := fs.String("state-file", defaultHubStateFile(), "pairing state file")
	if err := fs.Parse(args[3:]); err != nil {
		os.Exit(2)
	}

	payload, _ := json.Marshal(map[string]string{"code": code, "name": *name})
	req, err := http.NewRequest(http.MethodPost, serverURL+"/api/v1/hub/pair/claim", bytes.NewReader(payload))
	if err != nil {
		fmt.Fprintf(os.Stderr, "pair: build request: %v\n", err)
		os.Exit(1)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pair: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "pair failed: %s: %s\n", resp.Status, strings.TrimSpace(string(body)))
		os.Exit(1)
	}
	var envelope struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
		Data  struct {
			DeviceToken string `json:"device_token"`
			Name        string `json:"name"`
			Secret      string `json:"secret"`
			Host        string `json:"host"`
			WebURL      string `json:"web_url"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		fmt.Fprintf(os.Stderr, "pair: decode response: %v\n", err)
		os.Exit(1)
	}
	if !envelope.OK {
		fmt.Fprintf(os.Stderr, "pair failed: %s\n", envelope.Error)
		os.Exit(1)
	}
	out := envelope.Data
	if out.DeviceToken == "" || out.Secret == "" {
		fmt.Fprintln(os.Stderr, "pair: center returned an incomplete response")
		os.Exit(1)
	}
	if *stateFile == "" {
		*stateFile = defaultHubStateFile()
	}
	st := map[string]string{
		"server_url":   serverURL,
		"secret":       out.Secret,
		"device_token": out.DeviceToken,
		"device_name":  out.Name,
		"web_url":      out.WebURL,
	}
	b, _ := json.MarshalIndent(st, "", "  ")
	b = append(b, '\n')
	if dir := filepath.Dir(*stateFile); dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			fmt.Fprintf(os.Stderr, "pair: create state dir: %v\n", err)
			os.Exit(1)
		}
	}
	if err := os.WriteFile(*stateFile, b, 0o600); err != nil {
		fmt.Fprintf(os.Stderr, "pair: write state: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("paired %s (%s)\n", out.DeviceToken, out.Name)
	fmt.Printf("web:   %s\n", out.WebURL)
	fmt.Printf("state: %s\n", *stateFile)
	fmt.Println()
	fmt.Println("Add this to the node config.toml, then restart the service:")
	fmt.Println()
	fmt.Println("[hub]")
	fmt.Println("enabled = true")
	fmt.Println(`mode = "client"`)
	fmt.Printf("server_url = %q\n", serverURL)
	fmt.Printf("state_file = %q\n", *stateFile)
	fmt.Println()
	fmt.Println("[ompweb]")
	fmt.Printf("public_url = %q\n", out.WebURL)
}

func defaultHubStateFile() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "hub-client.json"
	}
	return filepath.Join(home, ".omp-lark-bridge", "hub-client.json")
}
