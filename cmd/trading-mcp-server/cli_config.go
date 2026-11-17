package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

// handleConfigCLI processes the "config" subcommand and its operations.
// Supports:
//   - config list [--group=X]            — list all params, optionally filtered by group
//   - config get <key> [--scope=pair:NEAR-USDT]  — get specific param value and metadata
//   - config history --key=X [--limit=N] — show change history for a param
//
// Wire in main.go's main(): if len(os.Args)>=2 && os.Args[1]=="config" { _ = handleConfigCLI(os.Args[2:]); return }
func handleConfigCLI(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("config: missing subcommand (list, get, history)")
	}

	subcommand := args[0]
	switch subcommand {
	case "list":
		return handleConfigList(args[1:])
	case "get":
		return handleConfigGet(args[1:])
	case "history":
		return handleConfigHistory(args[1:])
	default:
		return fmt.Errorf("config: unknown subcommand %q (valid: list, get, history)", subcommand)
	}
}

// handleConfigList prints a table of configuration parameters.
// Usage: config list [--group=risk|scalping|exchanges]
func handleConfigList(args []string) error {
	group := ""
	for _, arg := range args {
		if strings.HasPrefix(arg, "--group=") {
			group = strings.TrimPrefix(arg, "--group=")
		}
	}

	apiToken := os.Getenv("ADMIN_API_TOKEN")
	url := "http://localhost:10600/api/v1/config/list"
	if group != "" {
		url += "?group=" + group
	}

	resp, err := makeConfigRequest("GET", url, apiToken, nil)
	if err != nil {
		return fmt.Errorf("config list: request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("config list: read response failed: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("config list: server returned %d: %s", resp.StatusCode, string(body))
	}

	// Pretty-print JSON response
	var data interface{}
	if err := json.Unmarshal(body, &data); err != nil {
		return fmt.Errorf("config list: parse JSON failed: %w", err)
	}

	formatted, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("config list: format JSON failed: %w", err)
	}

	fmt.Println(string(formatted))
	return nil
}

// handleConfigGet retrieves a specific configuration parameter.
// Usage: config get <key> [--scope=pair:NEAR-USDT]
func handleConfigGet(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("config get: missing key argument")
	}

	key := args[0]
	scope := ""
	for i := 1; i < len(args); i++ {
		if strings.HasPrefix(args[i], "--scope=") {
			scope = strings.TrimPrefix(args[i], "--scope=")
		}
	}

	apiToken := os.Getenv("ADMIN_API_TOKEN")
	url := fmt.Sprintf("http://localhost:10600/api/v1/config/get/%s", key)
	if scope != "" {
		url += "?scope=" + scope
	}

	resp, err := makeConfigRequest("GET", url, apiToken, nil)
	if err != nil {
		return fmt.Errorf("config get: request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("config get: read response failed: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("config get: server returned %d: %s", resp.StatusCode, string(body))
	}

	// Pretty-print JSON response
	var data interface{}
	if err := json.Unmarshal(body, &data); err != nil {
		return fmt.Errorf("config get: parse JSON failed: %w", err)
	}

	formatted, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("config get: format JSON failed: %w", err)
	}

	fmt.Println(string(formatted))
	return nil
}

// handleConfigHistory retrieves the change history for a configuration parameter.
// Usage: config history --key=<key> [--limit=N]
func handleConfigHistory(args []string) error {
	key := ""
	limit := "50"

	for _, arg := range args {
		if strings.HasPrefix(arg, "--key=") {
			key = strings.TrimPrefix(arg, "--key=")
		}
		if strings.HasPrefix(arg, "--limit=") {
			limit = strings.TrimPrefix(arg, "--limit=")
		}
	}

	if key == "" {
		return fmt.Errorf("config history: --key is required")
	}

	apiToken := os.Getenv("ADMIN_API_TOKEN")
	url := fmt.Sprintf("http://localhost:10600/api/v1/config/history/%s?limit=%s", key, limit)

	resp, err := makeConfigRequest("GET", url, apiToken, nil)
	if err != nil {
		return fmt.Errorf("config history: request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("config history: read response failed: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("config history: server returned %d: %s", resp.StatusCode, string(body))
	}

	// Pretty-print JSON response
	var data interface{}
	if err := json.Unmarshal(body, &data); err != nil {
		return fmt.Errorf("config history: parse JSON failed: %w", err)
	}

	formatted, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("config history: format JSON failed: %w", err)
	}

	fmt.Println(string(formatted))
	return nil
}

// makeConfigRequest sends an HTTP request to the config API with proper headers.
// Reads ADMIN_API_TOKEN from environment and adds X-Admin-Token header.
func makeConfigRequest(method, url, token string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("X-Admin-Token", token)
	}

	client := &http.Client{}
	return client.Do(req)
}
