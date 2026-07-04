package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

const defaultClientTimeout = 30 * time.Second

type clientFileConfig struct {
	Server struct {
		Host          string `toml:"host"`
		Port          int    `toml:"port"`
		Endpoint      string `toml:"endpoint"`
		AuthTokenEnv  string `toml:"auth_token_env"`
		AuthTokenFile string `toml:"auth_token_file"`
	} `toml:"server"`
}

type mcpClientConfig struct {
	URL        string
	HostHeader string
	Token      string
	Timeout    time.Duration
}

type mcpCLIClient struct {
	cfg    mcpClientConfig
	http   *http.Client
	nextID int
}

func clientCommand(args []string) {
	if len(args) > 0 && isHelpArg(args[0]) {
		printClientHelp()
		return
	}
	fs := flag.NewFlagSet("client", flag.ExitOnError)
	configPath := fs.String("config", "", "path to TOML config; defaults to $PERSONAL_MCP_ROOT/config/config.toml when present")
	configDir := fs.String("config-dir", defaultConfigDir(), "config directory used for default config/token discovery")
	endpointURL := fs.String("url", "", "explicit MCP endpoint URL, for example http://127.0.0.1:3929/mcp")
	token := fs.String("token", "", "bearer token; overrides token files and configured auth_token_env")
	timeout := fs.Duration("timeout", defaultClientTimeout, "HTTP client timeout")
	_ = fs.Parse(args)

	rest := fs.Args()
	subcmd := "ping"
	if len(rest) > 0 {
		subcmd = rest[0]
		rest = rest[1:]
	}
	if isHelpArg(subcmd) {
		printClientHelp()
		return
	}
	if subcmd == "run-named" && len(rest) > 0 && isHelpArg(rest[0]) {
		printClientRunNamedHelp()
		return
	}

	cfg, err := loadMCPClientConfig(*configPath, *configDir, *endpointURL, *token, *timeout)
	if err != nil {
		logFatalClient(err)
	}
	client := &mcpCLIClient{cfg: cfg, http: &http.Client{Timeout: cfg.Timeout}, nextID: 1}

	switch subcmd {
	case "ping":
		clientPing(client, rest)
	case "tools":
		clientTools(client, rest)
	case "call":
		clientCall(client, rest)
	case "run-named":
		clientRunNamed(client, rest)
	case "raw":
		clientRaw(client, rest)
	default:
		printClientHelp()
		os.Exit(2)
	}
}

func printClientHelp() {
	fmt.Print(`usage:
  personal-mcp-server client [GLOBAL FLAGS] ping
  personal-mcp-server client [GLOBAL FLAGS] tools
  personal-mcp-server client [GLOBAL FLAGS] call TOOL_NAME [JSON_ARGS]
  personal-mcp-server client [GLOBAL FLAGS] run-named [--cwd DIR] [--extra-arg ARG ...] NAME
  personal-mcp-server client [GLOBAL FLAGS] raw METHOD [JSON_PARAMS]

Global flags:
  --config PATH       TOML config path; defaults to the configured user location when present.
  --config-dir DIR    Config directory used for default config/token discovery.
  --url URL           Explicit MCP endpoint URL.
  --token TOKEN       Bearer token override.
  --timeout DURATION  HTTP client timeout.

Commands:
  ping        Send initialize and print the JSON response.
  tools       List MCP tools.
  call        Call one MCP tool with optional JSON arguments.
  run-named   Run one configured named command through cmd_run_named.
  raw         Send one raw JSON-RPC method with optional JSON params.

Examples:
  personal-mcp-server client tools
  personal-mcp-server client call server_info '{}'
  personal-mcp-server client run-named --cwd . --extra-arg -k --extra-arg TestFoo test
`)
}

