package services

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestMCPServiceSyncFromPlatformFiles_DisableCodexWhenDeletedExternally(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	storeDir := filepath.Join(tmpHome, ".code-switch")
	if err := os.MkdirAll(storeDir, 0o755); err != nil {
		t.Fatalf("mkdir store dir: %v", err)
	}
	storePath := filepath.Join(storeDir, "mcp.json")

	seed := mcpStorePayload{
		Servers: map[string]rawMCPServer{
			"foo": {
				Type:           "http",
				URL:            "https://example.com/mcp",
				EnablePlatform: []string{platCodex},
			},
		},
	}
	data, err := json.MarshalIndent(seed, "", "  ")
	if err != nil {
		t.Fatalf("marshal seed store: %v", err)
	}
	if err := os.WriteFile(storePath, data, 0o644); err != nil {
		t.Fatalf("write store: %v", err)
	}

	// Codex config exists but server is missing (simulates external deletion).
	codexDir := filepath.Join(tmpHome, codexDirName)
	if err := os.MkdirAll(codexDir, 0o755); err != nil {
		t.Fatalf("mkdir codex dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(codexDir, codexConfigFile), []byte(""), 0o644); err != nil {
		t.Fatalf("write codex config: %v", err)
	}

	ms := NewMCPService()
	servers, err := ms.SyncFromPlatformFiles()
	if err != nil {
		t.Fatalf("SyncFromPlatformFiles: %v", err)
	}
	var foo *MCPServer
	for i := range servers {
		if servers[i].Name == "foo" {
			foo = &servers[i]
			break
		}
	}
	if foo == nil {
		t.Fatalf("expected foo in result, got %#v", servers)
	}
	if got := foo.EnablePlatform; len(got) != 0 {
		t.Fatalf("expected codex to be disabled, got enable_platform=%#v", got)
	}

	// Store should be updated, so later SaveServers won't re-add it.
	saved, err := os.ReadFile(storePath)
	if err != nil {
		t.Fatalf("read store: %v", err)
	}
	var payload mcpStorePayload
	if err := json.Unmarshal(saved, &payload); err != nil {
		t.Fatalf("unmarshal store: %v", err)
	}
	if entry, ok := payload.Servers["foo"]; !ok {
		t.Fatalf("expected foo in store")
	} else if len(entry.EnablePlatform) != 0 {
		t.Fatalf("expected store enable_platform cleared, got %#v", entry.EnablePlatform)
	}
}

func TestMCPServiceSyncFromPlatformFiles_EnableCodexWhenAddedExternally(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	storeDir := filepath.Join(tmpHome, ".code-switch")
	if err := os.MkdirAll(storeDir, 0o755); err != nil {
		t.Fatalf("mkdir store dir: %v", err)
	}
	storePath := filepath.Join(storeDir, "mcp.json")

	seed := mcpStorePayload{
		Servers: map[string]rawMCPServer{
			"foo": {
				Type:           "http",
				URL:            "https://example.com/mcp",
				EnablePlatform: []string{},
			},
		},
	}
	data, err := json.MarshalIndent(seed, "", "  ")
	if err != nil {
		t.Fatalf("marshal seed store: %v", err)
	}
	if err := os.WriteFile(storePath, data, 0o644); err != nil {
		t.Fatalf("write store: %v", err)
	}

	// Codex config contains foo (simulates external add/enable).
	codexDir := filepath.Join(tmpHome, codexDirName)
	if err := os.MkdirAll(codexDir, 0o755); err != nil {
		t.Fatalf("mkdir codex dir: %v", err)
	}
	codexConfig := `
[mcp_servers.foo]
type = "http"
url = "https://example.com/mcp"
`
	if err := os.WriteFile(filepath.Join(codexDir, codexConfigFile), []byte(codexConfig), 0o644); err != nil {
		t.Fatalf("write codex config: %v", err)
	}

	ms := NewMCPService()
	servers, err := ms.SyncFromPlatformFiles()
	if err != nil {
		t.Fatalf("SyncFromPlatformFiles: %v", err)
	}
	var foo *MCPServer
	for i := range servers {
		if servers[i].Name == "foo" {
			foo = &servers[i]
			break
		}
	}
	if foo == nil {
		t.Fatalf("expected foo in result, got %#v", servers)
	}
	got := foo.EnablePlatform
	if len(got) != 1 || got[0] != platCodex {
		t.Fatalf("expected codex enabled, got enable_platform=%#v", got)
	}
}
