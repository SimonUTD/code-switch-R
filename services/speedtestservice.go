package services

import (
	"fmt"
	"net/http"
	neturl "net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	defaultTimeoutSecs = 8
	maxTimeoutSecs     = 30
	minTimeoutSecs     = 2
	endpointsFileName  = "speedtest-endpoints.json"
)

// EndpointLatency 端点延迟测试结果
type EndpointLatency struct {
	URL     string  `json:"url"`              // 端点 URL
	Latency *uint64 `json:"latency"`          // 延迟（毫秒），nil 表示失败
	Status  *int    `json:"status,omitempty"` // HTTP 状态码
	Error   *string `json:"error,omitempty"`  // 错误信息
}

// EndpointRecord 端点记录（保存到文件的数据结构）
type EndpointRecord struct {
	URL            string  `json:"url"`              // API 端点 URL
	LastTestTime   *int64  `json:"lastTestTime"`     // 最后一次测速时间（Unix 时间戳），nil 表示未测试
	LastTestSpeed  *uint64 `json:"lastTestSpeed"`    // 最后一次测试速度（毫秒），nil 表示失败或未测试
}

// SpeedTestService 测速服务
type SpeedTestService struct {
	relayAddr string
}

// NewSpeedTestService 创建测速服务
func NewSpeedTestService() *SpeedTestService {
	return &SpeedTestService{}
}

// NewSpeedTestServiceWithAddr 创建带地址的测速服务
func NewSpeedTestServiceWithAddr(relayAddr string) *SpeedTestService {
	return &SpeedTestService{relayAddr: relayAddr}
}

// Start Wails生命周期方法
func (s *SpeedTestService) Start() error {
	return nil
}

// Stop Wails生命周期方法
func (s *SpeedTestService) Stop() error {
	return nil
}

// TestEndpoints 测试一组端点的响应延迟
// 使用并发请求，每个端点先进行一次热身请求，再测量第二次请求的延迟
func (s *SpeedTestService) TestEndpoints(urls []string, timeoutSecs *int) []EndpointLatency {
	if len(urls) == 0 {
		return []EndpointLatency{}
	}

	timeout := s.sanitizeTimeout(timeoutSecs)
	client := s.buildClient(timeout)

	// 并发测试所有端点
	results := make([]EndpointLatency, len(urls))
	var wg sync.WaitGroup

	for i, rawURL := range urls {
		wg.Add(1)
		go func(index int, urlStr string) {
			defer wg.Done()
			results[index] = s.testSingleEndpoint(client, urlStr)
		}(i, rawURL)
	}

	wg.Wait()

	// 保存测试结果（无论成功还是失败）
	for _, result := range results {
		if result.Error == nil {
			_ = s.UpdateEndpointTestResult(result.URL, result.Latency)
		} else {
			// 测试失败也要记录，使用 nil 表示失败
			_ = s.UpdateEndpointTestResult(result.URL, nil)
		}
	}

	return results
}

// testSingleEndpoint 测试单个端点
func (s *SpeedTestService) testSingleEndpoint(client *http.Client, rawURL string) EndpointLatency {
	trimmed := trimSpace(rawURL)
	if trimmed == "" {
		errMsg := "URL 不能为空"
		return EndpointLatency{
			URL:     rawURL,
			Latency: nil,
			Status:  nil,
			Error:   &errMsg,
		}
	}

	// 验证 URL
	parsedURL, err := neturl.Parse(trimmed)
	if err != nil {
		errMsg := fmt.Sprintf("URL 无效: %v", err)
		return EndpointLatency{
			URL:     trimmed,
			Latency: nil,
			Status:  nil,
			Error:   &errMsg,
		}
	}

	// 热身请求（忽略结果，用于建立连接）
	_, _ = s.makeRequest(client, parsedURL.String())

	// 第二次请求：测量延迟
	start := time.Now()
	resp, err := s.makeRequest(client, parsedURL.String())
	latency := uint64(time.Since(start).Milliseconds())

	if err != nil {
		errMsg := s.formatError(err)
		return EndpointLatency{
			URL:     trimmed,
			Latency: nil,
			Status:  nil,
			Error:   &errMsg,
		}
	}
	defer resp.Body.Close()

	statusCode := resp.StatusCode
	return EndpointLatency{
		URL:     trimmed,
		Latency: &latency,
		Status:  &statusCode,
		Error:   nil,
	}
}

// makeRequest 发送 HTTP GET 请求
func (s *SpeedTestService) makeRequest(client *http.Client, urlStr string) (*http.Response, error) {
	req, err := http.NewRequest("GET", urlStr, nil)
	if err != nil {
		return nil, err
	}

	// 设置 User-Agent
	req.Header.Set("User-Agent", "cc-r-speedtest/1.0")

	return client.Do(req)
}

// formatError 格式化错误信息
func (s *SpeedTestService) formatError(err error) string {
	// 检查是否是超时错误
	if e, ok := err.(interface{ Timeout() bool }); ok && e.Timeout() {
		return "请求超时"
	}

	// 其他错误
	return fmt.Sprintf("请求失败: %v", err)
}