func clientPing(client *mcpCLIClient, args []string) {
	fs := flag.NewFlagSet("client ping", flag.ExitOnError)
	_ = fs.Parse(args)
	if fs.NArg() != 0 {
		log.Fatal("client ping accepts no arguments")
	}
	result, err := client.request("initialize", map[string]any{})
	if err != nil {
		logFatalClient(err)
	}
	printJSONValue(result)
}

func clientTools(client *mcpCLIClient, args []string) {
	fs := flag.NewFlagSet("client tools", flag.ExitOnError)
	_ = fs.Parse(args)
	if fs.NArg() != 0 {
		log.Fatal("client tools accepts no arguments")
	}
	result, err := client.request("tools/list", map[string]any{})
	if err != nil {
		logFatalClient(err)
	}
	printJSONValue(result)
}

func clientCall(client *mcpCLIClient, args []string) {
	fs := flag.NewFlagSet("client call", flag.ExitOnError)
	_ = fs.Parse(args)
	if fs.NArg() < 1 || fs.NArg() > 2 {
		log.Fatal("client call requires TOOL_NAME and optional JSON_ARGS")
	}
	toolName := fs.Arg(0)
	arguments := map[string]any{}
	if fs.NArg() == 2 {
		var err error
		arguments, err = parseJSONObject(fs.Arg(1), "JSON_ARGS")
		if err != nil {
			logFatalClient(err)
		}
	}
	result, err := client.callTool(toolName, arguments)
	if err != nil {
		logFatalClient(err)
	}
	printJSONValue(result)
}

func clientRunNamed(client *mcpCLIClient, args []string) {
	if len(args) > 0 && isHelpArg(args[0]) {
		printClientRunNamedHelp()
		return
	}
	parsed, err := parseRunNamedArgs(args)
	if err != nil {
		log.Fatal(err)
	}
	arguments := map[string]any{"name": parsed.name}
	if parsed.cwd != "" {
		arguments["cwd"] = parsed.cwd
	}
	if len(parsed.extraArgs) > 0 {
		arguments["extra_args"] = parsed.extraArgs
	}
	result, err := client.callTool("cmd_run_named", arguments)
	if err != nil {
		logFatalClient(err)
	}
	printJSONValue(result)
}

type runNamedArgs struct {
	name      string
	cwd       string
	extraArgs []string
}

func printClientRunNamedHelp() {
	fmt.Print(`usage:
  personal-mcp-server client [GLOBAL FLAGS] run-named [--cwd DIR] [--extra-arg ARG ...] NAME

Runs one configured named command through the cmd_run_named MCP tool.

Flags:
  --cwd DIR          Project directory used for trusted project command discovery.
  --extra-arg ARG    Add one constrained extra argument. Repeat for multiple args.

Examples:
  personal-mcp-server client run-named test
  personal-mcp-server client run-named --cwd ~/src/project --extra-arg -k --extra-arg TestFoo pytest
`)
}

func parseRunNamedArgs(args []string) (runNamedArgs, error) {
	parsed := runNamedArgs{extraArgs: []string{}}
	remaining := append([]string(nil), args...)
	for len(remaining) > 0 {
		arg := remaining[0]
		remaining = remaining[1:]
		switch {
		case arg == "--cwd":
			if len(remaining) == 0 {
				return runNamedArgs{}, fmt.Errorf("client run-named --cwd requires a value")
			}
			parsed.cwd = remaining[0]
			remaining = remaining[1:]
		case strings.HasPrefix(arg, "--cwd="):
			parsed.cwd = strings.TrimPrefix(arg, "--cwd=")
		case arg == "--extra-arg":
			if len(remaining) == 0 {
				return runNamedArgs{}, fmt.Errorf("client run-named --extra-arg requires a value")
			}
			parsed.extraArgs = append(parsed.extraArgs, remaining[0])
			remaining = remaining[1:]
		case strings.HasPrefix(arg, "--extra-arg="):
			parsed.extraArgs = append(parsed.extraArgs, strings.TrimPrefix(arg, "--extra-arg="))
		case strings.HasPrefix(arg, "-"):
			return runNamedArgs{}, fmt.Errorf("unknown client run-named flag %q", arg)
		default:
			if parsed.name != "" {
				return runNamedArgs{}, fmt.Errorf("client run-named requires exactly one command name")
			}
			parsed.name = arg
		}
	}
	if parsed.name == "" {
		return runNamedArgs{}, fmt.Errorf("client run-named requires exactly one command name")
	}
	return parsed, nil
}

