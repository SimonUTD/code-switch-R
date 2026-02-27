package services

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

type mcpEnabledSnapshot struct {
	Names  map[string]struct{}
	Exists bool
}

// SyncFromPlatformFiles 扫描 Claude/Codex/Gemini 的 MCP 配置文件，并将外部变更同步到本程序的 store：
// - 外部新增：自动导入到 store，并开启对应平台开关
// - 外部删除：若 store 中该平台开关为开启状态，则自动关闭（避免下次保存又写回去）
//
// 注意：该方法只同步“状态/定义”到 store，不会写回任何平台配置文件。
func (ms *MCPService) SyncFromPlatformFiles() ([]MCPServer, error) {
	ms.mu.Lock()
	defer ms.mu.Unlock()

	servers, deletedBuiltins, err := ms.loadServersWithDeletedBuiltins()
	if err != nil {
		return nil, err
	}

	// loadConfig 会完成：合并外部新增、内置 server 补全、以及必要时的 store 写入
	loaded, err := ms.loadConfig()
	if err != nil {
		return nil, err
	}
	servers = loaded

	claudeSnap, err := readClaudeEnabledSnapshot()
	if err != nil {
		return nil, err
	}
	codexSnap, err := readCodexEnabledSnapshot()
	if err != nil {
		return nil, err
	}
	geminiSnap, err := readGeminiEnabledSnapshot()
	if err != nil {
		return nil, err
	}

	changed := false
	for name, entry := range servers {
		next := entry
		c1, updated := reconcileEnablePlatforms(next.EnablePlatform, platClaudeCode, name, claudeSnap)
		changed = changed || updated
		c2, updated := reconcileEnablePlatforms(c1, platCodex, name, codexSnap)
		changed = changed || updated
		c3, updated := reconcileEnablePlatforms(c2, platGemini, name, geminiSnap)
		changed = changed || updated
		next.EnablePlatform = c3
		servers[name] = next
	}

	if changed {
		if err := ms.saveStore(servers, deletedBuiltins); err != nil {
			return nil, err
		}
	}

	return buildMCPServerList(servers, claudeSnap, codexSnap, geminiSnap), nil
}

func buildMCPServerList(
	config map[string]rawMCPServer,
	claudeSnap mcpEnabledSnapshot,
	codexSnap mcpEnabledSnapshot,
	geminiSnap mcpEnabledSnapshot,
) []MCPServer {
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
			Name:              name,
			Type:              typ,
			Command:           strings.TrimSpace(entry.Command),
			Args:              cloneArgs(entry.Args),
			Cwd:               strings.TrimSpace(entry.Cwd),
			Env:               cloneEnv(entry.Env),
			URL:               strings.TrimSpace(entry.URL),
			Headers:           cloneStrMap(entry.Headers),
			StartupTimeoutSec: entry.StartupTimeoutSec,
			Website:           strings.TrimSpace(entry.Website),
			Tips:              strings.TrimSpace(entry.Tips),
			EnablePlatform:    platforms,
			EnabledInClaude:   claudeSnap.Exists && containsNormalized(claudeSnap.Names, name),
			EnabledInCodex:    codexSnap.Exists && containsNormalized(codexSnap.Names, name),
			EnabledInGemini:   geminiSnap.Exists && containsNormalized(geminiSnap.Names, name),
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

	return servers
}

func reconcileEnablePlatforms(
	current []string,
	platform string,
	serverName string,
	snapshot mcpEnabledSnapshot,
) ([]string, bool) {
	if !snapshot.Exists {
		return normalizePlatforms(current), false
	}
	enabledInFile := containsNormalized(snapshot.Names, serverName)
	return setPlatformEnabled(current, platform, enabledInFile)
}

func setPlatformEnabled(platforms []string, platform string, enabled bool) ([]string, bool) {
	normalized := normalizePlatforms(platforms)
	exists := platformContains(normalized, platform)
	if enabled {
		if exists {
			return normalized, false
		}
		return normalizePlatforms(append(normalized, platform)), true
	}
	if !exists {
		return normalized, false
	}
	next := make([]string, 0, len(normalized))
	for _, item := range normalized {
		if item == platform {
			continue
		}
		next = append(next, item)
	}
	return next, true
}

func readClaudeEnabledSnapshot() (mcpEnabledSnapshot, error) {
	path, err := claudeMcpConfigPath()
	if err != nil {
		return mcpEnabledSnapshot{}, err
	}
	data, exists, err := readFileIfExists(path)
	if err != nil || !exists {
		return mcpEnabledSnapshot{Names: map[string]struct{}{}, Exists: exists}, err
	}
	var payload claudeMcpFilePayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return mcpEnabledSnapshot{}, err
	}
	names := make(map[string]struct{}, len(payload.Servers))
	for name := range payload.Servers {
		names[strings.ToLower(strings.TrimSpace(name))] = struct{}{}
	}
	return mcpEnabledSnapshot{Names: names, Exists: true}, nil
}

func readCodexEnabledSnapshot() (mcpEnabledSnapshot, error) {
	path, err := codexMcpConfigPath()
	if err != nil {
		return mcpEnabledSnapshot{}, err
	}
	data, exists, err := readFileIfExists(path)
	if err != nil || !exists {
		return mcpEnabledSnapshot{Names: map[string]struct{}{}, Exists: exists}, err
	}
	var payload codexMcpFilePayload
	if err := toml.Unmarshal(data, &payload); err != nil {
		return mcpEnabledSnapshot{}, err
	}
	names := make(map[string]struct{}, len(payload.Servers))
	for name := range payload.Servers {
		names[strings.ToLower(strings.TrimSpace(name))] = struct{}{}
	}
	return mcpEnabledSnapshot{Names: names, Exists: true}, nil
}

func readGeminiEnabledSnapshot() (mcpEnabledSnapshot, error) {
	path, err := geminiMcpConfigPath()
	if err != nil {
		return mcpEnabledSnapshot{}, err
	}
	data, exists, err := readFileIfExists(path)
	if err != nil || !exists {
		return mcpEnabledSnapshot{Names: map[string]struct{}{}, Exists: exists}, err
	}
	var payload geminiMcpFilePayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return mcpEnabledSnapshot{}, err
	}
	names := make(map[string]struct{}, len(payload.Servers))
	for name := range payload.Servers {
		names[strings.ToLower(strings.TrimSpace(name))] = struct{}{}
	}
	return mcpEnabledSnapshot{Names: names, Exists: true}, nil
}

func readFileIfExists(path string) ([]byte, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return data, true, nil
}

func claudeMcpConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, claudeMcpFile), nil
}

func codexMcpConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, codexDirName, codexConfigFile), nil
}

func geminiMcpConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, geminiDirName, geminiConfigFile), nil
}

func (ms *MCPService) loadServersWithDeletedBuiltins() (map[string]rawMCPServer, []string, error) {
	path, err := ms.configPath()
	if err != nil {
		return nil, nil, err
	}

	servers := map[string]rawMCPServer{}
	var deletedBuiltins []string

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return servers, deletedBuiltins, nil
		}
		return nil, nil, err
	}
	if len(data) == 0 {
		return servers, deletedBuiltins, nil
	}

	var storePayload mcpStorePayload
	if err := json.Unmarshal(data, &storePayload); err == nil && storePayload.Servers != nil {
		return storePayload.Servers, storePayload.DeletedBuiltins, nil
	}

	// legacy flat format
	if err := json.Unmarshal(data, &servers); err != nil {
		return nil, nil, err
	}
	return servers, deletedBuiltins, nil
}