// buildClient 构建 HTTP 客户端
func (s *SpeedTestService) buildClient(timeoutSecs int) *http.Client {
	return &http.Client{
		Timeout: time.Duration(timeoutSecs) * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			// 限制重定向次数为 5
			if len(via) >= 5 {
				return fmt.Errorf("重定向次数过多")
			}
			return nil
		},
	}
}

// sanitizeTimeout 规范化超时参数
func (s *SpeedTestService) sanitizeTimeout(timeoutSecs *int) int {
	if timeoutSecs == nil {
		return defaultTimeoutSecs
	}

	secs := *timeoutSecs
	if secs < minTimeoutSecs {
		return minTimeoutSecs
	}
	if secs > maxTimeoutSecs {
		return maxTimeoutSecs
	}
	return secs
}

// getEndpointsFilePath 获取端点清单文件路径
func (s *SpeedTestService) getEndpointsFilePath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".code-switch", endpointsFileName)
}

// LoadEndpoints 加载端点清单
func (s *SpeedTestService) LoadEndpoints() ([]EndpointRecord, error) {
	filePath := s.getEndpointsFilePath()

	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		// 文件不存在，创建默认端点文件
		defaultRecords := []EndpointRecord{
			{URL: "https://api.anthropic.com", LastTestTime: nil, LastTestSpeed: nil},
			{URL: "https://api.openai.com", LastTestTime: nil, LastTestSpeed: nil},
		}

		// 确保目录存在并创建文件
		if err := s.SaveEndpoints(defaultRecords); err != nil {
			return nil, fmt.Errorf("创建默认端点文件失败: %w", err)
		}

		return defaultRecords, nil
	}

	var records []EndpointRecord
	if err := ReadJSONFile(filePath, &records); err != nil {
		// 读取失败，尝试创建默认文件
		defaultRecords := []EndpointRecord{
			{URL: "https://api.anthropic.com", LastTestTime: nil, LastTestSpeed: nil},
			{URL: "https://api.openai.com", LastTestTime: nil, LastTestSpeed: nil},
		}

		if err := s.SaveEndpoints(defaultRecords); err != nil {
			return nil, fmt.Errorf("创建默认端点文件失败: %w", err)
		}

		return defaultRecords, nil
	}

	return records, nil
}

// SaveEndpoints 保存端点清单
func (s *SpeedTestService) SaveEndpoints(records []EndpointRecord) error {
	filePath := s.getEndpointsFilePath()

	// 确保目录存在
	if err := EnsureDir(filepath.Dir(filePath)); err != nil {
		return fmt.Errorf("创建目录失败: %w", err)
	}

	return AtomicWriteJSON(filePath, records)
}

// AddEndpoint 添加新的端点
func (s *SpeedTestService) AddEndpoint(url string) error {
	if url == "" {
		return fmt.Errorf("URL 不能为空")
	}

	// 验证 URL
	_, err := neturl.Parse(url)
	if err != nil {
		return fmt.Errorf("URL 无效: %w", err)
	}

	// 加载现有端点
	records, err := s.LoadEndpoints()
	if err != nil {
		return err
	}

	// 检查重复
	for _, record := range records {
		if record.URL == url {
			return fmt.Errorf("端点已存在: %s", url)
		}
	}

	// 添加新端点
	records = append(records, EndpointRecord{
		URL:           url,
		LastTestTime:  nil,
		LastTestSpeed: nil,
	})

	return s.SaveEndpoints(records)
}

// RemoveEndpoint 移除端点
func (s *SpeedTestService) RemoveEndpoint(url string) error {
	if url == "" {
		return fmt.Errorf("URL 不能为空")
	}

	// 加载现有端点
	records, err := s.LoadEndpoints()
	if err != nil {
		return err
	}

	// 查找并移除
	var newRecords []EndpointRecord
	found := false
	for _, record := range records {
		if record.URL != url {
			newRecords = append(newRecords, record)
		} else {
			found = true
		}
	}

	if !found {
		return fmt.Errorf("端点不存在: %s", url)
	}

	return s.SaveEndpoints(newRecords)
}

// UpdateEndpointTestResult 更新端点测试结果
func (s *SpeedTestService) UpdateEndpointTestResult(url string, latency *uint64) error {
	if url == "" {
		return fmt.Errorf("URL 不能为空")
	}

	// 加载现有端点
	records, err := s.LoadEndpoints()
	if err != nil {
		return err
	}

	// 更新测试结果
	now := time.Now().Unix()
	found := false
	for i, record := range records {
		if record.URL == url {
			records[i].LastTestTime = &now
			records[i].LastTestSpeed = latency
			found = true
			break
		}
	}

	if !found {
		return fmt.Errorf("端点不存在: %s", url)
	}

	return s.SaveEndpoints(records)
}