func clientRaw(client *mcpCLIClient, args []string) {
	fs := flag.NewFlagSet("client raw", flag.ExitOnError)
	_ = fs.Parse(args)
	if fs.NArg() < 1 || fs.NArg() > 2 {
		log.Fatal("client raw requires METHOD and optional JSON_PARAMS")
	}
	params := map[string]any{}
	if fs.NArg() == 2 {
		var err error
		params, err = parseJSONObject(fs.Arg(1), "JSON_PARAMS")
		if err != nil {
			logFatalClient(err)
		}
	}
	result, err := client.request(fs.Arg(0), params)
	if err != nil {
		logFatalClient(err)
	}
	printJSONValue(result)
}

func loadMCPClientConfig(configPath, configDir, endpointURL, tokenOverride string, timeout time.Duration) (mcpClientConfig, error) {
	if timeout <= 0 {
		timeout = defaultClientTimeout
	}
	cfg := mcpClientConfig{Timeout: timeout}
	configDirPath := expandUserPath(configDir)
	if configDirPath == "" {
		configDirPath = defaultConfigDir()
	}
	resolvedConfigPath, err := resolveClientConfigPath(configPath, configDirPath)
	if err != nil {
		return cfg, err
	}

	fileCfg := clientFileConfig{}
	serverHost := "127.0.0.1"
	serverPort := 3929
	serverEndpoint := "/mcp"
	if resolvedConfigPath != "" {
		b, readErr := os.ReadFile(resolvedConfigPath) //nolint:gosec // local user-selected client config.
		if readErr != nil {
			return cfg, readErr
		}
		if err := toml.Unmarshal(b, &fileCfg); err != nil {
			return cfg, fmt.Errorf("parse config: %w", err)
		}
		if fileCfg.Server.Host != "" {
			serverHost = fileCfg.Server.Host
		}
		if fileCfg.Server.Port > 0 {
			serverPort = fileCfg.Server.Port
		}
		if fileCfg.Server.Endpoint != "" {
			serverEndpoint = fileCfg.Server.Endpoint
		}
		configDirPath = filepath.Dir(resolvedConfigPath)
	}
	if !strings.HasPrefix(serverEndpoint, "/") {
		serverEndpoint = "/" + serverEndpoint
	}
	cfg.HostHeader = net.JoinHostPort(serverHost, fmt.Sprintf("%d", serverPort))
	cfg.URL = "http://" + cfg.HostHeader + serverEndpoint

	if endpointURL != "" {
		parsed, parseErr := url.Parse(endpointURL)
		if parseErr != nil {
			return cfg, parseErr
		}
		if parsed.Scheme != "http" {
			return cfg, fmt.Errorf("client only supports http:// localhost MCP endpoints")
		}
		if parsed.Host == "" || parsed.Port() == "" {
			return cfg, fmt.Errorf("--url must include host and port")
		}
		if parsed.Path == "" {
			parsed.Path = serverEndpoint
		}
		cfg.URL = parsed.String()
		cfg.HostHeader = parsed.Host
	}
	if err := validateLocalClientURL(cfg.URL); err != nil {
		return cfg, err
	}

	token := strings.TrimSpace(tokenOverride)
	if token == "" {
		token = discoverClientToken(fileCfg, configDirPath)
	}
	if token == "" {
		return cfg, fmt.Errorf("missing token; pass --token, configure server.auth_token_file, or create %s", filepath.Join(configDirPath, "token"))
	}
	cfg.Token = token
	return cfg, nil
}

