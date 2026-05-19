package service

import (
	"strings"
	"testing"
)

func TestLoadDefaultSpecExpandsServicePaths(t *testing.T) {
	spec, err := LoadDefaultSpec(Vars{
		"app_root":        "/Users/me/.personal-mcp-server",
		"install_bin":     "/Users/me/.personal-mcp-server/bin/personal-mcp-server",
		"config_file":     "/Users/me/.personal-mcp-server/config/config.toml",
		"home":            "/Users/me",
		"user_config_dir": "/Users/me/.config",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := spec.Service.Label, "com.noumenalabs.personal-mcp-server"; got != want {
		t.Fatalf("label = %q, want %q", got, want)
	}
	if got, want := spec.Paths.LogsDir, "/Users/me/.personal-mcp-server/logs"; got != want {
		t.Fatalf("logs dir = %q, want %q", got, want)
	}
	if got, want := spec.Platforms["darwin"].Backend, "launchd-user"; got != want {
		t.Fatalf("darwin backend = %q, want %q", got, want)
	}
}

func TestLaunchAgentPlistUsesSpecValues(t *testing.T) {
	spec, err := LoadDefaultSpec(Vars{
		"app_root":        "/Users/me/.personal-mcp-server",
		"install_bin":     "/Users/me/.personal-mcp-server/bin/personal & local",
		"config_file":     "/Users/me/.personal-mcp-server/config/config dev.toml",
		"home":            "/Users/me",
		"user_config_dir": "/Users/me/.config",
	})
	if err != nil {
		t.Fatal(err)
	}
	plist := LaunchAgentPlist(spec)
	for _, want := range []string{"com.noumenalabs.personal-mcp-server", "/Users/me/.personal-mcp-server/bin/personal &amp; local", "/Users/me/.personal-mcp-server/logs/stdout.log", "SuccessfulExit"} {
		if !strings.Contains(plist, want) {
			t.Fatalf("plist missing %q in:\n%s", want, plist)
		}
	}
}

func TestSystemdUserUnitUsesSpecValues(t *testing.T) {
	spec, err := LoadDefaultSpec(Vars{
		"app_root":        "/home/me/.personal-mcp-server",
		"install_bin":     "/home/me/.personal-mcp-server/bin/personal mcp",
		"config_file":     "/home/me/.personal-mcp-server/config/config dev.toml",
		"home":            "/home/me",
		"user_config_dir": "/home/me/.config",
	})
	if err != nil {
		t.Fatal(err)
	}
	unit := SystemdUserUnit(spec)
	want := `ExecStart="/home/me/.personal-mcp-server/bin/personal mcp" serve --config "/home/me/.personal-mcp-server/config/config dev.toml"`
	if !strings.Contains(unit, want) {
		t.Fatalf("unit missing quoted ExecStart %q in:\n%s", want, unit)
	}
	if !strings.Contains(unit, `StandardOutput=append:/home/me/.personal-mcp-server/logs/stdout.log`) {
		t.Fatalf("unit missing stdout path:\n%s", unit)
	}
}
