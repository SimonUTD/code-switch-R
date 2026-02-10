package services

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/pelletier/go-toml/v2"
)

const (
	mcpStoreDir      = ".code-switch"
	mcpStoreFile     = "mcp.json"
	claudeMcpFile    = ".claude.json"
	codexDirName     = ".codex"
	codexConfigFile  = "config.toml"
	geminiDirName    = ".gemini"
	geminiConfigFile = "settings.json"
	platClaudeCode   = "claude-code"
	platCodex        = "codex"
	platGemini       = "gemini"
)

var builtInServers = map[string]rawMCPServer{
	"reftools": {
		Type:    "http",
		URL:     "https://api.ref.tools/mcp?apiKey={apiKey}",
		Website: "https://ref.tools",
		Tips:    "Visit ref.tools to claim your API key.",
	},
	"chrome-devtools": {
		Type:    "stdio",
		Command: "npx",
		Args:    []string{"-y", "chrome-devtools-mcp@latest"},
		Tips:    "Needs Node.js. Run once to install dependencies.",
	},
}

var placeholderPattern = regexp.MustCompile(`\{([a-zA-Z0-9_]+)\}`)

type MCPService struct {
	mu sync.Mutex
}

func NewMCPService() *MCPService {
	return &MCPService{}
}

type MCPServer struct {
	Name                string            `json:"name"`
	Type                string            `json:"type"`
	Command             string            `json:"command,omitempty"`
	Args                []string          `json:"args,omitempty"`
	Cwd                 string            `json:"cwd,omitempty"`
	Env                 map[string]string `json:"env,omitempty"`
	URL                 string            `json:"url,omitempty"`
	Headers             map[string]string `json:"headers,omitempty"`
	StartupTimeoutSec   int               `json:"startup_timeout_sec,omitempty"`
	Website             string            `json:"website,omitempty"`
	Tips                string            `json:"tips,omitempty"`
	EnablePlatform      []string          `json:"enable_platform"`
	EnabledInClaude     bool              `json:"enabled_in_claude"`
	EnabledInCodex      bool              `json:"enabled_in_codex"`
	EnabledInGemini     bool              `json:"enabled_in_gemini"`
	MissingPlaceholders []string          `json:"missing_placeholders"`
}

type rawMCPServer struct {
	Type           string            `json:"type"`
	Command        string            `json:"command,omitempty"`
	Args           []string          `json:"args,omitempty"`
	Cwd            string            `json:"cwd,omitempty"`
	Env            map[string]string `json:"env,omitempty"`
	URL            string            `json:"url,omitempty"`
	Headers        map[string]string `json:"headers,omitempty"`
	StartupTimeoutSec int            `json:"startup_timeout_sec,omitempty"`
	Website        string            `json:"website,omitempty"`
	Tips           string            `json:"tips,omitempty"`
	EnablePlatform []string          `json:"enable_platform"`
}

type mcpStorePayload struct {
	Servers         map[string]rawMCPServer `json:"servers"`
	DeletedBuiltins []string                `json:"deletedBuiltins,omitempty"`
}

type claudeMcpFilePayload struct {
	Servers map[string]json.RawMessage `json:"mcpServers"`
}

type geminiMcpFilePayload struct {
	Servers map[string]json.RawMessage `json:"mcpServers"`
}

type codexMcpFilePayload struct {
	Servers map[string]map[string]any `toml:"mcp_servers"`
}

