package services

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestImportServiceParseMCPJSON_HTTPWithHeaders(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	ms := NewMCPService()
	is := NewImportService(nil, ms)

	input := `{
		"mcpServers": {
			"zread": {
				"type": "http",
				"url": "https://open.bigmodel.cn/api/mcp/zread/mcp",
				"headers": { "Authorization": "Bearer your_api_key" }
			}
		}
	}`

	result, err := is.ParseMCPJSON(input)
	if err != nil {
		t.Fatalf("ParseMCPJSON: %v", err)
	}
	if result == nil || len(result.Servers) != 1 {
		t.Fatalf("expected 1 server, got %#v", result)
	}

	server := result.Servers[0]
	if server.Name != "zread" {
		t.Fatalf("expected name zread, got %q", server.Name)
	}
	if server.Type != "http" {
		t.Fatalf("expected type http, got %q", server.Type)
	}
	if server.URL != "https://open.bigmodel.cn/api/mcp/zread/mcp" {
		t.Fatalf("unexpected url: %q", server.URL)
	}
	if server.Headers["Authorization"] != "Bearer your_api_key" {
		t.Fatalf("expected Authorization header to be preserved, got %#v", server.Headers)
	}
	if len(server.EnablePlatform) != 1 || server.EnablePlatform[0] != platClaudeCode {
		t.Fatalf("expected enable_platform to default to %q, got %#v", platClaudeCode, server.EnablePlatform)
	}
}

func TestImportServiceParseMCPJSON_StartupTimeoutAcceptsString(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	ms := NewMCPService()
	is := NewImportService(nil, ms)

	input := `{
		"mcpServers": {
			"local-server": {
				"command": "npx",
				"args": ["-y", "@modelcontextprotocol/server-xxx"],
				"startup_timeout_sec": "30"
			}
		}
	}`

	result, err := is.ParseMCPJSON(input)
	if err != nil {
		t.Fatalf("ParseMCPJSON: %v", err)
	}
	if result == nil || len(result.Servers) != 1 {
		t.Fatalf("expected 1 server, got %#v", result)
	}
	if got := result.Servers[0].StartupTimeoutSec; got != 30 {
		t.Fatalf("expected startup_timeout_sec=30, got %d", got)
	}
}

func TestImportServiceParseMCPJSON_HTTPUrlAlias(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	ms := NewMCPService()
	is := NewImportService(nil, ms)

	input := `{
		"mcpServers": {
			"remote-server": {
				"httpUrl": "https://example.com/mcp",
				"headers": { "Authorization": "Bearer X" }
			}
		}
	}`

	result, err := is.ParseMCPJSON(input)
	if err != nil {
		t.Fatalf("ParseMCPJSON: %v", err)
	}
	if result == nil || len(result.Servers) != 1 {
		t.Fatalf("expected 1 server, got %#v", result)
	}
	server := result.Servers[0]
	if server.Type != "http" {
		t.Fatalf("expected type http, got %q", server.Type)
	}
	if server.URL != "https://example.com/mcp" {
		t.Fatalf("expected url to be taken from httpUrl, got %q", server.URL)
	}
	if server.Headers["Authorization"] != "Bearer X" {
		t.Fatalf("expected Authorization header to be preserved, got %#v", server.Headers)
	}
}

func TestMCPServiceSaveServers_SyncCodexKeepsHeadersAndStartupTimeout(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	ms := NewMCPService()
	servers := []MCPServer{
		{
			Name:           "remote",
			Type:           "http",
			URL:            "https://example.com/mcp",
			Headers:        map[string]string{"Authorization": "Bearer X"},
			EnablePlatform: []string{platCodex},
		},
		{
			Name:              "local",
			Type:              "stdio",
			Command:           "npx",
			Args:              []string{"-y", "@modelcontextprotocol/server-xxx"},
			StartupTimeoutSec: 42,
			EnablePlatform:    []string{platCodex},
		},
	}

	if err := ms.SaveServers(servers); err != nil {
		t.Fatalf("SaveServers: %v", err)
	}

	path := filepath.Join(tmpHome, codexDirName, codexConfigFile)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read codex config: %v", err)
	}
	content := string(data)

	if !strings.Contains(content, "[mcp_servers.remote]") {
		t.Fatalf("expected remote server table in codex config, got:\n%s", content)
	}
	if !strings.Contains(content, "[mcp_servers.remote.headers]") {
		t.Fatalf("expected remote headers table in codex config, got:\n%s", content)
	}
	if !strings.Contains(content, "Authorization = 'Bearer X'") {
		t.Fatalf("expected Authorization header in codex config, got:\n%s", content)
	}

	if !strings.Contains(content, "[mcp_servers.local]") {
		t.Fatalf("expected local server table in codex config, got:\n%s", content)
	}
	if !strings.Contains(content, "startup_timeout_sec = 42") {
		t.Fatalf("expected startup_timeout_sec in codex config, got:\n%s", content)
	}
}