func resolveClientConfigPath(configPath, configDir string) (string, error) {
	if strings.TrimSpace(configPath) != "" {
		path := expandUserPath(configPath)
		if _, err := os.Stat(path); err != nil {
			return "", fmt.Errorf("config not found: %s", path)
		}
		return path, nil
	}
	path := filepath.Join(configDir, "config.toml")
	if _, err := os.Stat(path); err == nil {
		return path, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	return "", nil
}

func discoverClientToken(fileCfg clientFileConfig, configDir string) string {
	if fileCfg.Server.AuthTokenFile != "" {
		if token := readClientTokenFile(expandUserPath(fileCfg.Server.AuthTokenFile)); token != "" {
			return token
		}
	}
	if token := readClientTokenFile(filepath.Join(configDir, "token")); token != "" {
		return token
	}
	if fileCfg.Server.AuthTokenEnv != "" {
		return strings.TrimSpace(os.Getenv(fileCfg.Server.AuthTokenEnv))
	}
	return ""
}

func readClientTokenFile(path string) string {
	b, err := os.ReadFile(path) //nolint:gosec // local user-selected token path.
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func validateLocalClientURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return err
	}
	host := parsed.Hostname()
	if host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return nil
	}
	return fmt.Errorf("client refuses non-local MCP endpoint host %q", host)
}

func (c *mcpCLIClient) request(method string, params map[string]any) (any, error) {
	payload := map[string]any{"jsonrpc": "2.0", "method": method}
	if !strings.HasPrefix(method, "notifications/") {
		payload["id"] = c.nextID
		c.nextID++
	}
	if params != nil {
		payload["params"] = params
	}
	return c.post(payload)
}

func (c *mcpCLIClient) callTool(name string, arguments map[string]any) (any, error) {
	return c.request("tools/call", map[string]any{"name": name, "arguments": arguments})
}

func (c *mcpCLIClient) post(payload map[string]any) (any, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, c.cfg.URL, bytes.NewReader(body)) // #nosec G107 -- URL is validated to localhost by loadMCPClientConfig.
	if err != nil {
		return nil, err
	}
	if c.cfg.HostHeader != "" {
		req.Host = c.cfg.HostHeader
	}
	req.Header.Set("Authorization", "Bearer "+c.cfg.Token)
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req) // #nosec G107 -- request URL is validated to localhost by loadMCPClientConfig.
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d %s: %s", resp.StatusCode, resp.Status, strings.TrimSpace(string(respBody)))
	}
	if strings.TrimSpace(string(respBody)) == "" {
		return map[string]any{"ok": true}, nil
	}
	var decoded any
	if err := json.Unmarshal(respBody, &decoded); err != nil {
		return nil, fmt.Errorf("non-JSON response: %s", strings.TrimSpace(string(respBody)))
	}
	if obj, ok := decoded.(map[string]any); ok {
		if rpcErr, exists := obj["error"]; exists {
			b, marshalErr := json.MarshalIndent(rpcErr, "", "  ")
			if marshalErr != nil {
				return nil, fmt.Errorf("MCP error: %v", rpcErr)
			}
			return nil, fmt.Errorf("MCP error: %s", string(b))
		}
	}
	return decoded, nil
}

func parseJSONObject(raw, label string) (map[string]any, error) {
	var value map[string]any
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return nil, fmt.Errorf("%s must be valid JSON object: %w", label, err)
	}
	if value == nil {
		return nil, fmt.Errorf("%s must be a JSON object", label)
	}
	return value, nil
}

func printJSONValue(value any) {
	out, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		logFatalClient(err)
	}
	fmt.Println(string(out))
}

func expandUserPath(path string) string {
	if path == "" {
		return ""
	}
	path = os.ExpandEnv(path)
	if path == "~" {
		home, err := os.UserHomeDir()
		if err == nil {
			return home
		}
	}
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}

func logFatalClient(err error) {
	log.Fatalf("client: %v", err)
}