type claudeDesktopServer struct {
	Type    string            `json:"type,omitempty"`
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Cwd     string            `json:"cwd,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

func (ms *MCPService) ListServers() ([]MCPServer, error) {
	ms.mu.Lock()
	defer ms.mu.Unlock()

	config, err := ms.loadConfig()
	if err != nil {
		return nil, err
	}

	claudeEnabled := loadClaudeEnabledServers()
	codexEnabled := loadCodexEnabledServers()
	geminiEnabled := loadGeminiEnabledServers()

	names := make([]string, 0, len(config))
	for name := range config {
		names = append(names, name)
	}
	sort.Strings(names)

	servers := make([]MCPServer, 0, len(names))
	for _, name := range names {
		entry := config[name]
		typ := normalizeServerType(entry.Type)
		platforms := normalizePlatforms(entry.EnablePlatform)
		server := MCPServer{
			Name:            name,
			Type:            typ,
			Command:         strings.TrimSpace(entry.Command),
			Args:            cloneArgs(entry.Args),
			Cwd:             strings.TrimSpace(entry.Cwd),
			Env:             cloneEnv(entry.Env),
			URL:             strings.TrimSpace(entry.URL),
			Headers:         cloneStrMap(entry.Headers),
			StartupTimeoutSec: entry.StartupTimeoutSec,
			Website:         strings.TrimSpace(entry.Website),
			Tips:            strings.TrimSpace(entry.Tips),
			EnablePlatform:  platforms,
			EnabledInClaude: containsNormalized(claudeEnabled, name),
			EnabledInCodex:  containsNormalized(codexEnabled, name),
			EnabledInGemini: containsNormalized(geminiEnabled, name),
		}
		server.MissingPlaceholders = detectPlaceholders(
			server.URL,
			server.Command,
			server.Args,
			server.Cwd,
			server.Env,
			server.Headers,
		)
		servers = append(servers, server)
	}

	return servers, nil
}

func (ms *MCPService) SaveServers(servers []MCPServer) error {
	ms.mu.Lock()
	defer ms.mu.Unlock()

	normalized := make([]MCPServer, len(servers))
	raw := make(map[string]rawMCPServer, len(servers))
	for i := range servers {
		server := servers[i]
		name := strings.TrimSpace(server.Name)
		if name == "" {
			return fmt.Errorf("server name 不能为空")
		}
		typ := normalizeServerType(server.Type)
		platforms := normalizePlatforms(server.EnablePlatform)
		args := cleanArgs(server.Args)
		env := cleanEnv(server.Env)
		headers := cleanHeaders(server.Headers)
		command := strings.TrimSpace(server.Command)
		url := strings.TrimSpace(server.URL)
		cwd := strings.TrimSpace(server.Cwd)
		startupTimeoutSec := server.StartupTimeoutSec
		if startupTimeoutSec < 0 {
			return fmt.Errorf("%s startup_timeout_sec 不能为负数", name)
		}
		if typ == "stdio" && command == "" {
			return fmt.Errorf("%s 需要提供 command", name)
		}
		if typ == "http" && url == "" {
			return fmt.Errorf("%s 需要提供 url", name)
		}
		normalized[i] = MCPServer{
			Name:            name,
			Type:            typ,
			Command:         command,
			Args:            args,
			Cwd:             cwd,
			Env:             env,
			URL:             url,
			Headers:         headers,
			StartupTimeoutSec: startupTimeoutSec,
			Website:         strings.TrimSpace(server.Website),
			Tips:            strings.TrimSpace(server.Tips),
			EnablePlatform:  platforms,
			EnabledInClaude: server.EnabledInClaude,
			EnabledInCodex:  server.EnabledInCodex,
			EnabledInGemini: server.EnabledInGemini,
		}
		raw[name] = rawMCPServer{
			Type:           typ,
			Command:        command,
			Args:           args,
			Cwd:            cwd,
			Env:            env,
			URL:            url,
			Headers:        headers,
			StartupTimeoutSec: startupTimeoutSec,
			Website:        normalized[i].Website,
			Tips:           normalized[i].Tips,
			EnablePlatform: platforms,
		}
		placeholders := detectPlaceholders(url, command, args, cwd, env, headers)
		normalized[i].MissingPlaceholders = placeholders
		if len(placeholders) > 0 {
			normalized[i].EnablePlatform = []string{}
			rawEntry := raw[name]
			rawEntry.EnablePlatform = []string{}
			raw[name] = rawEntry
		}
	}

	// Calculate deletedBuiltins: built-in servers that are missing from raw
	var deletedBuiltins []string
	for builtInName := range builtInServers {
		if _, exists := raw[builtInName]; !exists {
			deletedBuiltins = append(deletedBuiltins, builtInName)
		}
	}
	sort.Strings(deletedBuiltins)

	if err := ms.saveStore(raw, deletedBuiltins); err != nil {
		return err
	}
	if err := ms.syncClaudeServers(normalized); err != nil {
		return err
	}
	if err := ms.syncCodexServers(normalized); err != nil {
		return err
	}
	if err := ms.syncGeminiServers(normalized); err != nil {
		return err
	}
	return nil
}

func (ms *MCPService) configPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, mcpStoreDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(dir, mcpStoreFile), nil
}

func (ms *MCPService) loadConfig() (map[string]rawMCPServer, error) {
	path, err := ms.configPath()
	if err != nil {
		return nil, err
	}

	servers := map[string]rawMCPServer{}
	var deletedBuiltins []string

	if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
		// Try parsing new format first (with servers and deletedBuiltins)
		var storePayload mcpStorePayload
		if err := json.Unmarshal(data, &storePayload); err == nil && storePayload.Servers != nil {
			servers = storePayload.Servers
			deletedBuiltins = storePayload.DeletedBuiltins
		} else {
			// Fall back to legacy flat format for backward compatibility
			if err := json.Unmarshal(data, &servers); err != nil {
				return nil, err
			}
		}
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	for name, entry := range servers {
		servers[name] = normalizeRawEntry(entry)
	}

	changed := false
	if imported, err := ms.importFromClaude(servers); err == nil {
		if ms.mergeImportedServers(servers, imported) {
			changed = true
		}
	} else {
		return nil, err
	}

	if imported, err := ms.importFromCodex(servers); err == nil {
		if ms.mergeImportedServers(servers, imported) {
			changed = true
		}
	} else {
		return nil, err
	}

	if imported, err := ms.importFromGemini(servers); err == nil {
		if ms.mergeImportedServers(servers, imported) {
			changed = true
		}
	} else {
		return nil, err
	}

	if ensureBuiltInServers(servers, deletedBuiltins) {
		changed = true
	}

	if changed {
		if err := ms.saveStore(servers, deletedBuiltins); err != nil {
			return servers, err
		}
	}

	return servers, nil
}

func (ms *MCPService) importFromClaude(existing map[string]rawMCPServer) (map[string]rawMCPServer, error) {
	path, err := claudeConfigPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]rawMCPServer{}, nil
		}
		return nil, err
	}
	if len(data) == 0 {
		return map[string]rawMCPServer{}, nil
	}
	var payload struct {
		Servers map[string]claudeDesktopServer `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	result := make(map[string]rawMCPServer, len(payload.Servers))
	for name, entry := range payload.Servers {
		trimmedName := strings.TrimSpace(name)
		if trimmedName == "" {
			continue
		}
		if _, exists := existing[trimmedName]; exists {
			continue
		}
		typeHint := entry.Type
		if strings.TrimSpace(typeHint) == "" {
			if strings.TrimSpace(entry.URL) != "" {
				typeHint = "http"
			}
		}
		if strings.TrimSpace(typeHint) == "" {
			typeHint = "stdio"
		}
		typ := normalizeServerType(typeHint)
		if typ == "http" && entry.URL == "" {
			continue
		}
		if typ == "stdio" && entry.Command == "" {
			continue
		}
		result[trimmedName] = rawMCPServer{
			Type:           typ,
			Command:        strings.TrimSpace(entry.Command),
			Args:           cleanArgs(entry.Args),
			Cwd:            strings.TrimSpace(entry.Cwd),
			Env:            cleanEnv(entry.Env),
			URL:            strings.TrimSpace(entry.URL),
			Headers:        cleanHeaders(entry.Headers),
			EnablePlatform: []string{platClaudeCode},
		}
	}
	return result, nil
}

