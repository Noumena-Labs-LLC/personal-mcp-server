package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/noumena-labs-llc/personal-mcp-server/internal/config"
)

func approvalsCommand(args []string) {
	if len(args) == 0 || isHelpArg(args[0]) {
		printCommandHelp("approvals")
		return
	}
	if len(args) > 1 && isHelpArg(args[1]) {
		if !printCommandHelp("approvals " + args[0]) {
			printCommandHelp("approvals")
		}
		return
	}
	fs := flag.NewFlagSet("approvals", flag.ExitOnError)
	configPath := fs.String("config", defaultConfigPath(), "path to global TOML config")
	interval := fs.Duration("interval", 2*time.Second, "watch polling interval")
	_ = fs.Parse(args[1:])

	client, err := newApprovalClient(*configPath)
	if err != nil {
		log.Fatal(err)
	}
	switch args[0] {
	case "list":
		if err := client.list(); err != nil {
			log.Fatal(err)
		}
	case "watch":
		if err := client.watch(*interval); err != nil {
			log.Fatal(err)
		}
	case "approve":
		remaining := fs.Args()
		if len(remaining) != 1 {
			log.Fatal("approvals approve requires exactly one approval ID")
		}
		if err := client.decide(remaining[0], "approve"); err != nil {
			log.Fatal(err)
		}
	case "deny":
		remaining := fs.Args()
		if len(remaining) != 1 {
			log.Fatal("approvals deny requires exactly one approval ID")
		}
		if err := client.decide(remaining[0], "deny"); err != nil {
			log.Fatal(err)
		}
	default:
		usage()
		osExit2()
	}
}

type approvalClient struct {
	baseURL string
	token   string
	client  *http.Client
}

func newApprovalClient(configPath string) (*approvalClient, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, err
	}
	token := cfg.AuthToken()
	if token == "" {
		return nil, fmt.Errorf("auth token is empty; set %s or configure server.auth_token_file", cfg.Server.AuthTokenEnv)
	}
	listenAddr := cfg.ListenAddr()
	if err := validateLocalApprovalAddr(listenAddr); err != nil {
		return nil, err
	}
	return &approvalClient{
		baseURL: fmt.Sprintf("http://%s", listenAddr),
		token:   token,
		client:  &http.Client{Timeout: 10 * time.Second},
	}, nil
}

func validateLocalApprovalAddr(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("invalid configured server listen address %q: %w", addr, err)
	}
	if host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return nil
	}
	return fmt.Errorf("approval CLI refuses non-local server address %q", addr)
}

func (c *approvalClient) list() error {
	body, err := c.do(http.MethodGet, "/approvals")
	if err != nil {
		return err
	}
	return printPrettyJSON(body)
}

func (c *approvalClient) watch(interval time.Duration) error {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	if err := c.list(); err != nil {
		return err
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for range ticker.C {
		if err := c.list(); err != nil {
			return err
		}
	}
	return nil
}

func (c *approvalClient) decide(id, decision string) error {
	body, err := c.do(http.MethodPost, "/approvals/" + url.PathEscape(id) + "/" + decision)
	if err != nil {
		return err
	}
	return printPrettyJSON(body)
}

func (c *approvalClient) do(method, reqPath string) ([]byte, error) {
	endpoint, err := url.Parse(c.baseURL)
	if err != nil {
		return nil, err
	}
	decodedPath, err := url.PathUnescape(reqPath)
	if err != nil {
		return nil, err
	}
	endpoint.Path = decodedPath
	if decodedPath != reqPath {
		endpoint.RawPath = reqPath
	}
	req, err := http.NewRequestWithContext(context.Background(), method, endpoint.String(), http.NoBody) // #nosec G704 -- approval CLI only contacts the configured local personal-mcp-server server.
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.client.Do(req) // #nosec G704 -- request URL is derived from validated local server config.
	if err != nil {
		return nil, err
	}
	body, readErr := io.ReadAll(resp.Body)
	closeErr := resp.Body.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s %s failed: HTTP %d: %s", method, reqPath, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return body, nil
}

func printPrettyJSON(body []byte) error {
	if !json.Valid(body) {
		fmt.Println(string(body))
		return nil
	}
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		return err
	}
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(out))
	return nil
}