// ExtractEndpointsFromConfigs 从配置文件中提取API端点
func (s *SpeedTestService) ExtractEndpointsFromConfigs(relayAddr string) ([]string, error) {
	var urls []string
	seen := make(map[string]bool)
	home, _ := os.UserHomeDir()
	configDir := filepath.Join(home, ".code-switch")

	// 从 Claude Code 配置文件中提取 API URL
	claudeConfigPath := filepath.Join(configDir, "claude-code.json")
	if claudeProviders, err := s.loadProviderFile(claudeConfigPath); err == nil {
		for _, provider := range claudeProviders {
			if provider.APIURL != "" && provider.Enabled {
				if !seen[provider.APIURL] {
					urls = append(urls, provider.APIURL)
					seen[provider.APIURL] = true
				}
			}
		}
	}

	// 从 Codex 配置文件中提取 API URL
	codexConfigPath := filepath.Join(configDir, "codex.json")
	if codexProviders, err := s.loadProviderFile(codexConfigPath); err == nil {
		for _, provider := range codexProviders {
			if provider.APIURL != "" && provider.Enabled {
				if !seen[provider.APIURL] {
					urls = append(urls, provider.APIURL)
					seen[provider.APIURL] = true
				}
			}
		}
	}

	// 从 Gemini 配置文件中提取 BaseURL
	geminiConfigPath := filepath.Join(configDir, "gemini-providers.json")
	if geminiProviders, err := s.loadGeminiProviderFile(geminiConfigPath); err == nil {
		for _, provider := range geminiProviders {
			if provider.BaseURL != "" && provider.Enabled {
				if !seen[provider.BaseURL] {
					urls = append(urls, provider.BaseURL)
					seen[provider.BaseURL] = true
				}
			}
		}
	}

	// 如果没有找到任何配置，尝试使用默认的代理地址
	if len(urls) == 0 && s.relayAddr != "" {
		defaultURL := s.getBaseURLFromRelayAddr()
		if defaultURL != "" {
			urls = append(urls, defaultURL)
		}
	}

	return urls, nil
}

// loadProviderFile 加载 Provider 配置文件 (Claude/Codex)
func (s *SpeedTestService) loadProviderFile(filePath string) ([]Provider, error) {
	var envelope providerEnvelope
	if err := ReadJSONFile(filePath, &envelope); err != nil {
		return nil, err
	}
	return envelope.Providers, nil
}

// loadGeminiProviderFile 加载 Gemini Provider 配置文件
func (s *SpeedTestService) loadGeminiProviderFile(filePath string) ([]GeminiProvider, error) {
	var providers []GeminiProvider
	if err := ReadJSONFile(filePath, &providers); err != nil {
		return nil, err
	}
	return providers, nil
}

// getBaseURLFromRelayAddr 从代理地址生成基础URL
func (s *SpeedTestService) getBaseURLFromRelayAddr() string {
	if s.relayAddr == "" {
		return ""
	}

	addr := strings.TrimSpace(s.relayAddr)
	if strings.HasPrefix(addr, "http://") || strings.HasPrefix(addr, "https://") {
		return addr
	}

	host := addr
	if strings.HasPrefix(host, ":") {
		host = "127.0.0.1" + host
	}
	if !strings.Contains(host, "://") {
		host = "http://" + host
	}
	return host
}

// RefreshEndpointsFromConfigs 从配置文件刷新端点清单
func (s *SpeedTestService) RefreshEndpointsFromConfigs(relayAddr string) error {
	// 提取配置中的端点
	configURLs, err := s.ExtractEndpointsFromConfigs(relayAddr)
	if err != nil {
		return fmt.Errorf("从配置提取端点失败: %w", err)
	}

	// 加载现有端点
	records, err := s.LoadEndpoints()
	if err != nil {
		return err
	}

	// 创建 URL 到记录的映射
	recordMap := make(map[string]EndpointRecord)
	for _, record := range records {
		recordMap[record.URL] = record
	}

	// 添加配置中的新端点
	for _, url := range configURLs {
		if _, exists := recordMap[url]; !exists {
			records = append(records, EndpointRecord{
				URL:           url,
				LastTestTime:  nil,
				LastTestSpeed: nil,
			})
		}
	}

	return s.SaveEndpoints(records)
}

// GetEndpointRecords 获取端点记录（供前端调用）
func (s *SpeedTestService) GetEndpointRecords() ([]EndpointRecord, error) {
	// 先尝试从配置刷新（忽略错误，避免崩溃）
	if s.relayAddr != "" {
		if err := s.RefreshEndpointsFromConfigs(s.relayAddr); err != nil {
			// 配置刷新失败，记录日志但不影响主要功能
			fmt.Printf("从配置刷新端点失败: %v\n", err)
		}
	}

	// 返回端点记录
	return s.LoadEndpoints()
}

// AddEndpointRecord 添加端点记录（供前端调用）
func (s *SpeedTestService) AddEndpointRecord(url string) error {
	return s.AddEndpoint(url)
}

// RemoveEndpointRecord 移除端点记录（供前端调用）
func (s *SpeedTestService) RemoveEndpointRecord(url string) error {
	return s.RemoveEndpoint(url)
}

// trimSpace 去除字符串首尾空格
func trimSpace(s string) string {
	start := 0
	end := len(s)

	for start < end && (s[start] == ' ' || s[start] == '\t' || s[start] == '\n' || s[start] == '\r') {
		start++
	}

	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\n' || s[end-1] == '\r') {
		end--
	}

	return s[start:end]
}