func (ms *MCPService) importFromCodex(existing map[string]rawMCPServer) (map[string]rawMCPServer, error) {
	path, err := codexConfigPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]rawMCPServer{}, nil
		}
		return nil, err
	}
	if len(data) == 0 {
		return map[string]rawMCPServer{}, nil
	}

	var payload codexMcpFilePayload
	if err := toml.Unmarshal(data, &payload); err != nil {
		return nil, err
	}

	result := make(map[string]rawMCPServer, len(payload.Servers))
	for name, cfg := range payload.Servers {
		trimmedName := strings.TrimSpace(name)
		if trimmedName == "" {
			continue
		}
		if _, exists := existing[trimmedName]; exists {
			continue
		}

		typeHint := getString(cfg["type"])
		url := getString(cfg["url"])
		command := getString(cfg["command"])
		if strings.TrimSpace(typeHint) == "" {
			if strings.TrimSpace(url) != "" {
				typeHint = "http"
			} else {
				typeHint = "stdio"
			}
		}
		typ := normalizeServerType(typeHint)
		if typ == "http" && strings.TrimSpace(url) == "" {
			continue
		}
		if typ == "stdio" && strings.TrimSpace(command) == "" {
			continue
		}

		args := cleanArgs(getStringSlice(cfg["args"]))
		env := cleanEnv(getStringMap(cfg["env"]))
		headers := cleanHeaders(getStringMap(cfg["headers"]))
		cwd := strings.TrimSpace(getString(cfg["cwd"]))
		startupTimeoutSec := getInt(cfg["startup_timeout_sec"])
		if startupTimeoutSec < 0 {
			startupTimeoutSec = 0
		}

		result[trimmedName] = rawMCPServer{
			Type:             typ,
			Command:          strings.TrimSpace(command),
			Args:             args,
			Cwd:              cwd,
			Env:              env,
			URL:              strings.TrimSpace(url),
			Headers:          headers,
			StartupTimeoutSec: startupTimeoutSec,
			EnablePlatform:   []string{platCodex},
		}
	}

	return result, nil
}

func (ms *MCPService) importFromGemini(existing map[string]rawMCPServer) (map[string]rawMCPServer, error) {
	path, err := geminiConfigPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]rawMCPServer{}, nil
		}
		return nil, err
	}
	if len(data) == 0 {
		return map[string]rawMCPServer{}, nil
	}

	var payload geminiMcpFilePayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}

	result := make(map[string]rawMCPServer, len(payload.Servers))
	for name, raw := range payload.Servers {
		trimmedName := strings.TrimSpace(name)
		if trimmedName == "" {
			continue
		}
		if _, exists := existing[trimmedName]; exists {
			continue
		}

		var cfg map[string]any
		if err := json.Unmarshal(raw, &cfg); err != nil {
			continue
		}

		httpURL := getString(cfg["httpUrl"])
		url := getString(cfg["url"])
		if httpURL != "" {
			url = httpURL
		}

		typeHint := getString(cfg["type"])
		if strings.TrimSpace(typeHint) == "" {
			if strings.TrimSpace(url) != "" {
				typeHint = "http"
			} else {
				typeHint = "stdio"
			}
		}
		typ := normalizeServerType(typeHint)

		command := getString(cfg["command"])
		args := cleanArgs(getStringSlice(cfg["args"]))
		env := cleanEnv(getStringMap(cfg["env"]))
		headers := cleanHeaders(getStringMap(cfg["headers"]))
		cwd := strings.TrimSpace(getString(cfg["cwd"]))

		if typ == "http" && strings.TrimSpace(url) == "" {
			continue
		}
		if typ == "stdio" && strings.TrimSpace(command) == "" {
			continue
		}

		result[trimmedName] = rawMCPServer{
			Type:           typ,
			Command:        strings.TrimSpace(command),
			Args:           args,
			Cwd:            cwd,
			Env:            env,
			URL:            strings.TrimSpace(url),
			Headers:        headers,
			EnablePlatform: []string{platGemini},
		}
	}

	return result, nil
}

func (ms *MCPService) saveStore(servers map[string]rawMCPServer, deletedBuiltins []string) error {
	path, err := ms.configPath()
	if err != nil {
		return err
	}
	storePayload := mcpStorePayload{
		Servers:         servers,
		DeletedBuiltins: deletedBuiltins,
	}
	data, err := json.MarshalIndent(storePayload, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func normalizeServerType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "http", "sse", "streamable_http", "streamable-http":
		return "http"
	case "stdio":
		return "stdio"
	default:
		return "stdio"
	}
}

func normalizePlatforms(values []string) []string {
	seen := make(map[string]struct{})
	result := make([]string, 0, len(values))
	for _, raw := range values {
		if platform, ok := normalizePlatform(raw); ok {
			if _, exists := seen[platform]; exists {
				continue
			}
			seen[platform] = struct{}{}
			result = append(result, platform)
		}
	}
	return result
}

func normalizePlatform(value string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "claude", "claude_code", "claude-code":
		return "claude-code", true
	case "codex":
		return "codex", true
	case "gemini", "gemini-cli", "gemini_cli":
		return "gemini", true
	default:
		return "", false
	}
}

func unionPlatforms(primary, secondary []string) []string {
	combined := append([]string{}, primary...)
	combined = append(combined, secondary...)
	return normalizePlatforms(combined)
}

func normalizeRawEntry(entry rawMCPServer) rawMCPServer {
	entry.Type = normalizeServerType(entry.Type)
	entry.Command = strings.TrimSpace(entry.Command)
	entry.Cwd = strings.TrimSpace(entry.Cwd)
	entry.URL = strings.TrimSpace(entry.URL)
	entry.Website = strings.TrimSpace(entry.Website)
	entry.Tips = strings.TrimSpace(entry.Tips)
	entry.Args = cleanArgs(entry.Args)
	entry.Env = cleanEnv(entry.Env)
	entry.Headers = cleanHeaders(entry.Headers)
	entry.EnablePlatform = normalizePlatforms(entry.EnablePlatform)
	return entry
}

func cloneArgs(values []string) []string {
	if len(values) == 0 {
		return []string{}
	}
	dup := make([]string, len(values))
	copy(dup, values)
	return dup
}

func cloneEnv(values map[string]string) map[string]string {
	if len(values) == 0 {
		return map[string]string{}
	}
	dup := make(map[string]string, len(values))
	for k, v := range values {
		dup[k] = v
	}
	return dup
}

func cloneStrMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return map[string]string{}
	}
	dup := make(map[string]string, len(values))
	for k, v := range values {
		dup[k] = v
	}
	return dup
}

func cleanArgs(values []string) []string {
	if len(values) == 0 {
		return []string{}
	}
	result := make([]string, 0, len(values))
	for _, v := range values {
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			continue
		}
		result = append(result, trimmed)
	}
	return result
}

func cleanEnv(values map[string]string) map[string]string {
	if len(values) == 0 {
		return map[string]string{}
	}
	result := make(map[string]string, len(values))
	for key, value := range values {
		trimmedKey := strings.TrimSpace(key)
		if trimmedKey == "" {
			continue
		}
		result[trimmedKey] = strings.TrimSpace(value)
	}
	return result
}

func cleanHeaders(values map[string]string) map[string]string {
	if len(values) == 0 {
		return map[string]string{}
	}
	result := make(map[string]string, len(values))
	for key, value := range values {
		trimmedKey := strings.TrimSpace(key)
		if trimmedKey == "" {
			continue
		}
		result[trimmedKey] = strings.TrimSpace(value)
	}
	return result
}

func containsNormalized(pool map[string]struct{}, value string) bool {
	if len(pool) == 0 {
		return false
	}
	_, ok := pool[strings.ToLower(strings.TrimSpace(value))]
	return ok
}

func loadClaudeEnabledServers() map[string]struct{} {
	result := map[string]struct{}{}
	home, err := os.UserHomeDir()
	if err != nil {
		return result
	}
	path := filepath.Join(home, claudeMcpFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return result
	}
	var payload claudeMcpFilePayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return result
	}
	for name := range payload.Servers {
		result[strings.ToLower(strings.TrimSpace(name))] = struct{}{}
	}
	return result
}

func loadCodexEnabledServers() map[string]struct{} {
	result := map[string]struct{}{}
	home, err := os.UserHomeDir()
	if err != nil {
		return result
	}
	path := filepath.Join(home, codexDirName, codexConfigFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return result
	}
	var payload codexMcpFilePayload
	if err := toml.Unmarshal(data, &payload); err != nil {
		return result
	}
	for name := range payload.Servers {
		result[strings.ToLower(strings.TrimSpace(name))] = struct{}{}
	}
	return result
}

func loadGeminiEnabledServers() map[string]struct{} {
	result := map[string]struct{}{}
	home, err := os.UserHomeDir()
	if err != nil {
		return result
	}
	path := filepath.Join(home, geminiDirName, geminiConfigFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return result
	}
	var payload geminiMcpFilePayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return result
	}
	for name := range payload.Servers {
		result[strings.ToLower(strings.TrimSpace(name))] = struct{}{}
	}
	return result
}

func (ms *MCPService) mergeImportedServers(target, imported map[string]rawMCPServer) bool {
	changed := false
	for name, entry := range imported {
		entry = normalizeRawEntry(entry)
		if existing, ok := target[name]; ok {
			entry.EnablePlatform = unionPlatforms(existing.EnablePlatform, entry.EnablePlatform)
			if entry.Website == "" {
				entry.Website = existing.Website
			}
			if entry.Tips == "" {
				entry.Tips = existing.Tips
			}
		}
		if existing, ok := target[name]; !ok || !reflect.DeepEqual(existing, entry) {
			target[name] = entry
			changed = true
		}
	}
	return changed
}

func ensureBuiltInServers(target map[string]rawMCPServer, deletedBuiltins []string) bool {
	deletedSet := make(map[string]struct{}, len(deletedBuiltins))
	for _, name := range deletedBuiltins {
		deletedSet[strings.ToLower(strings.TrimSpace(name))] = struct{}{}
	}

	changed := false
	for name, builtIn := range builtInServers {
		// Skip deleted built-in servers (tombstone check)
		if _, deleted := deletedSet[strings.ToLower(name)]; deleted {
			continue
		}

		builtIn = normalizeRawEntry(builtIn)
		if existing, ok := target[name]; ok {
			merged := existing
			merged.EnablePlatform = unionPlatforms(existing.EnablePlatform, builtIn.EnablePlatform)
			if merged.Command == "" {
				merged.Command = builtIn.Command
			}
			if len(merged.Args) == 0 {
				merged.Args = builtIn.Args
			}
			if merged.Cwd == "" {
				merged.Cwd = builtIn.Cwd
			}
			if len(merged.Env) == 0 {
				merged.Env = builtIn.Env
			}
			if merged.URL == "" {
				merged.URL = builtIn.URL
			}
			if len(merged.Headers) == 0 {
				merged.Headers = builtIn.Headers
			}
			if merged.StartupTimeoutSec == 0 {
				merged.StartupTimeoutSec = builtIn.StartupTimeoutSec
			}
			if merged.Website == "" {
				merged.Website = builtIn.Website
			}
			if merged.Tips == "" {
				merged.Tips = builtIn.Tips
			}
			merged = normalizeRawEntry(merged)
			if !reflect.DeepEqual(existing, merged) {
				target[name] = merged
				changed = true
			}
			continue
		}
		target[name] = builtIn
		changed = true
	}
	return changed
}

func (ms *MCPService) syncClaudeServers(servers []MCPServer) error {
	path, err := claudeConfigPath()
	if err != nil {
		return err
	}
	desired := make(map[string]claudeDesktopServer)
	for _, server := range servers {
		if !platformContains(server.EnablePlatform, platClaudeCode) {
			continue
		}
		desired[server.Name] = buildClaudeDesktopEntry(server)
	}
	payload := make(map[string]any)
	if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
		if err := json.Unmarshal(data, &payload); err != nil {
			payload = make(map[string]any)
		}
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	payload["mcpServers"] = desired
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func (ms *MCPService) syncCodexServers(servers []MCPServer) error {
	path, err := codexConfigPath()
	if err != nil {
		return err
	}
	desired := make(map[string]map[string]any)
	for _, server := range servers {
		if !platformContains(server.EnablePlatform, platCodex) {
			continue
		}
		desired[server.Name] = buildCodexEntry(server)
	}
	payload := make(map[string]any)
	if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
		if err := toml.Unmarshal(data, &payload); err != nil {
			payload = make(map[string]any)
		}
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	payload["mcp_servers"] = desired
	data, err := toml.Marshal(payload)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func (ms *MCPService) syncGeminiServers(servers []MCPServer) error {
	path, err := geminiConfigPath()
	if err != nil {
		return err
	}
	desired := make(map[string]map[string]any)
	for _, server := range servers {
		if !platformContains(server.EnablePlatform, platGemini) {
			continue
		}
		desired[server.Name] = buildGeminiEntry(server)
	}
	payload := make(map[string]any)
	if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
		if err := json.Unmarshal(data, &payload); err != nil {
			payload = make(map[string]any)
		}
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	payload["mcpServers"] = desired
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func platformContains(platforms []string, target string) bool {
	for _, value := range platforms {
		if value == target {
			return true
		}
	}
	return false
}

func buildClaudeDesktopEntry(server MCPServer) claudeDesktopServer {
	entry := claudeDesktopServer{Type: server.Type}
	if server.Type == "http" {
		entry.URL = server.URL
		if len(server.Headers) > 0 {
			entry.Headers = server.Headers
		}
	} else {
		entry.Command = server.Command
		if len(server.Args) > 0 {
			entry.Args = server.Args
		}
		if server.Cwd != "" {
			entry.Cwd = server.Cwd
		}
		if len(server.Env) > 0 {
			entry.Env = server.Env
		}
	}
	return entry
}

func buildCodexEntry(server MCPServer) map[string]any {
	entry := make(map[string]any)
	entry["type"] = server.Type
	if server.Type == "http" {
		entry["url"] = server.URL
		if len(server.Headers) > 0 {
			entry["headers"] = server.Headers
		}
	} else {
		entry["command"] = server.Command
		if len(server.Args) > 0 {
			entry["args"] = server.Args
		}
		if server.Cwd != "" {
			entry["cwd"] = server.Cwd
		}
		if len(server.Env) > 0 {
			entry["env"] = server.Env
		}
	}
	if server.StartupTimeoutSec > 0 {
		entry["startup_timeout_sec"] = server.StartupTimeoutSec
	}
	return entry
}

// buildGeminiEntry creates Gemini CLI MCP server config.
// Gemini uses "httpUrl" (not "url") for HTTP type, and omits the "type" field.
func buildGeminiEntry(server MCPServer) map[string]any {
	entry := make(map[string]any)
	if server.Type == "http" {
		entry["httpUrl"] = server.URL
		if len(server.Headers) > 0 {
			entry["headers"] = server.Headers
		}
	} else {
		entry["command"] = server.Command
		if len(server.Args) > 0 {
			entry["args"] = server.Args
		}
		if server.Cwd != "" {
			entry["cwd"] = server.Cwd
		}
		if len(server.Env) > 0 {
			entry["env"] = server.Env
		}
	}
	return entry
}

func claudeConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, claudeMcpFile), nil
}

func codexConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, codexDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(dir, codexConfigFile), nil
}

func geminiConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, geminiDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(dir, geminiConfigFile), nil
}

func detectPlaceholders(url string, command string, args []string, cwd string, env map[string]string, headers map[string]string) []string {
	set := make(map[string]struct{})
	collectPlaceholders(set, url)
	collectPlaceholders(set, command)
	collectPlaceholders(set, cwd)
	for _, arg := range args {
		collectPlaceholders(set, arg)
	}
	for _, value := range env {
		collectPlaceholders(set, value)
	}
	for _, value := range headers {
		collectPlaceholders(set, value)
	}
	if len(set) == 0 {
		return []string{}
	}
	result := make([]string, 0, len(set))
	for key := range set {
		result = append(result, key)
	}
	sort.Strings(result)
	return result
}

func collectPlaceholders(set map[string]struct{}, value string) {
	if value == "" {
		return
	}
	matches := placeholderPattern.FindAllStringSubmatch(value, -1)
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		set[match[1]] = struct{}{}
	}
}

func getString(value any) string {
	if value == nil {
		return ""
	}
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}

func getStringSlice(value any) []string {
	if value == nil {
		return []string{}
	}
	switch v := value.(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if item == nil {
				continue
			}
			trimmed := strings.TrimSpace(fmt.Sprint(item))
			if trimmed != "" {
				out = append(out, trimmed)
			}
		}
		return out
	default:
		return []string{}
	}
}

func getStringMap(value any) map[string]string {
	if value == nil {
		return map[string]string{}
	}
	if m, ok := value.(map[string]string); ok {
		return m
	}
	raw, ok := value.(map[string]any)
	if !ok {
		return map[string]string{}
	}
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		key := strings.TrimSpace(k)
		if key == "" {
			continue
		}
		out[key] = strings.TrimSpace(fmt.Sprint(v))
	}
	return out
}

func getInt(value any) int {
	if value == nil {
		return 0
	}
	switch v := value.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case float32:
		return int(v)
	case string:
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			return 0
		}
		var out int
		_, _ = fmt.Sscanf(trimmed, "%d", &out)
		return out
	default:
		return 0
	}
}
