package services

import (
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/daodao97/xgo/xdb"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// responseBufferSize 是流式响应复制时使用的缓冲区大小
const responseBufferSize = 32 * 1024 // 32KB

// LastUsedProvider 最后使用的供应商信息
// @author sm
type LastUsedProvider struct {
	Platform     string `json:"platform"`      // claude/codex/gemini
	ProviderName string `json:"provider_name"` // 供应商名称
	UpdatedAt    int64  `json:"updated_at"`    // 更新时间（毫秒）
}

type ProviderRelayService struct {
	providerService     *ProviderService
	geminiService       *GeminiService
	blacklistService    *BlacklistService
	notificationService *NotificationService
	appSettings         *AppSettingsService   // 应用设置服务（用于获取轮询开关状态）
	affinityManager     *CacheAffinityManager // 5分钟同源缓存亲和性管理器
	server              *http.Server
	addr                string
	lastUsed            map[string]*LastUsedProvider // 各平台最后使用的供应商
	lastUsedMu          sync.RWMutex                 // 保护 lastUsed 的锁
	rrMu                sync.Mutex                   // 轮询状态锁
	rrLastStart         map[string]string            // 轮询状态：key="platform:level" → value=上次起始 Provider Name
}

// errClientAbort 表示客户端中断连接，不应计入 provider 失败次数
var errClientAbort = errors.New("client aborted, skip failure count")

// skipReasons tracks the count of providers skipped during filtering
type skipReasons struct {
	disabled         int // disabled or missing URL/APIKey
	configInvalid    int // configuration validation failed
	modelUnsupported int // does not support requested model
	blacklisted      int // temporarily unavailable (blacklisted)
}

// total returns the total count of all skip reasons
func (s *skipReasons) total() int {
	return s.disabled + s.configInvalid + s.modelUnsupported + s.blacklisted
}

// formatKind formats the kind parameter for user-friendly display
func formatKind(kind string) string {
	if strings.HasPrefix(kind, "custom:") {
		toolID := strings.TrimPrefix(kind, "custom:")
		return fmt.Sprintf("custom CLI '%s'", toolID)
	}
	return kind
}

// buildNoProviderError builds a user-friendly error message with detailed skip reasons
// kind parameter is used to display platform type when requestedModel is empty
func buildNoProviderError(requestedModel string, kind string, reasons skipReasons) string {
	if requestedModel == "" {
		if kind != "" {
			return fmt.Sprintf("no providers available for %s", formatKind(kind))
		}
		return "no providers available"
	}

	var details []string
	if reasons.modelUnsupported > 0 {
		details = append(details, fmt.Sprintf("%d not supporting this model", reasons.modelUnsupported))
	}
	if reasons.blacklisted > 0 {
		details = append(details, fmt.Sprintf("%d temporarily unavailable (blacklisted, retry later or check quota)", reasons.blacklisted))
	}
	if reasons.configInvalid > 0 {
		details = append(details, fmt.Sprintf("%d with invalid config", reasons.configInvalid))
	}
	if reasons.disabled > 0 {
		details = append(details, fmt.Sprintf("%d disabled or missing credentials", reasons.disabled))
	}

	errMsg := fmt.Sprintf("no providers available for model '%s'", requestedModel)
	if len(details) > 0 {
		errMsg += " (" + strings.Join(details, ", ") + ")"
	}
	return errMsg
}

// AuthMethod 表示原始请求使用的认证方式
type AuthMethod int

const (
	AuthMethodBearer  AuthMethod = iota // Authorization: Bearer xxx
	AuthMethodXAPIKey                   // x-api-key: xxx
)

// detectAuthMethod 检测原始请求使用的认证方式
// 使用 http.Header.Get 进行大小写无关的匹配
func detectAuthMethod(header http.Header) AuthMethod {
	// 优先检查 x-api-key（使用 http.Header.Get 自动处理大小写）
	if header.Get("X-Api-Key") != "" {
		return AuthMethodXAPIKey
	}
	// 默认使用 Authorization Bearer
	return AuthMethodBearer
}

// determineAuthMethod 根据 Provider 配置和原始请求确定最终的认证方式
// 优先级：Provider.ConnectivityAuthType (当显式设置为 bearer/x-api-key 时) > 自动检测原始请求
// 当 Provider 配置为 "auto" 或空值时，使用自动检测逻辑
func determineAuthMethod(provider *Provider, requestHeader http.Header) AuthMethod {
	if provider == nil {
		return detectAuthMethod(requestHeader)
	}

	authType := strings.TrimSpace(strings.ToLower(provider.ConnectivityAuthType))

	switch authType {
	case "bearer":
		return AuthMethodBearer
	case "x-api-key":
		return AuthMethodXAPIKey
	case "auto", "":
		// 自动检测原始请求的认证方式
		return detectAuthMethod(requestHeader)
	default:
		// 自定义 Header 名称场景：暂时按 Bearer 处理
		// 注意：自定义 Header 应通过 OverrideHeaders 实现，这里仅作兜底
		slog.Warn("Unknown auth type, falling back to Bearer", "authType", authType, "provider", provider.Name)
		return AuthMethodBearer
	}
}

// httpHeaderToMap 将 http.Header 转换为 map[string]string
// 用于与需要 map[string]string 的函数（如 SanitizeHeaders）兼容
func httpHeaderToMap(h http.Header) map[string]string {
	result := make(map[string]string, len(h))
	for k, v := range h {
		if len(v) > 0 {
			result[k] = v[len(v)-1] // 取最后一个值
		}
	}
	return result
}

// hopByHopHeaders 是不应该被代理转发的逐跳头（hop-by-hop headers）
// 参考 RFC 2616 Section 13.5.1 和 RFC 7230 Section 6.1
var hopByHopHeaders = map[string]bool{
	"Connection":          true,
	"Keep-Alive":          true,
	"Proxy-Authenticate":  true,
	"Proxy-Authorization": true,
	"Te":                  true,
	"Trailer":             true,
	"Transfer-Encoding":   true,
	"Upgrade":             true,
}

// RetryTransport 是一个支持网络级错误自动重试的 http.RoundTripper
// 用于处理瞬时网络抖动（TCP reset、DNS 解析失败等），避免直接计入应用层失败次数
type RetryTransport struct {
	Base       http.RoundTripper
	MaxRetries int           // 最大重试次数
	RetryDelay time.Duration // 重试间隔
}

// RoundTrip 实现 http.RoundTripper 接口，支持网络级错误重试
func (rt *RetryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var lastErr error
	base := rt.Base
	if base == nil {
		base = http.DefaultTransport
	}

	for attempt := 0; attempt <= rt.MaxRetries; attempt++ {
		// 检查 context 是否已取消
		if req.Context().Err() != nil {
			return nil, req.Context().Err()
		}

		// 重试时重置 request body（如果 GetBody 可用）
		if attempt > 0 && req.GetBody != nil {
			newBody, err := req.GetBody()
			if err != nil {
				return nil, fmt.Errorf("failed to reset request body for retry: %w", err)
			}
			req.Body = newBody
		}

		resp, err := base.RoundTrip(req)
		if err == nil {
			return resp, nil
		}

		lastErr = err

		// 检查是否是 context 取消/超时
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}

		// 判断是否是网络级别的瞬时错误（值得重试）
		if !isTransientNetworkError(err) {
			return nil, err
		}

		// 最后一次尝试不等待
		if attempt < rt.MaxRetries {
			fmt.Printf("[RetryTransport] 网络错误，%dms 后重试（%d/%d）: %v\n",
				rt.RetryDelay.Milliseconds(), attempt+1, rt.MaxRetries, err)
			time.Sleep(rt.RetryDelay)
		}
	}

	return nil, lastErr
}

// isTransientNetworkError 判断是否是值得重试的瞬时网络错误
func isTransientNetworkError(err error) bool {
	if err == nil {
		return false
	}

	// 使用类型断言检测网络错误（比字符串匹配更可靠）
	// 检查是否是 net.Error（网络超时等）
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}

	// 检查是否是 net.OpError（连接级别错误）
	// 只重试 reset/broken pipe 等瞬时错误，不重试 connection refused（服务器可能已关闭）
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		errStr := strings.ToLower(opErr.Error())
		return strings.Contains(errStr, "reset") ||
			strings.Contains(errStr, "broken pipe")
	}

	// 检查是否是 DNS 错误
	// 只重试临时 DNS 错误（如超时、服务器不可达），不重试永久错误如 NXDOMAIN
	// 注意：Temporary() 方法已废弃，使用 IsTemporary 字段代替
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		// IsNotFound 表示域名不存在（NXDOMAIN），不应重试
		if dnsErr.IsNotFound {
			return false
		}
		// IsTemporary 表示临时性 DNS 错误（超时、服务器暂时不可达等）
		// IsTimeout 表示 DNS 查询超时，也应该重试
		// 注意：IsTemporary 默认为 false，需要显式检查 IsTimeout
		return dnsErr.IsTemporary || dnsErr.IsTimeout
	}

	// 回退：字符串匹配处理未被上述类型覆盖的错误
	// 使用更精确的模式避免误匹配（如 "Geoffrey" 包含 "eof"）
	errStr := strings.ToLower(err.Error())
	transientErrors := []string{
		"unexpected eof",
		"read: eof",
		" eof",  // 空格前缀避免误匹配如 "Geoffrey"
		"eof\n", // 行尾 eof
		"broken pipe",
		"connection reset by peer",
		"tls handshake timeout",
	}

	for _, te := range transientErrors {
		if strings.Contains(errStr, te) {
			return true
		}
	}

	return false
}

// newRetryHTTPClient 返回带有网络级重试的 HTTP 客户端（按渠道应用代理配置）。
// - Base Transport 由 GetHTTPTransportForKind(kind) 提供，确保该渠道所有网络流量遵循“走/不走代理”配置
// - 超时应通过 context.WithTimeout 控制，避免修改共享客户端字段
func newRetryHTTPClient(kind string, maxRetries int, retryDelay time.Duration) *http.Client {
	// 注意：maxRetries <= 0 被视为"使用默认值"（1次重试），而非"不重试"
	// 如需完全禁用重试，应使用标准 http.Client 或直接使用 GetHTTPClientForKind()
	if maxRetries <= 0 {
		maxRetries = 1
	}
	if retryDelay <= 0 {
		retryDelay = 500 * time.Millisecond
	}

	baseTransport := GetHTTPTransportForKind(kind)
	return &http.Client{
		Transport: &RetryTransport{
			Base:       baseTransport,
			MaxRetries: maxRetries,
			RetryDelay: retryDelay,
		},
	}
}

// isHopByHopHeader 检查给定的 header 是否是逐跳头
func isHopByHopHeader(header string) bool {
	return hopByHopHeaders[header]
}

func NewProviderRelayService(providerService *ProviderService, geminiService *GeminiService, blacklistService *BlacklistService, notificationService *NotificationService, appSettings *AppSettingsService, addr string) *ProviderRelayService {
	if addr == "" {
		addr = "127.0.0.1:18100" // 【安全修复】仅监听本地回环地址，防止 API Key 暴露到局域网
	}

	// 【修复】数据库初始化已移至 main.go 的 InitDatabase()
	// 此处不再调用 xdb.Inits()、ensureRequestLogTable()、ensureBlacklistTables()

	// 初始化 5 分钟同源缓存亲和性管理器
	affinityManager := NewCacheAffinityManager(5 * time.Minute)

	return &ProviderRelayService{
		providerService:     providerService,
		geminiService:       geminiService,
		blacklistService:    blacklistService,
		notificationService: notificationService,
		appSettings:         appSettings,
		affinityManager:     affinityManager,
		addr:                addr,
		lastUsed: map[string]*LastUsedProvider{
			"claude": nil,
			"codex":  nil,
			"gemini": nil,
		},
		rrLastStart: make(map[string]string),
	}
}

// setLastUsedProvider 记录最后使用的供应商
// @author sm
func (prs *ProviderRelayService) setLastUsedProvider(platform, providerName string) {
	prs.lastUsedMu.Lock()
	defer prs.lastUsedMu.Unlock()
	prs.lastUsed[platform] = &LastUsedProvider{
		Platform:     platform,
		ProviderName: providerName,
		UpdatedAt:    time.Now().UnixMilli(),
	}
}

// GetLastUsedProvider 获取指定平台最后使用的供应商
// @author sm
func (prs *ProviderRelayService) GetLastUsedProvider(platform string) *LastUsedProvider {
	prs.lastUsedMu.RLock()
	defer prs.lastUsedMu.RUnlock()
	return prs.lastUsed[platform]
}

// GetAllLastUsedProviders 获取所有平台最后使用的供应商
// @author sm
func (prs *ProviderRelayService) GetAllLastUsedProviders() map[string]*LastUsedProvider {
	prs.lastUsedMu.RLock()
	defer prs.lastUsedMu.RUnlock()
	result := make(map[string]*LastUsedProvider)
	for k, v := range prs.lastUsed {
		result[k] = v
	}
	return result
}

// isRoundRobinEnabled 检查轮询功能是否启用
// 条件：1. 应用设置开关启用 2. 拉黑模式关闭（Fixed Mode 跳过轮询）
func (prs *ProviderRelayService) isRoundRobinEnabled() bool {
	// 检查拉黑模式是否启用（Fixed Mode 优先级高于轮询）
	if prs.blacklistService.ShouldUseFixedMode() {
		return false
	}

	// 检查应用设置开关
	if prs.appSettings == nil {
		return false
	}
	settings, err := prs.appSettings.GetAppSettings()
	if err != nil {
		return false
	}
	return settings.EnableRoundRobin
}

// roundRobinOrder 对同 Level 的 providers 进行轮询排序
// 算法：基于 name 追踪，将上次起始 provider 移到末尾，实现轮询效果
// 参数：
//   - platform: 平台标识（claude/codex/gemini/custom:xxx）
//   - level: 当前 Level
//   - providers: 同 Level 的 providers 列表（已过滤、按用户排序）
//
// 返回：轮询排序后的 providers 列表（新切片，不修改原切片）
func (prs *ProviderRelayService) roundRobinOrder(platform string, level int, providers []Provider) []Provider {
	if len(providers) <= 1 {
		return providers
	}

	// 构建 key: "platform:level"
	key := fmt.Sprintf("%s:%d", platform, level)

	prs.rrMu.Lock()
	defer prs.rrMu.Unlock()

	lastStart := prs.rrLastStart[key]

	// 记录本次起始 provider 名称（更新状态）
	prs.rrLastStart[key] = providers[0].Name

	// 如果没有历史记录，返回原顺序
	if lastStart == "" {
		return providers
	}

	// 查找上次起始 provider 在当前列表中的位置
	lastIdx := -1
	for i, p := range providers {
		if p.Name == lastStart {
			lastIdx = i
			break
		}
	}

	// 上次起始 provider 不在当前列表（可能被禁用/黑名单），返回原顺序
	if lastIdx == -1 {
		return providers
	}

	// 构建轮询顺序：从 lastIdx+1 开始，环形遍历
	result := make([]Provider, len(providers))
	for i := 0; i < len(providers); i++ {
		idx := (lastIdx + 1 + i) % len(providers)
		result[i] = providers[idx]
	}

	// 更新本次起始 provider 名称
	prs.rrLastStart[key] = result[0].Name

	return result
}

// roundRobinOrderGemini 对 Gemini providers 进行轮询排序（复用相同逻辑）
func (prs *ProviderRelayService) roundRobinOrderGemini(level int, providers []GeminiProvider) []GeminiProvider {
	if len(providers) <= 1 {
		return providers
	}

	// 构建 key: "gemini:level"
	key := fmt.Sprintf("gemini:%d", level)

	prs.rrMu.Lock()
	defer prs.rrMu.Unlock()

	lastStart := prs.rrLastStart[key]

	// 记录本次起始 provider 名称
	prs.rrLastStart[key] = providers[0].Name

	// 如果没有历史记录，返回原顺序
	if lastStart == "" {
		return providers
	}

	// 查找上次起始 provider 在当前列表中的位置
	lastIdx := -1
	for i, p := range providers {
		if p.Name == lastStart {
			lastIdx = i
			break
		}
	}

	// 上次起始 provider 不在当前列表，返回原顺序
	if lastIdx == -1 {
		return providers
	}

	// 构建轮询顺序
	result := make([]GeminiProvider, len(providers))
	for i := 0; i < len(providers); i++ {
		idx := (lastIdx + 1 + i) % len(providers)
		result[i] = providers[idx]
	}

	// 更新本次起始 provider 名称
	prs.rrLastStart[key] = result[0].Name

	return result
}

func (prs *ProviderRelayService) Start() error {
	// 启动前验证配置
	if warnings := prs.validateConfig(); len(warnings) > 0 {
		fmt.Println("======== Provider 配置验证警告 ========")
		for _, warn := range warnings {
			fmt.Printf("⚠️  %s\n", warn)
		}
		fmt.Println("========================================")
	}

	// 启动缓存亲和性管理器的后台清理任务
	if prs.affinityManager != nil {
		prs.affinityManager.StartCleanupTask()
	}

	router := gin.Default()
	prs.registerRoutes(router)

	prs.server = &http.Server{
		Addr:    prs.addr,
		Handler: router,
	}

	fmt.Printf("provider relay server listening on %s\n", prs.addr)

	go func() {
		if err := prs.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Printf("provider relay server error: %v\n", err)
		}
	}()
	return nil
}

// validateConfig 验证所有 provider 的配置
// 返回警告列表（非阻塞性错误）
func (prs *ProviderRelayService) validateConfig() []string {
	warnings := make([]string, 0)

	for _, kind := range []string{"claude", "codex"} {
		providers, err := prs.providerService.LoadProviders(kind)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("[%s] 加载配置失败: %v", kind, err))
			continue
		}

		enabledCount := 0
		for _, p := range providers {
			if !p.Enabled {
				continue
			}
			enabledCount++

			// 验证每个启用的 provider
			if errs := p.ValidateConfiguration(); len(errs) > 0 {
				for _, errMsg := range errs {
					warnings = append(warnings, fmt.Sprintf("[%s/%s] %s", kind, p.Name, errMsg))
				}
			}

			// 检查是否配置了模型白名单或映射
			if len(p.SupportedModels) == 0 && len(p.ModelMapping) == 0 {
				warnings = append(warnings, fmt.Sprintf(
					"[%s/%s] 未配置 supportedModels 或 modelMapping，将假设支持所有模型（可能导致降级失败）",
					kind, p.Name))
			}

			// 检查是否只配置了映射但没有白名单
			if len(p.ModelMapping) > 0 && len(p.SupportedModels) == 0 {
				warnings = append(warnings, fmt.Sprintf(
					"[%s/%s] 配置了 modelMapping 但未配置 supportedModels，映射目标将不做校验，请确认目标模型在供应商处可用",
					kind, p.Name))
			}
		}

		if enabledCount == 0 {
			warnings = append(warnings, fmt.Sprintf("[%s] 没有启用的 provider", kind))
		}
	}

	return warnings
}

func (prs *ProviderRelayService) Stop() error {
	// 停止缓存亲和性管理器的后台清理任务
	if prs.affinityManager != nil {
		prs.affinityManager.StopCleanupTask()
	}

	if prs.server == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return prs.server.Shutdown(ctx)
}

func (prs *ProviderRelayService) Addr() string {
	return prs.addr
}

func (prs *ProviderRelayService) registerRoutes(router gin.IRouter) {
	router.POST("/v1/messages", prs.proxyHandler("claude", "/v1/messages"))
	router.POST("/responses", prs.proxyHandler("codex", "/responses"))

	// /v1/models 端点（OpenAI-compatible API）
	// 默认走 codex（OpenAI）平台的 providers
	router.GET("/v1/models", prs.modelsHandler("codex"))

	// Gemini API 端点（使用专门的路径前缀避免与 Claude 冲突）
	router.POST("/gemini/v1beta/*any", prs.geminiProxyHandler("/v1beta"))
	router.POST("/gemini/v1/*any", prs.geminiProxyHandler("/v1"))

	// 自定义 CLI 工具端点（路由格式: /custom/:toolId/v1/messages）
	// toolId 用于区分不同的 CLI 工具，对应 provider kind 为 "custom:{toolId}"
	router.POST("/custom/:toolId/v1/messages", prs.customCliProxyHandler())

	// 自定义 CLI 工具的 /v1/models 端点
	router.GET("/custom/:toolId/v1/models", prs.customModelsHandler())
}

func (prs *ProviderRelayService) proxyHandler(kind string, endpoint string) gin.HandlerFunc {
	return func(c *gin.Context) {
		var bodyBytes []byte
		if c.Request.Body != nil {
			data, err := io.ReadAll(c.Request.Body)
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
				return
			}
			bodyBytes = data
			c.Request.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		}

		isStream := gjson.GetBytes(bodyBytes, "stream").Bool()
		requestedModel := gjson.GetBytes(bodyBytes, "model").String()

		// 如果未指定模型，记录警告但不拦截
		if requestedModel == "" {
			fmt.Printf("[WARN] 请求未指定模型名，无法执行模型智能降级\n")
		}

		// 【5分钟同源缓存】提取 user_id 用于缓存亲和性
		userID := prs.extractUserID(c)
		affinityKey := GenerateAffinityKey(userID, kind, requestedModel)

		providers, err := prs.providerService.LoadProviders(kind)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load providers"})
			return
		}

		active := make([]Provider, 0, len(providers))
		reasons := skipReasons{} // track skip reasons
		for _, provider := range providers {
			// Basic filter: enabled, URL, APIKey
			if !provider.Enabled || provider.APIURL == "" || provider.APIKey == "" {
				reasons.disabled++
				continue
			}

			// Config validation: auto-skip on failure
			if errs := provider.ValidateConfiguration(); len(errs) > 0 {
				fmt.Printf("[WARN] Provider %s config validation failed, skipped: %v\n", provider.Name, errs)
				reasons.configInvalid++
				continue
			}

			// Model filter: only keep providers supporting the requested model
			if requestedModel != "" && !provider.IsModelSupported(requestedModel) {
				fmt.Printf("[INFO] Provider %s does not support model %s, skipped\n", provider.Name, requestedModel)
				reasons.modelUnsupported++
				continue
			}

			// Blacklist check: skip blacklisted providers
			if isBlacklisted, until := prs.blacklistService.IsBlacklisted(kind, provider.Name); isBlacklisted {
				fmt.Printf("⛔ Provider %s blacklisted until %v\n", provider.Name, until.Format("15:04:05"))
				reasons.blacklisted++
				continue
			}

			active = append(active, provider)
		}

		if len(active) == 0 {
			errMsg := buildNoProviderError(requestedModel, kind, reasons)
			c.JSON(http.StatusNotFound, gin.H{"error": errMsg})
			return
		}

		// Build provider names list
		providerNames := make([]string, len(active))
		for i, p := range active {
			providerNames[i] = p.Name
		}
		fmt.Printf("[INFO] Providers: total=%d, active=%d, skipped=%d (disabled=%d, config=%d, model=%d, blacklist=%d): %s\n",
			len(providers), len(active), reasons.total(),
			reasons.disabled, reasons.configInvalid, reasons.modelUnsupported, reasons.blacklisted,
			strings.Join(providerNames, ", "))
		fmt.Println()

		// 【5分钟同源缓存】检查是否有缓存的 provider
		cachedProviderName := ""
		if prs.affinityManager != nil {
			cachedProviderName = prs.affinityManager.Get(affinityKey)
		}

		// 按 Level 分组
		levelGroups := make(map[int][]Provider)
		for _, provider := range active {
			level := provider.Level
			if level <= 0 {
				level = 1 // 未配置或零值时默认为 Level 1
			}
			levelGroups[level] = append(levelGroups[level], provider)
		}

		// 获取所有 level 并升序排序
		levels := make([]int, 0, len(levelGroups))
		for level := range levelGroups {
			levels = append(levels, level)
		}
		sort.Ints(levels)

		fmt.Printf("[INFO] 共 %d 个 Level 分组：%v\n", len(levels), levels)

		query := flattenQuery(c.Request.URL.Query())
		clientHeaders := cloneHeaders(c.Request.Header)
		// 注意：认证方式在每个 provider 转发时由 determineAuthMethod() 动态计算

		// 获取拉黑功能开关状态
		blacklistEnabled := prs.blacklistService.ShouldUseFixedMode()

		// 【拉黑模式】：同 Provider 重试直到被拉黑，然后切换到下一个 Provider
		// 设计目标：Claude Code 单次请求最多重试 3 次，但拉黑阈值可能是 5
		// 通过内部重试机制，在单次请求中累积足够失败次数触发拉黑
		if blacklistEnabled {
			fmt.Printf("[INFO] 🔒 拉黑模式已开启（同 Provider 重试到拉黑再切换）\n")

			// 获取重试配置
			retryConfig := prs.blacklistService.GetRetryConfig()
			maxRetryPerProvider := retryConfig.FailureThreshold
			retryWaitSeconds := retryConfig.RetryWaitSeconds
			fmt.Printf("[INFO] 重试配置: 每 Provider 最多 %d 次重试，间隔 %d 秒\n",
				maxRetryPerProvider, retryWaitSeconds)

			var lastError error
			var lastProvider string
			totalAttempts := 0

			// 遍历所有 Level 和 Provider
			for _, level := range levels {
				providersInLevel := levelGroups[level]
				fmt.Printf("[INFO] === 尝试 Level %d（%d 个 provider）===\n", level, len(providersInLevel))

				for _, provider := range providersInLevel {
					// 检查是否已被拉黑（跳过已拉黑的 provider）
					if blacklisted, until := prs.blacklistService.IsBlacklisted(kind, provider.Name); blacklisted {
						fmt.Printf("[INFO] ⏭️ 跳过已拉黑的 Provider: %s (解禁时间: %v)\n", provider.Name, until)
						continue
					}

					// 获取实际模型名
					effectiveModel := provider.GetEffectiveModel(requestedModel)
					currentBodyBytes := bodyBytes
					if effectiveModel != requestedModel && requestedModel != "" {
						fmt.Printf("[INFO] Provider %s 映射模型: %s -> %s\n", provider.Name, requestedModel, effectiveModel)
						modifiedBody, err := ReplaceModelInRequestBody(bodyBytes, effectiveModel)
						if err != nil {
							fmt.Printf("[ERROR] 模型映射失败: %v，跳过此 Provider\n", err)
							continue
						}
						currentBodyBytes = modifiedBody
					}

					// 获取有效端点
					effectiveEndpoint := provider.GetEffectiveEndpoint(endpoint)

					// 同 Provider 内重试循环
					for retryCount := 0; retryCount < maxRetryPerProvider; retryCount++ {
						totalAttempts++

						// 再次检查是否已被拉黑（重试过程中可能被拉黑）
						if blacklisted, _ := prs.blacklistService.IsBlacklisted(kind, provider.Name); blacklisted {
							fmt.Printf("[INFO] 🚫 Provider %s 已被拉黑，切换到下一个\n", provider.Name)
							break
						}

						fmt.Printf("[INFO] [拉黑模式] Provider: %s (Level %d) | 尝试 %d/%d | Model: %s\n",
							provider.Name, level, retryCount+1, maxRetryPerProvider, effectiveModel)

						startTime := time.Now()
						// 根据 Provider 配置决定认证方式（auto 时自动检测原始请求）
						authMethod := determineAuthMethod(&provider, c.Request.Header)
						ok, err := prs.forwardRequest(c, kind, provider, effectiveEndpoint, query, clientHeaders, currentBodyBytes, isStream, effectiveModel, authMethod)
						duration := time.Since(startTime)

						if ok {
							fmt.Printf("[INFO] ✓ 成功: %s | 尝试 %d 次 | 耗时: %.2fs\n",
								provider.Name, retryCount+1, duration.Seconds())
							if err := prs.blacklistService.RecordSuccess(kind, provider.Name); err != nil {
								fmt.Printf("[WARN] 清零失败计数失败: %v\n", err)
							}
							prs.setLastUsedProvider(kind, provider.Name)
							return
						}

						// 失败处理
						lastError = err
						lastProvider = provider.Name

						errorMsg := "未知错误"
						if err != nil {
							errorMsg = err.Error()
						}
						fmt.Printf("[WARN] ✗ 失败: %s | 尝试 %d/%d | 错误: %s | 耗时: %.2fs\n",
							provider.Name, retryCount+1, maxRetryPerProvider, errorMsg, duration.Seconds())

						// 客户端中断不计入失败次数，直接返回
						if errors.Is(err, errClientAbort) {
							fmt.Printf("[INFO] 客户端中断，停止重试\n")
							return
						}

						// 记录失败次数（可能触发拉黑）
						if err := prs.blacklistService.RecordFailure(kind, provider.Name); err != nil {
							fmt.Printf("[ERROR] 记录失败到黑名单失败: %v\n", err)
						}

						// 检查是否刚被拉黑
						if blacklisted, _ := prs.blacklistService.IsBlacklisted(kind, provider.Name); blacklisted {
							fmt.Printf("[INFO] 🚫 Provider %s 达到失败阈值，已被拉黑，切换到下一个\n", provider.Name)
							break
						}

						// 等待后重试（除非是最后一次）
						if retryCount < maxRetryPerProvider-1 {
							fmt.Printf("[INFO] ⏳ 等待 %d 秒后重试...\n", retryWaitSeconds)
							time.Sleep(time.Duration(retryWaitSeconds) * time.Second)
						}
					}
				}
			}

			// 所有 Provider 都失败或被拉黑
			fmt.Printf("[ERROR] 💥 拉黑模式：所有 Provider 都失败或被拉黑（共尝试 %d 次）\n", totalAttempts)

			errorMsg := "未知错误"
			if lastError != nil {
				errorMsg = lastError.Error()
			}
			c.JSON(http.StatusBadGateway, gin.H{
				"error":         fmt.Sprintf("所有 Provider 都失败或被拉黑，最后尝试: %s - %s", lastProvider, errorMsg),
				"lastProvider":  lastProvider,
				"totalAttempts": totalAttempts,
				"mode":          "blacklist_retry",
				"hint":          "拉黑模式已开启，同 Provider 重试到拉黑再切换。如需立即降级请关闭拉黑功能",
			})
			return
		}

		// 【降级模式】：拉黑功能关闭，失败自动尝试下一个 provider
		roundRobinEnabled := prs.isRoundRobinEnabled()
		if roundRobinEnabled {
			fmt.Printf("[INFO] 🔄 降级模式 + 轮询负载均衡\n")
		} else {
			fmt.Printf("[INFO] 🔄 降级模式（顺序降级）\n")
		}

		var lastError error
		var lastProvider string
		var lastDuration time.Duration
		totalAttempts := 0

		// 【5分钟同源缓存】如果有缓存的 provider，优先尝试
		if cachedProviderName != "" {
			affinityResult := prs.tryAffinityProvider(
				c, kind, affinityKey, cachedProviderName, active,
				endpoint, query, clientHeaders, bodyBytes, isStream, requestedModel,
			)
			if affinityResult.Handled {
				return // 成功或客户端中断，不再继续
			}
			if affinityResult.UsedProvider != "" {
				totalAttempts++
				lastError = affinityResult.LastError
				lastProvider = affinityResult.UsedProvider
				lastDuration = affinityResult.Duration
			}
		}

		for _, level := range levels {
			providersInLevel := levelGroups[level]

			// 如果启用轮询，对同 Level 的 providers 进行轮询排序
			if roundRobinEnabled {
				providersInLevel = prs.roundRobinOrder(kind, level, providersInLevel)
			}

			fmt.Printf("[INFO] === 尝试 Level %d（%d 个 provider）===\n", level, len(providersInLevel))

			for i, provider := range providersInLevel {
				// 【5分钟同源缓存】跳过已经尝试过的缓存 provider
				if provider.Name == cachedProviderName {
					fmt.Printf("[INFO]   跳过已尝试的缓存 provider: %s\n", provider.Name)
					continue
				}

				totalAttempts++

				// 获取实际应该使用的模型名
				effectiveModel := provider.GetEffectiveModel(requestedModel)

				// 如果需要映射，修改请求体
				currentBodyBytes := bodyBytes
				if effectiveModel != requestedModel && requestedModel != "" {
					fmt.Printf("[INFO] Provider %s 映射模型: %s -> %s\n", provider.Name, requestedModel, effectiveModel)

					modifiedBody, err := ReplaceModelInRequestBody(bodyBytes, effectiveModel)
					if err != nil {
						fmt.Printf("[ERROR] 替换模型名失败: %v\n", err)
						// 映射失败不应阻止尝试其他 provider
						continue
					}
					currentBodyBytes = modifiedBody
				}

				fmt.Printf("[INFO]   [%d/%d] Provider: %s | Model: %s\n", i+1, len(providersInLevel), provider.Name, effectiveModel)

				// 尝试发送请求
				// 获取有效的端点（用户配置优先）
				effectiveEndpoint := provider.GetEffectiveEndpoint(endpoint)
				startTime := time.Now()
				// 根据 Provider 配置决定认证方式（auto 时自动检测原始请求）
				authMethod := determineAuthMethod(&provider, c.Request.Header)
				ok, err := prs.forwardRequest(c, kind, provider, effectiveEndpoint, query, clientHeaders, currentBodyBytes, isStream, effectiveModel, authMethod)
				duration := time.Since(startTime)

				if ok {
					fmt.Printf("[INFO]   ✓ Level %d 成功: %s | 耗时: %.2fs\n", level, provider.Name, duration.Seconds())

					// 【5分钟同源缓存】设置缓存亲和性
					if prs.affinityManager != nil {
						prs.affinityManager.Set(affinityKey, provider.Name)
					}

					// 成功：清零连续失败计数
					if err := prs.blacklistService.RecordSuccess(kind, provider.Name); err != nil {
						fmt.Printf("[WARN] 清零失败计数失败: %v\n", err)
					}

					// 记录最后使用的供应商
					prs.setLastUsedProvider(kind, provider.Name)

					return // 成功，立即返回
				}

				// 失败：记录错误并尝试下一个
				lastError = err
				lastProvider = provider.Name
				lastDuration = duration

				errorMsg := "未知错误"
				if err != nil {
					errorMsg = err.Error()
				}
				fmt.Printf("[WARN]   ✗ Level %d 失败: %s | 错误: %s | 耗时: %.2fs\n",
					level, provider.Name, errorMsg, duration.Seconds())

				// 客户端中断不计入失败次数
				if errors.Is(err, errClientAbort) {
					fmt.Printf("[INFO] 客户端中断，跳过失败计数: %s\n", provider.Name)
				} else if err := prs.blacklistService.RecordFailure(kind, provider.Name); err != nil {
					fmt.Printf("[ERROR] 记录失败到黑名单失败: %v\n", err)
				}

				// 发送切换通知：检查是否有下一个可用的 provider
				if prs.notificationService != nil {
					nextProvider := ""
					// 先查找同级别的下一个
					if i+1 < len(providersInLevel) {
						nextProvider = providersInLevel[i+1].Name
					} else {
						// 查找下一个 level 的第一个 provider
						for _, nextLevel := range levels {
							if nextLevel > level && len(levelGroups[nextLevel]) > 0 {
								nextProvider = levelGroups[nextLevel][0].Name
								break
							}
						}
					}
					if nextProvider != "" {
						prs.notificationService.NotifyProviderSwitch(SwitchNotification{
							FromProvider: provider.Name,
							ToProvider:   nextProvider,
							Reason:       errorMsg,
							Platform:     kind,
						})
					}
				}
			}

			fmt.Printf("[WARN] Level %d 的所有 %d 个 provider 均失败，尝试下一 Level\n", level, len(providersInLevel))
		}

		// 所有 provider 都失败，返回 502
		errorMsg := "未知错误"
		if lastError != nil {
			errorMsg = lastError.Error()
		}
		fmt.Printf("[ERROR] 所有 %d 个 provider 均失败，最后尝试: %s | 错误: %s\n",
			totalAttempts, lastProvider, errorMsg)

		c.JSON(http.StatusBadGateway, gin.H{
			"error":          fmt.Sprintf("所有 %d 个 provider 均失败，最后错误: %s", totalAttempts, errorMsg),
			"last_provider":  lastProvider,
			"last_duration":  fmt.Sprintf("%.2fs", lastDuration.Seconds()),
			"total_attempts": totalAttempts,
		})
	}
}

func (prs *ProviderRelayService) forwardRequest(
	c *gin.Context,
	kind string,
	provider Provider,
	endpoint string,
	query map[string]string,
	clientHeaders http.Header,
	bodyBytes []byte,
	isStream bool,
	model string,
	authMethod AuthMethod,
) (bool, error) {
	targetURL := joinURL(provider.APIURL, endpoint)

	// 添加查询参数（使用 url.Parse 进行正确的 URL 操作）
	if len(query) > 0 {
		u, err := url.Parse(targetURL)
		if err == nil {
			q := u.Query()
			for k, v := range query {
				q.Set(k, v)
			}
			u.RawQuery = q.Encode()
			targetURL = u.String()
		}
	}

	// 使用 buildForwardHeaders 构建转发请求头（支持 Provider 级别 Header 配置）
	// 按优先级处理：原请求 Headers → StripHeaders → OverrideHeaders → ExtraHeaders
	headers := buildForwardHeaders(clientHeaders, &provider)

	// 根据原始请求的认证方式设置转发请求头
	// 原始请求用 Authorization 就用 Authorization，原始请求用 x-api-key 就用 x-api-key
	// 注意：认证头最后设置，确保覆盖任何 OverrideHeaders 中的错误配置
	switch authMethod {
	case AuthMethodXAPIKey:
		// 原始请求使用 x-api-key，转发也使用 x-api-key
		headers.Set("X-Api-Key", provider.APIKey)
		// 删除可能存在的 Authorization 头
		headers.Del("Authorization")
	default:
		// 原始请求使用 Authorization Bearer，转发也使用 Authorization
		headers.Set("Authorization", fmt.Sprintf("Bearer %s", provider.APIKey))
		// 删除可能存在的 x-api-key 头
		headers.Del("X-Api-Key")
	}

	if headers.Get("Accept") == "" {
		headers.Set("Accept", "application/json")
	}

	requestLog := &RequestLog{
		Platform: kind,
		Provider: provider.Name,
		Model:    model,
		IsStream: isStream,
	}

	// 【请求详情缓存】准备响应收集器
	var responseCollector *strings.Builder
	var respHeaders map[string]string // 响应头（用于请求详情缓存）
	shouldRecordDetail := IsLoggingEnabled() &&
		GlobalRequestDetailCache != nil &&
		GlobalRequestDetailCache.GetMode() != RequestDetailModeOff

	start := time.Now()
	defer func() {
		requestLog.DurationSec = time.Since(start).Seconds()

		// 全局日志关闭：不记录任何日志（含 request_log / request_detail / MITM / console）
		if !IsLoggingEnabled() {
			return
		}

		// 【修复】判空保护：避免队列未初始化时 panic
		if GlobalDBQueueLogs == nil {
			fmt.Printf("⚠️  写入 request_log 失败: 队列未初始化\n")
			return
		}

		// 使用批量队列写入 request_log（高频同构操作，批量提交）
		_ = GlobalDBQueueLogs.EnqueueBatch(`
				INSERT INTO request_log (
					platform, model, provider, http_code,
					input_tokens, output_tokens, cache_create_tokens, cache_read_tokens,
					reasoning_tokens, is_stream, duration_sec
				) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			`,
			requestLog.Platform,
			requestLog.Model,
			requestLog.Provider,
			requestLog.HttpCode,
			requestLog.InputTokens,
			requestLog.OutputTokens,
			requestLog.CacheCreateTokens,
			requestLog.CacheReadTokens,
			requestLog.ReasoningTokens,
			boolToInt(requestLog.IsStream),
			requestLog.DurationSec,
		)

		// 【请求详情缓存】获取刚插入的 ID 并存储详情
		if shouldRecordDetail && GlobalRequestDetailCache.ShouldRecord(requestLog.HttpCode) {
			// 使用毫秒时间戳作为 ID（13位数字在 JavaScript 安全整数范围内）
			// 注意：UnixNano 是 19 位，超出 JS Number.MAX_SAFE_INTEGER (16位)，会丢失精度
			seqID := time.Now().UnixMilli()

			// 准备请求体（截断）
			reqBody, reqTruncated := TruncateBody(string(bodyBytes), MaxRequestBodySize)

			// 准备响应体（截断）
			respBody := ""
			respTruncated := false
			if responseCollector != nil {
				collectedData := responseCollector.String()

				// 检查响应是否是 gzip 压缩的，如果是则解压
				if respHeaders != nil {
					if encoding, ok := respHeaders["Content-Encoding"]; ok && strings.EqualFold(encoding, "gzip") {
						if decompressed, err := decompressGzip([]byte(collectedData)); err == nil {
							collectedData = string(decompressed)
						}
						// 解压失败时保留原始数据（可能显示乱码，但至少有数据）
					}
				}

				respBody, respTruncated = TruncateBody(collectedData, MaxResponseBodySize)
			}

			// 使用请求完成时的时间（与数据库 created_at 时间对齐，便于匹配）
			completedAt := time.Now()
			detail := &RequestDetail{
				SequenceID:      seqID,
				Platform:        kind,
				Provider:        provider.Name,
				Model:           model,
				RequestURL:      targetURL,
				RequestBody:     reqBody,
				ResponseBody:    respBody,
				Headers:         SanitizeHeaders(httpHeaderToMap(headers)),
				ResponseHeaders: respHeaders,
				HttpCode:        requestLog.HttpCode,
				Timestamp:       completedAt,
				DurationMs:      int64(requestLog.DurationSec * 1000),
				Truncated:       reqTruncated || respTruncated,
				RequestSize:     len(bodyBytes),
				ResponseSize:    len(respBody),
			}

			GlobalRequestDetailCache.Store(detail)
		}
	}()

	// 初始化响应收集器
	if shouldRecordDetail {
		responseCollector = &strings.Builder{}
	}

	// 某些中转（如 bigmodel/z.ai 的 Anthropic 兼容层）需要默认注入 thinking 参数以启用推理
	bodyBytes = prs.injectThinkingIfNeeded(bodyBytes, provider.APIURL)
	requestTimeout := 32 * time.Hour // 给模型足够思考时间

	// 使用标准 http.Client + http.NewRequestWithContext
	// 这能确保 context 取消时请求真正被中断
	reqCtx, cancelFunc := context.WithTimeout(c.Request.Context(), requestTimeout)
	defer cancelFunc()

	httpReq, reqErr := http.NewRequestWithContext(reqCtx, "POST", targetURL, bytes.NewReader(bodyBytes))
	if reqErr != nil {
		return false, reqErr
	}
	// 设置 GetBody 以支持 RetryTransport 重试时重置请求体
	httpReq.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(bodyBytes)), nil
	}
	// 复制 headers 到请求（headers 已经是 http.Header 类型）
	// Content-Type 已由 cloneHeaders() 从原请求复制，无需强制覆盖
	for k, values := range headers {
		for _, v := range values {
			httpReq.Header.Add(k, v)
		}
	}

	// 使用带网络级重试的 http.Client（超时由 context 控制）
	// 网络级重试：1 次，间隔 500ms，仅针对瞬时网络错误（TCP reset、DNS 失败等）
	// 应用层重试由外层 BlacklistService 控制，处理 API 级别错误
	httpClient := newRetryHTTPClient(kind, 1, 500*time.Millisecond)
	httpResp, err := httpClient.Do(httpReq)

	// 无论成功失败，先尝试记录 HttpCode
	if httpResp != nil {
		requestLog.HttpCode = httpResp.StatusCode
	}

	if err != nil {
		// 检查是否是 context 超时或取消
		if errors.Is(err, context.DeadlineExceeded) {
			fmt.Printf("[INFO] Provider %s 请求超时（context deadline exceeded）\n", provider.Name)
			return false, fmt.Errorf("request timeout: %w", err)
		}
		if errors.Is(err, context.Canceled) {
			fmt.Printf("[INFO] Provider %s 请求被取消（context canceled）\n", provider.Name)
			return false, fmt.Errorf("%w: %v", errClientAbort, err)
		}
		// 响应存在但状态码为0：可能是客户端中断
		if httpResp != nil && requestLog.HttpCode == 0 {
			fmt.Printf("[INFO] Provider %s 响应存在但状态码为0，判定为客户端中断\n", provider.Name)
			return false, fmt.Errorf("%w: %v", errClientAbort, err)
		}
		return false, err
	}

	// 检查响应是否存在
	if httpResp == nil {
		return false, fmt.Errorf("empty response")
	}
	defer httpResp.Body.Close()

	// 【请求详情缓存】保存响应头
	if shouldRecordDetail {
		respHeaders = make(map[string]string)
		for k, vv := range httpResp.Header {
			if len(vv) > 0 {
				respHeaders[k] = vv[0] // 只保存第一个值
			}
		}
	}

	status := httpResp.StatusCode
	requestLog.HttpCode = status

	// 状态码为 0 且无错误：当作成功处理
	// 注意：status=0 通常发生在以下场景：
	// 1. HTTP/2 或某些代理服务器在特殊情况下可能不设置状态码
	// 2. 连接成功但响应解析异常时的防御性处理
	// 3. 某些非标准HTTP实现的兼容性处理
	// 这是防御性编程，确保即使遇到异常状态码也能正常处理
	if status == 0 {
		fmt.Printf("[WARN] Provider %s 返回状态码 0，但无错误，当作成功处理\n", provider.Name)
		if copyErr := writeProxiedResponseWithCollector(c, httpResp, kind, requestLog, responseCollector); copyErr != nil {
			fmt.Printf("[WARN] 复制响应到客户端失败（不影响provider成功判定）: %v\n", copyErr)
		}
		// responseWritten: always true after writeProxiedResponseWithCollector returns
		return true, nil
	}

	if status >= http.StatusOK && status < http.StatusMultipleChoices {
		if copyErr := writeProxiedResponseWithCollector(c, httpResp, kind, requestLog, responseCollector); copyErr != nil {
			fmt.Printf("[WARN] 复制响应到客户端失败（不影响provider成功判定）: %v\n", copyErr)
		}
		// 只要provider返回了2xx状态码，就算成功（复制失败是客户端问题，不是provider问题）
		// responseWritten: always true after writeProxiedResponseWithCollector returns
		// 与 Gemini handler 的三元组 (success, errMsg, responseWritten) 语义一致：
		// 一旦响应头写入客户端，即使流式传输中断也不会触发重试
		return true, nil
	}

	// 对于非 2xx 响应，读取响应体用于错误调试和日志记录
	var respBody []byte
	if httpResp.Body != nil {
		respBody, _ = io.ReadAll(io.LimitReader(httpResp.Body, int64(MaxResponseBodySize)))
		if responseCollector != nil {
			responseCollector.Write(respBody)
		}
	}

	// 返回错误信息时包含上游响应体，便于调试和日志分析
	if len(respBody) > 0 {
		// 限制错误信息中的响应体长度，避免日志过长
		bodyPreview := string(respBody)
		if len(bodyPreview) > 500 {
			bodyPreview = bodyPreview[:500] + "...(truncated)"
		}
		return false, fmt.Errorf("upstream status %d: %s", status, bodyPreview)
	}
	return false, fmt.Errorf("upstream status %d", status)
}

// writeProxiedResponse 写入代理响应（复制响应头、状态码、响应体）
// 用于避免 forwardRequest 中 status 0 和 status 2xx 的重复代码
func writeProxiedResponse(c *gin.Context, httpResp *http.Response, kind string, requestLog *RequestLog) error {
	return writeProxiedResponseWithCollector(c, httpResp, kind, requestLog, nil)
}

// writeProxiedResponseWithCollector 写入代理响应，同时可选地收集响应内容
func writeProxiedResponseWithCollector(c *gin.Context, httpResp *http.Response, kind string, requestLog *RequestLog, collector *strings.Builder) error {
	// 复制响应头（过滤掉逐跳头）
	for k, vv := range httpResp.Header {
		if isHopByHopHeader(k) {
			continue
		}
		for _, v := range vv {
			c.Writer.Header().Add(k, v)
		}
	}
	c.Writer.WriteHeader(httpResp.StatusCode)

	// 非流式响应：一次性读取 body，解析 token 用量后再写回客户端。
	// 说明：
	// - 旧逻辑只在 SSE（data: ...）流里解析 usage；非流式 JSON 会导致 tokens 一直为 0，从而看板折线图“无数据”。
	// - 这里读取完整 body 进行解析；若 body 过大/非 JSON，则解析会自动跳过，不影响转发结果。
	if requestLog != nil && !requestLog.IsStream && !isEventStreamContentType(httpResp.Header.Get("Content-Type")) {
		body, err := io.ReadAll(httpResp.Body)
		if err != nil {
			return err
		}
		if collector != nil && len(body) > 0 {
			toWrite := len(body)
			if toWrite > MaxStreamResponseSize {
				toWrite = MaxStreamResponseSize
			}
			collector.Write(body[:toWrite])
		}
		parseTokenUsageFromNonStreamResponse(kind, httpResp.Header, body, requestLog)
		if len(body) > 0 {
			if _, err := c.Writer.Write(body); err != nil {
				return err
			}
			if flusher, ok := c.Writer.(http.Flusher); ok {
				flusher.Flush()
			}
		}
		return nil
	}

	// 流式复制 body 并通过 hook 提取 token 用量（SSE）
	hook := RequestLogHook(c, kind, requestLog)
	return copyResponseBodyWithHookAndCollector(httpResp.Body, c.Writer, hook, collector)
}

func isEventStreamContentType(contentType string) bool {
	value := strings.ToLower(strings.TrimSpace(contentType))
	return strings.Contains(value, "text/event-stream") || strings.Contains(value, "application/x-ndjson")
}

func looksLikeJSON(body []byte) bool {
	for _, b := range body {
		if b == ' ' || b == '\n' || b == '\r' || b == '\t' {
			continue
		}
		return b == '{' || b == '['
	}
	return false
}

func parseTokenUsageFromNonStreamResponse(kind string, headers http.Header, body []byte, usage *RequestLog) {
	if len(body) == 0 || usage == nil {
		return
	}

	parseBody := body
	if strings.EqualFold(strings.TrimSpace(headers.Get("Content-Encoding")), "gzip") {
		if decompressed, err := decompressGzip(body); err == nil && len(decompressed) > 0 {
			parseBody = decompressed
		}
	}
	if !looksLikeJSON(parseBody) {
		return
	}

	data := string(parseBody)
	switch kind {
	case "codex":
		CodexParseTokenUsageFromResponse(data, usage)
	case "gemini":
		// Gemini 非流式走独立链路（forwardGeminiRequest）；这里做兜底不影响。
		GeminiParseTokenUsageFromResponse(data, usage)
	default:
		// claude / custom / others：尽量复用 Claude 解析逻辑（两者都可能返回 usage.*）
		ClaudeCodeParseTokenUsageFromResponse(data, usage)
	}
}

// copyResponseBodyWithHook 流式复制响应 body 到 writer，同时调用 hook 处理数据
// 用于在流式传输过程中解析 SSE 数据提取 token 用量
func copyResponseBodyWithHook(body io.Reader, writer io.Writer, hook func([]byte) []byte) error {
	return copyResponseBodyWithHookAndCollector(body, writer, hook, nil)
}

// copyResponseBodyWithHookAndCollector 流式复制响应 body，同时可选地收集响应内容
func copyResponseBodyWithHookAndCollector(body io.Reader, writer io.Writer, hook func([]byte) []byte, collector *strings.Builder) error {
	buf := make([]byte, responseBufferSize)
	collectedSize := 0
	maxCollectSize := MaxStreamResponseSize

	for {
		n, readErr := body.Read(buf)
		if n > 0 {
			data := buf[:n]

			// 调用 hook 处理数据（用于解析 token 用量）
			// 使用 panic recovery 保护，避免 hook 异常导致服务器崩溃
			if hook != nil {
				data = safeCallHook(hook, data)
			}

			// 收集响应内容（用于请求详情缓存）
			if collector != nil && collectedSize < maxCollectSize {
				remaining := maxCollectSize - collectedSize
				toWrite := len(data)
				if toWrite > remaining {
					toWrite = remaining
				}
				collector.Write(data[:toWrite])
				collectedSize += toWrite
			}

			// 写入客户端
			if _, writeErr := writer.Write(data); writeErr != nil {
				return writeErr
			}

			// 对于流式响应，立即 flush
			if flusher, ok := writer.(http.Flusher); ok {
				flusher.Flush()
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				return nil
			}
			return readErr
		}
	}
}

// safeCallHook 安全地调用 hook 函数，捕获可能的 panic
// 如果 hook 发生 panic，记录错误并返回原始数据，不影响响应传输
func safeCallHook(hook func([]byte) []byte, data []byte) (result []byte) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("[ERROR] Hook panicked: %v\nStack: %s\n", r, debug.Stack())
			result = data // 返回原始数据，确保响应不中断
		}
	}()
	return hook(data)
}

// extractUserID 从请求头中提取 user_id（用于缓存亲和性）
// 通过对 Authorization header 中的 API Key 进行 hash 处理来生成唯一标识
func (prs *ProviderRelayService) extractUserID(c *gin.Context) string {
	// 使用 http.Header.Get 进行大小写无关的匹配
	authHeader := c.Request.Header.Get("Authorization")
	if authHeader == "" {
		authHeader = c.Request.Header.Get("X-Api-Key")
	}
	if authHeader == "" {
		return "anonymous"
	}
	// 移除 "Bearer " 前缀
	apiKey := strings.TrimPrefix(authHeader, "Bearer ")
	apiKey = strings.TrimSpace(apiKey)
	return HashAPIKey(apiKey)
}

// AffinityTryResult 缓存亲和性尝试结果
type AffinityTryResult struct {
	Handled      bool          // 是否已处理完成（成功或客户端中断）
	UsedProvider string        // 使用的 provider 名称
	LastError    error         // 最后的错误
	Duration     time.Duration // 耗时
}

// tryAffinityProvider 尝试使用缓存的 provider
// 封装了查找、尝试、成功刷新缓存、失败清除缓存的完整逻辑
// 返回 AffinityTryResult，调用方根据 Handled 判断是否需要继续降级
// 注意：认证方式由 Provider 配置决定，不再通过参数传递
func (prs *ProviderRelayService) tryAffinityProvider(
	c *gin.Context,
	kind string,
	affinityKey string,
	cachedProviderName string,
	activeProviders []Provider,
	endpoint string,
	query map[string]string,
	clientHeaders http.Header,
	bodyBytes []byte,
	isStream bool,
	requestedModel string,
) AffinityTryResult {
	result := AffinityTryResult{}

	if cachedProviderName == "" {
		return result
	}

	fmt.Printf("[INFO] 🎯 发现缓存的 provider: %s，优先尝试\n", cachedProviderName)

	// 查找缓存的 provider
	var cachedProvider *Provider
	for i := range activeProviders {
		if activeProviders[i].Name == cachedProviderName {
			cachedProvider = &activeProviders[i]
			break
		}
	}

	if cachedProvider == nil {
		fmt.Printf("[INFO] 缓存的 provider %s 不在可用列表中，跳过\n", cachedProviderName)
		return result
	}

	result.UsedProvider = cachedProvider.Name

	// 准备请求
	effectiveModel := cachedProvider.GetEffectiveModel(requestedModel)
	currentBodyBytes := bodyBytes

	if effectiveModel != requestedModel && requestedModel != "" {
		fmt.Printf("[INFO] Provider %s 映射模型: %s -> %s\n", cachedProvider.Name, requestedModel, effectiveModel)
		modifiedBody, modErr := ReplaceModelInRequestBody(bodyBytes, effectiveModel)
		if modErr == nil {
			currentBodyBytes = modifiedBody
		}
	}

	effectiveEndpoint := cachedProvider.GetEffectiveEndpoint(endpoint)
	startTime := time.Now()
	// 根据 Provider 配置决定认证方式（auto 时自动检测原始请求）
	effectiveAuthMethod := determineAuthMethod(cachedProvider, c.Request.Header)
	ok, err := prs.forwardRequest(c, kind, *cachedProvider, effectiveEndpoint, query, clientHeaders, currentBodyBytes, isStream, effectiveModel, effectiveAuthMethod)
	result.Duration = time.Since(startTime)

	if ok {
		fmt.Printf("[INFO] ✓ 缓存命中成功: %s | 耗时: %.2fs\n", cachedProvider.Name, result.Duration.Seconds())

		// 刷新缓存（延长 TTL）
		if prs.affinityManager != nil {
			prs.affinityManager.Set(affinityKey, cachedProvider.Name)
		}

		if recErr := prs.blacklistService.RecordSuccess(kind, cachedProvider.Name); recErr != nil {
			fmt.Printf("[WARN] 清零失败计数失败: %v\n", recErr)
		}
		prs.setLastUsedProvider(kind, cachedProvider.Name)
		result.Handled = true
		return result
	}

	// 缓存的 provider 失败，清除缓存
	fmt.Printf("[WARN] ✗ 缓存的 provider 失败: %s | 错误: %v | 耗时: %.2fs\n",
		cachedProvider.Name, err, result.Duration.Seconds())
	if prs.affinityManager != nil {
		prs.affinityManager.Invalidate(affinityKey)
	}

	result.LastError = err

	// 客户端中断不计入失败次数
	if errors.Is(err, errClientAbort) {
		result.Handled = true // 客户端中断，不再继续降级
		return result
	}

	if recErr := prs.blacklistService.RecordFailure(kind, cachedProvider.Name); recErr != nil {
		fmt.Printf("[ERROR] 记录失败到黑名单失败: %v\n", recErr)
	}

	return result
}

// GeminiAffinityTryResult Gemini 缓存亲和性尝试结果
type GeminiAffinityTryResult struct {
	Handled         bool   // 是否已处理完成（成功或响应已写入）
	UsedProvider    string // 使用的 provider 名称
	LastError       string // 最后的错误信息
	ResponseWritten bool   // 响应是否已部分写入客户端
}

// tryGeminiAffinityProvider 尝试使用缓存的 Gemini provider
// 封装了查找、尝试、成功刷新缓存、失败清除缓存的完整逻辑
func (prs *ProviderRelayService) tryGeminiAffinityProvider(
	c *gin.Context,
	affinityKey string,
	cachedProviderName string,
	activeProviders []GeminiProvider,
	endpoint string,
	bodyBytes []byte,
	isStream bool,
	requestLog *RequestLog,
	startTime time.Time,
) GeminiAffinityTryResult {
	result := GeminiAffinityTryResult{}

	if cachedProviderName == "" {
		return result
	}

	fmt.Printf("[Gemini] 🎯 发现缓存的 provider: %s，优先尝试\n", cachedProviderName)

	// 查找缓存的 provider
	var cachedProvider *GeminiProvider
	for i := range activeProviders {
		if activeProviders[i].Name == cachedProviderName {
			cachedProvider = &activeProviders[i]
			break
		}
	}

	if cachedProvider == nil {
		fmt.Printf("[Gemini] 缓存的 provider %s 不在可用列表中，跳过\n", cachedProviderName)
		return result
	}

	result.UsedProvider = cachedProvider.Name
	requestLog.Provider = cachedProvider.Name
	requestLog.Model = cachedProvider.Model

	ok, errMsg, responseWritten := prs.forwardGeminiRequest(c, cachedProvider, endpoint, bodyBytes, isStream, requestLog)
	result.ResponseWritten = responseWritten

	if ok {
		// 刷新缓存（延长 TTL）
		if prs.affinityManager != nil {
			prs.affinityManager.Set(affinityKey, cachedProvider.Name)
		}
		_ = prs.blacklistService.RecordSuccess("gemini", cachedProvider.Name)
		prs.setLastUsedProvider("gemini", cachedProvider.Name)
		fmt.Printf("[Gemini] ✓ 缓存命中成功 | Provider: %s | 总耗时: %.2fs\n", cachedProvider.Name, time.Since(startTime).Seconds())
		result.Handled = true
		return result
	}

	// 缓存的 provider 失败，清除缓存
	fmt.Printf("[Gemini] ⚠️ 缓存的 provider 失败: %s | 错误: %s\n", cachedProvider.Name, errMsg)
	if prs.affinityManager != nil {
		prs.affinityManager.Invalidate(affinityKey)
	}
	_ = prs.blacklistService.RecordFailure("gemini", cachedProvider.Name)
	result.LastError = errMsg

	if responseWritten {
		fmt.Printf("[Gemini] ⚠️ 响应已部分写入，无法降级: %s\n", cachedProvider.Name)
		result.Handled = true
	}

	return result
}

// cloneHeaders 克隆请求头，返回 http.Header 类型
// 过滤掉认证相关的头（Authorization, x-api-key, x-goog-api-key），因为转发时会根据原始请求的认证方式重新设置
// 同时过滤掉 hop-by-hop headers
func cloneHeaders(header http.Header) http.Header {
	cloned := make(http.Header)
	for key, values := range header {
		// 使用 http.CanonicalHeaderKey 进行标准化比较
		canonicalKey := http.CanonicalHeaderKey(key)

		// 跳过认证相关的头（会在转发时根据 authMethod 重新设置）
		// 包括：Authorization (Bearer), X-Api-Key (Anthropic), X-Goog-Api-Key (Gemini)
		if canonicalKey == "Authorization" || canonicalKey == "X-Api-Key" || canonicalKey == "X-Goog-Api-Key" {
			continue
		}

		// 跳过 hop-by-hop headers
		if hopByHopHeaders[canonicalKey] {
			continue
		}

		// 复制所有值
		cloned[canonicalKey] = append([]string(nil), values...)
	}
	return cloned
}

// buildForwardHeaders 构建转发请求的 Headers（不含认证头）
// 按优先级处理：1. 复制原请求 Headers → 2. 移除 StripHeaders → 3. 应用 OverrideHeaders → 4. 应用 ExtraHeaders
// 认证头由调用方根据 Provider.APIKey + authMethod 单独设置
func buildForwardHeaders(original http.Header, provider *Provider) http.Header {
	// 复制 headers（如果 original 已经是 cloneHeaders 的结果，此处仅做浅拷贝开销可接受）
	headers := make(http.Header, len(original))
	for k, v := range original {
		headers[k] = append([]string(nil), v...)
	}

	if provider == nil {
		return headers
	}

	// Step 2: 移除指定 Headers
	for _, h := range provider.StripHeaders {
		headers.Del(h)
	}

	// Step 3: 强制覆盖 (注意：不应包含认证头，认证头会被调用方覆盖)
	for k, v := range provider.OverrideHeaders {
		headers.Set(k, v)
	}

	// Step 4: 额外添加（仅当 key 不存在时添加）
	for k, v := range provider.ExtraHeaders {
		if headers.Get(k) == "" {
			headers.Set(k, v)
		}
	}

	return headers
}

func cloneMap(m map[string]string) map[string]string {
	cloned := make(map[string]string, len(m))
	for k, v := range m {
		cloned[k] = v
	}
	return cloned
}

func flattenQuery(values map[string][]string) map[string]string {
	query := make(map[string]string, len(values))
	for key, items := range values {
		if len(items) > 0 {
			query[key] = items[len(items)-1]
		}
	}
	return query
}

func joinURL(base string, endpoint string) string {
	base = strings.TrimSuffix(base, "/")
	endpoint = "/" + strings.TrimPrefix(endpoint, "/")
	return base + endpoint
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func ensureRequestLogColumn(db *sql.DB, column string, definition string) error {
	query := fmt.Sprintf("SELECT COUNT(*) FROM pragma_table_info('request_log') WHERE name = '%s'", column)
	var count int
	if err := db.QueryRow(query).Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		alter := fmt.Sprintf("ALTER TABLE request_log ADD COLUMN %s %s", column, definition)
		if _, err := db.Exec(alter); err != nil {
			return err
		}
	}
	return nil
}

func ensureRequestLogTable() error {
	db, err := xdb.DB("default")
	if err != nil {
		return err
	}
	return ensureRequestLogTableWithDB(db)
}

func ensureRequestLogTableWithDB(db *sql.DB) error {
	const createTableSQL = `CREATE TABLE IF NOT EXISTS request_log (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		platform TEXT,
		model TEXT,
		provider TEXT,
		http_code INTEGER,
		input_tokens INTEGER,
		output_tokens INTEGER,
		cache_create_tokens INTEGER,
		cache_read_tokens INTEGER,
		reasoning_tokens INTEGER,
		is_stream INTEGER DEFAULT 0,
		duration_sec REAL DEFAULT 0,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`

	if _, err := db.Exec(createTableSQL); err != nil {
		return err
	}

	if err := ensureRequestLogColumn(db, "created_at", "DATETIME DEFAULT CURRENT_TIMESTAMP"); err != nil {
		return err
	}
	if err := ensureRequestLogColumn(db, "is_stream", "INTEGER DEFAULT 0"); err != nil {
		return err
	}
	if err := ensureRequestLogColumn(db, "duration_sec", "REAL DEFAULT 0"); err != nil {
		return err
	}

	return nil
}

// RequestLogHook SSE 钩子：解析 token 用量，返回原始数据（不做修改）
func RequestLogHook(c *gin.Context, kind string, usage *RequestLog) func(data []byte) []byte {
	parserFn := ClaudeCodeParseTokenUsageFromResponse
	switch kind {
	case "codex":
		parserFn = CodexParseTokenUsageFromResponse
	case "gemini":
		parserFn = GeminiParseTokenUsageFromResponse
	}

	// SSE 数据可能被 TCP 分割到多个 chunk，不能对单个 chunk 直接 strings.Split。
	// 这里做一个轻量行缓冲：按行（\n）拼接，遇到 "data:" 行再交给 parser 解析。
	const maxSSEBufferSize = 256 * 1024
	buf := make([]byte, 0, 8*1024)

	return func(data []byte) []byte {
		if usage == nil || len(data) == 0 {
			return data
		}

		buf = append(buf, data...)
		if len(buf) > maxSSEBufferSize {
			buf = buf[len(buf)-maxSSEBufferSize:]
		}

		for {
			idx := bytes.IndexByte(buf, '\n')
			if idx == -1 {
				break
			}
			line := strings.TrimSpace(string(buf[:idx]))
			buf = buf[idx+1:]
			if strings.HasPrefix(line, "data:") {
				parserFn(strings.TrimSpace(strings.TrimPrefix(line, "data:")), usage)
			}
		}

		return data
	}
}

type RequestLog struct {
	ID                int64   `json:"id"`
	Platform          string  `json:"platform"` // claude、codex 或 gemini
	Model             string  `json:"model"`
	Provider          string  `json:"provider"` // provider name
	HttpCode          int     `json:"http_code"`
	InputTokens       int     `json:"input_tokens"`
	OutputTokens      int     `json:"output_tokens"`
	CacheCreateTokens int     `json:"cache_create_tokens"`
	CacheReadTokens   int     `json:"cache_read_tokens"`
	ReasoningTokens   int     `json:"reasoning_tokens"`
	IsStream          bool    `json:"is_stream"`
	DurationSec       float64 `json:"duration_sec"`
	CreatedAt         string  `json:"created_at"`
	InputCost         float64 `json:"input_cost"`
	OutputCost        float64 `json:"output_cost"`
	ReasoningCost     float64 `json:"reasoning_cost"`
	CacheCreateCost   float64 `json:"cache_create_cost"`
	CacheReadCost     float64 `json:"cache_read_cost"`
	Ephemeral5mCost   float64 `json:"ephemeral_5m_cost"`
	Ephemeral1hCost   float64 `json:"ephemeral_1h_cost"`
	TotalCost         float64 `json:"total_cost"`
	HasPricing        bool    `json:"has_pricing"`
	RequestDetailID   int64   `json:"request_detail_id,omitempty"` // 关联的请求详情 ID（内存缓存）
}

// claude code usage parser
func ClaudeCodeParseTokenUsageFromResponse(data string, usage *RequestLog) {
	usage.InputTokens += int(gjson.Get(data, "message.usage.input_tokens").Int())
	usage.OutputTokens += int(gjson.Get(data, "message.usage.output_tokens").Int())
	usage.CacheCreateTokens += int(gjson.Get(data, "message.usage.cache_creation_input_tokens").Int())
	usage.CacheReadTokens += int(gjson.Get(data, "message.usage.cache_read_input_tokens").Int())

	usage.InputTokens += int(gjson.Get(data, "usage.input_tokens").Int())
	usage.OutputTokens += int(gjson.Get(data, "usage.output_tokens").Int())
}

// codex usage parser
func CodexParseTokenUsageFromResponse(data string, usage *RequestLog) {
	// OpenAI Responses API (stream chunk): response.usage.*
	usage.InputTokens += int(gjson.Get(data, "response.usage.input_tokens").Int())
	usage.OutputTokens += int(gjson.Get(data, "response.usage.output_tokens").Int())
	usage.CacheReadTokens += int(gjson.Get(data, "response.usage.input_tokens_details.cached_tokens").Int())
	usage.ReasoningTokens += int(gjson.Get(data, "response.usage.output_tokens_details.reasoning_tokens").Int())

	// OpenAI Responses API (non-stream): usage.*
	usage.InputTokens += int(gjson.Get(data, "usage.input_tokens").Int())
	usage.OutputTokens += int(gjson.Get(data, "usage.output_tokens").Int())
	usage.CacheReadTokens += int(gjson.Get(data, "usage.input_tokens_details.cached_tokens").Int())
	usage.ReasoningTokens += int(gjson.Get(data, "usage.output_tokens_details.reasoning_tokens").Int())

	// OpenAI Chat Completions API: usage.prompt_tokens / usage.completion_tokens
	usage.InputTokens += int(gjson.Get(data, "usage.prompt_tokens").Int())
	usage.OutputTokens += int(gjson.Get(data, "usage.completion_tokens").Int())
	usage.CacheReadTokens += int(gjson.Get(data, "usage.prompt_tokens_details.cached_tokens").Int())
	usage.ReasoningTokens += int(gjson.Get(data, "usage.completion_tokens_details.reasoning_tokens").Int())
}

// gemini usage parser (流式响应专用)
// Gemini SSE 流中每个 chunk 都会携带完整的 usageMetadata，需取最大值而非累加
func GeminiParseTokenUsageFromResponse(data string, usage *RequestLog) {
	usageResult := gjson.Get(data, "usageMetadata")
	if !usageResult.Exists() {
		return
	}
	mergeGeminiUsageMetadata(usageResult, usage)
}

// mergeGeminiUsageMetadata 合并 Gemini usageMetadata 到 RequestLog（取最大值去重）
// Gemini 流式响应特点：每个 chunk 包含截止当前的累计用量，因此取最大值即可
func mergeGeminiUsageMetadata(usage gjson.Result, reqLog *RequestLog) {
	if !usage.Exists() || reqLog == nil {
		return
	}

	// 取最大值（流式响应中后续 chunk 包含前面的累计值）
	if v := int(usage.Get("promptTokenCount").Int()); v > reqLog.InputTokens {
		reqLog.InputTokens = v
	}
	if v := int(usage.Get("candidatesTokenCount").Int()); v > reqLog.OutputTokens {
		reqLog.OutputTokens = v
	}
	if v := int(usage.Get("cachedContentTokenCount").Int()); v > reqLog.CacheReadTokens {
		reqLog.CacheReadTokens = v
	}
	// Gemini thinking/reasoning tokens (thoughtsTokenCount)
	// 参考: https://ai.google.dev/gemini-api/docs/thinking
	if v := int(usage.Get("thoughtsTokenCount").Int()); v > reqLog.ReasoningTokens {
		reqLog.ReasoningTokens = v
	}

	// 若仅提供 totalTokenCount，按 total - input 估算输出 token
	total := usage.Get("totalTokenCount").Int()
	if total > 0 && reqLog.OutputTokens == 0 && reqLog.InputTokens > 0 && reqLog.InputTokens < int(total) {
		reqLog.OutputTokens = int(total) - reqLog.InputTokens
	}
}

// streamGeminiResponseWithHook 流式传输 Gemini 响应并通过 Hook 提取 token 用量
// 【修复】维护跨 chunk 缓冲，确保完整 SSE 事件解析
// Gemini SSE 格式: "data: {json}\n\n" 或 "data: [DONE]\n\n"
func streamGeminiResponseWithHook(body io.Reader, writer io.Writer, requestLog *RequestLog) error {
	buf := make([]byte, 8192)   // 增大缓冲区减少系统调用
	var lineBuf strings.Builder // 跨 chunk 行缓冲

	for {
		n, err := body.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			// 写入客户端（优先保证数据传输）
			if _, writeErr := writer.Write(chunk); writeErr != nil {
				return writeErr
			}
			// 如果是 http.Flusher，立即刷新
			if flusher, ok := writer.(http.Flusher); ok {
				flusher.Flush()
			}
			// 解析 SSE 数据提取 token 用量（使用缓冲处理跨 chunk 情况）
			parseGeminiSSEWithBuffer(string(chunk), &lineBuf, requestLog)
		}
		if err != nil {
			// 处理缓冲区残留数据
			if lineBuf.Len() > 0 {
				parseGeminiSSELine(lineBuf.String(), requestLog)
				lineBuf.Reset()
			}
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}

// parseGeminiSSEWithBuffer 使用缓冲处理跨 chunk 的 SSE 事件
// 【修复】解决 JSON 被 TCP 分割到多个 chunk 导致解析失败的问题
func parseGeminiSSEWithBuffer(chunk string, lineBuf *strings.Builder, requestLog *RequestLog) {
	// 将当前 chunk 追加到缓冲
	lineBuf.WriteString(chunk)
	content := lineBuf.String()

	// 按双换行符分割完整的 SSE 事件
	// SSE 格式: "data: {...}\n\n" 或 "data: {...}\r\n\r\n"
	for {
		// 查找事件分隔符（双换行）
		idx := strings.Index(content, "\n\n")
		if idx == -1 {
			// 尝试 \r\n\r\n 分隔符
			idx = strings.Index(content, "\r\n\r\n")
			if idx == -1 {
				break // 没有完整事件，等待更多数据
			}
			idx += 4 // \r\n\r\n 长度
		} else {
			idx += 2 // \n\n 长度
		}

		// 提取完整事件
		event := content[:idx]
		content = content[idx:]

		// 解析事件中的 data 行
		parseGeminiSSELine(event, requestLog)
	}

	// 更新缓冲区为未处理的残留数据
	lineBuf.Reset()
	lineBuf.WriteString(content)
}

// parseGeminiSSELine 解析单个 SSE 事件提取 usageMetadata
// 【优化】只在包含 usageMetadata 时才调用 gjson 解析
func parseGeminiSSELine(event string, requestLog *RequestLog) {
	lines := strings.Split(event, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" || data == "" {
			continue
		}
		// 【优化】快速检查是否包含 usageMetadata，避免无效解析
		if !strings.Contains(data, "usageMetadata") {
			continue
		}
		GeminiParseTokenUsageFromResponse(data, requestLog)
	}
}

// ReplaceModelInRequestBody 替换请求体中的模型名
// 使用 gjson + sjson 实现高性能 JSON 操作，避免完整反序列化
func ReplaceModelInRequestBody(bodyBytes []byte, newModel string) ([]byte, error) {
	// 检查请求体中是否存在 model 字段
	result := gjson.GetBytes(bodyBytes, "model")
	if !result.Exists() {
		return bodyBytes, fmt.Errorf("请求体中未找到 model 字段")
	}

	// 使用 sjson.SetBytes 替换模型名（高性能操作）
	modified, err := sjson.SetBytes(bodyBytes, "model", newModel)
	if err != nil {
		return bodyBytes, fmt.Errorf("替换模型名失败: %w", err)
	}

	return modified, nil
}

// geminiProxyHandler 处理 Gemini API 请求（支持 Level 分组降级和黑名单）
func (prs *ProviderRelayService) geminiProxyHandler(apiVersion string) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 获取完整路径（例如 /v1beta/models/gemini-2.5-pro:generateContent）
		fullPath := c.Param("any")
		endpoint := apiVersion + fullPath

		// 保留查询参数（如 ?alt=sse, ?key= 等）
		query := c.Request.URL.RawQuery
		if query != "" {
			endpoint = endpoint + "?" + query
		}

		fmt.Printf("[Gemini] 收到请求: %s\n", endpoint)

		// 读取请求体
		var bodyBytes []byte
		if c.Request.Body != nil {
			data, err := io.ReadAll(c.Request.Body)
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
				return
			}
			bodyBytes = data
			c.Request.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		}

		// 判断是否为流式请求
		isStream := strings.Contains(endpoint, ":streamGenerateContent") || strings.Contains(query, "alt=sse")

		// 【5分钟同源缓存】提取 user_id 和模型名
		userID := prs.extractUserID(c)
		geminiModel := extractGeminiModelFromEndpoint(endpoint)
		affinityKey := GenerateAffinityKey(userID, "gemini", geminiModel)

		// 加载 Gemini providers
		providers := prs.geminiService.GetProviders()
		if len(providers) == 0 {
			c.JSON(http.StatusNotFound, gin.H{"error": "no gemini providers configured"})
			return
		}

		// 1. 过滤可用的 providers（启用 + BaseURL 配置 + 未被拉黑）
		var activeProviders []GeminiProvider
		for _, p := range providers {
			if !p.Enabled || p.BaseURL == "" {
				continue
			}
			// 检查黑名单
			if isBlacklisted, until := prs.blacklistService.IsBlacklisted("gemini", p.Name); isBlacklisted {
				fmt.Printf("[Gemini] ⛔ Provider %s 已拉黑，过期时间: %v\n", p.Name, until.Format("15:04:05"))
				continue
			}
			// Level 默认值处理
			if p.Level <= 0 {
				p.Level = 1
			}
			activeProviders = append(activeProviders, p)
		}

		if len(activeProviders) == 0 {
			c.JSON(http.StatusNotFound, gin.H{"error": "no active gemini provider (all disabled or blacklisted)"})
			return
		}

		// 2. 按 Level 分组
		levelGroups := make(map[int][]GeminiProvider)
		for _, p := range activeProviders {
			levelGroups[p.Level] = append(levelGroups[p.Level], p)
		}

		// 获取排序后的 Level 列表
		var sortedLevels []int
		for level := range levelGroups {
			sortedLevels = append(sortedLevels, level)
		}
		sort.Ints(sortedLevels)

		fmt.Printf("[Gemini] 共 %d 个 Level 分组: %v\n", len(sortedLevels), sortedLevels)

		// 请求日志
		requestLog := &RequestLog{
			Platform:     "gemini",
			IsStream:     isStream,
			InputTokens:  0,
			OutputTokens: 0,
		}
		start := time.Now()

		// 保存日志的 defer
		defer func() {
			requestLog.DurationSec = time.Since(start).Seconds()

			// 全局日志关闭：不记录任何日志
			if !IsLoggingEnabled() {
				return
			}

			if GlobalDBQueueLogs == nil {
				return
			}
			_ = GlobalDBQueueLogs.EnqueueBatch(`
					INSERT INTO request_log (
						platform, model, provider, http_code,
						input_tokens, output_tokens, cache_create_tokens, cache_read_tokens,
						reasoning_tokens, is_stream, duration_sec
					) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
				`,
				requestLog.Platform, requestLog.Model, requestLog.Provider, requestLog.HttpCode,
				requestLog.InputTokens, requestLog.OutputTokens, requestLog.CacheCreateTokens,
				requestLog.CacheReadTokens, requestLog.ReasoningTokens,
				boolToInt(requestLog.IsStream), requestLog.DurationSec,
			)
		}()

		// 获取拉黑功能开关状态
		blacklistEnabled := prs.blacklistService.ShouldUseFixedMode()

		// 【拉黑模式】：同 Provider 重试直到被拉黑，然后切换到下一个 Provider
		if blacklistEnabled {
			fmt.Printf("[Gemini] 🔒 拉黑模式已开启（同 Provider 重试到拉黑再切换）\n")

			// 获取重试配置
			retryConfig := prs.blacklistService.GetRetryConfig()
			maxRetryPerProvider := retryConfig.FailureThreshold
			retryWaitSeconds := retryConfig.RetryWaitSeconds
			fmt.Printf("[Gemini] 重试配置: 每 Provider 最多 %d 次重试，间隔 %d 秒\n",
				maxRetryPerProvider, retryWaitSeconds)

			var lastError string
			var lastProvider string
			totalAttempts := 0

			// 遍历所有 Level 和 Provider
			for _, level := range sortedLevels {
				providersInLevel := levelGroups[level]
				fmt.Printf("[Gemini] === 尝试 Level %d（%d 个 provider）===\n", level, len(providersInLevel))

				for _, provider := range providersInLevel {
					// 检查是否已被拉黑（跳过已拉黑的 provider）
					if blacklisted, until := prs.blacklistService.IsBlacklisted("gemini", provider.Name); blacklisted {
						fmt.Printf("[Gemini] ⏭️ 跳过已拉黑的 Provider: %s (解禁时间: %v)\n", provider.Name, until)
						continue
					}

					// 预填日志
					requestLog.Provider = provider.Name
					requestLog.Model = provider.Model

					// 同 Provider 内重试循环
					for retryCount := 0; retryCount < maxRetryPerProvider; retryCount++ {
						totalAttempts++

						// 再次检查是否已被拉黑（重试过程中可能被拉黑）
						if blacklisted, _ := prs.blacklistService.IsBlacklisted("gemini", provider.Name); blacklisted {
							fmt.Printf("[Gemini] 🚫 Provider %s 已被拉黑，切换到下一个\n", provider.Name)
							break
						}

						fmt.Printf("[Gemini] [拉黑模式] Provider: %s (Level %d) | 尝试 %d/%d\n",
							provider.Name, level, retryCount+1, maxRetryPerProvider)

						ok, errMsg, responseWritten := prs.forwardGeminiRequest(c, &provider, endpoint, bodyBytes, isStream, requestLog)
						if ok {
							fmt.Printf("[Gemini] ✓ 成功: %s | 尝试 %d 次\n", provider.Name, retryCount+1)
							_ = prs.blacklistService.RecordSuccess("gemini", provider.Name)
							prs.setLastUsedProvider("gemini", provider.Name)
							return
						}

						// 【关键修复】如果响应已写入客户端，不能重试或降级，直接返回
						if responseWritten {
							fmt.Printf("[Gemini] ⚠️ 响应已部分写入，无法重试: %s | 错误: %s\n", provider.Name, errMsg)
							_ = prs.blacklistService.RecordFailure("gemini", provider.Name)
							return
						}

						// 失败处理
						lastError = errMsg
						lastProvider = provider.Name

						fmt.Printf("[Gemini] ✗ 失败: %s | 尝试 %d/%d | 错误: %s\n",
							provider.Name, retryCount+1, maxRetryPerProvider, errMsg)

						// 记录失败次数（可能触发拉黑）
						_ = prs.blacklistService.RecordFailure("gemini", provider.Name)

						// 检查是否刚被拉黑
						if blacklisted, _ := prs.blacklistService.IsBlacklisted("gemini", provider.Name); blacklisted {
							fmt.Printf("[Gemini] 🚫 Provider %s 达到失败阈值，已被拉黑，切换到下一个\n", provider.Name)
							break
						}

						// 等待后重试（除非是最后一次）
						if retryCount < maxRetryPerProvider-1 {
							fmt.Printf("[Gemini] ⏳ 等待 %d 秒后重试...\n", retryWaitSeconds)
							time.Sleep(time.Duration(retryWaitSeconds) * time.Second)
						}
					}
				}
			}

			// 所有 Provider 都失败或被拉黑
			fmt.Printf("[Gemini] 💥 拉黑模式：所有 Provider 都失败或被拉黑（共尝试 %d 次）\n", totalAttempts)

			if requestLog.HttpCode == 0 {
				requestLog.HttpCode = http.StatusBadGateway
			}
			c.JSON(http.StatusBadGateway, gin.H{
				"error":         fmt.Sprintf("所有 Provider 都失败或被拉黑，最后尝试: %s - %s", lastProvider, lastError),
				"lastProvider":  lastProvider,
				"totalAttempts": totalAttempts,
				"mode":          "blacklist_retry",
				"hint":          "拉黑模式已开启，同 Provider 重试到拉黑再切换。如需立即降级请关闭拉黑功能",
			})
			return
		}

		// 【降级模式】：按 Level 顺序尝试所有 provider
		roundRobinEnabled := prs.isRoundRobinEnabled()
		if roundRobinEnabled {
			fmt.Printf("[Gemini] 🔄 降级模式 + 轮询负载均衡\n")
		} else {
			fmt.Printf("[Gemini] 🔄 降级模式（顺序降级）\n")
		}

		var lastError string

		// 【5分钟同源缓存】检查是否有缓存的 provider
		cachedProviderName := ""
		if prs.affinityManager != nil {
			cachedProviderName = prs.affinityManager.Get(affinityKey)
		}

		// 【5分钟同源缓存】如果有缓存的 provider，优先尝试
		if cachedProviderName != "" {
			affinityResult := prs.tryGeminiAffinityProvider(
				c, affinityKey, cachedProviderName, activeProviders,
				endpoint, bodyBytes, isStream, requestLog, start,
			)
			if affinityResult.Handled {
				return // 成功或响应已写入，不再继续
			}
			if affinityResult.UsedProvider != "" {
				lastError = affinityResult.LastError
			}
		}

		for _, level := range sortedLevels {
			providersInLevel := levelGroups[level]

			// 如果启用轮询，对同 Level 的 providers 进行轮询排序
			if roundRobinEnabled {
				providersInLevel = prs.roundRobinOrderGemini(level, providersInLevel)
			}

			fmt.Printf("[Gemini] === 尝试 Level %d（%d 个 provider）===\n", level, len(providersInLevel))

			for idx, provider := range providersInLevel {
				// 【5分钟同源缓存】跳过已经尝试过的缓存 provider
				if provider.Name == cachedProviderName {
					fmt.Printf("[Gemini]   跳过已尝试的缓存 provider: %s\n", provider.Name)
					continue
				}

				fmt.Printf("[Gemini]   [%d/%d] Provider: %s\n", idx+1, len(providersInLevel), provider.Name)

				// 预填日志，失败也能落库
				requestLog.Provider = provider.Name
				requestLog.Model = provider.Model

				ok, errMsg, responseWritten := prs.forwardGeminiRequest(c, &provider, endpoint, bodyBytes, isStream, requestLog)
				if ok {
					// 【5分钟同源缓存】设置缓存亲和性
					if prs.affinityManager != nil {
						prs.affinityManager.Set(affinityKey, provider.Name)
					}
					_ = prs.blacklistService.RecordSuccess("gemini", provider.Name)
					// 记录最后使用的供应商
					prs.setLastUsedProvider("gemini", provider.Name)
					fmt.Printf("[Gemini] ✓ 请求完成 | Provider: %s | 总耗时: %.2fs\n", provider.Name, time.Since(start).Seconds())
					return // 成功，退出
				}

				// 【关键修复】如果响应已写入客户端，不能降级到其他 provider，直接返回
				if responseWritten {
					fmt.Printf("[Gemini] ⚠️ 响应已部分写入，无法降级: %s | 错误: %s\n", provider.Name, errMsg)
					_ = prs.blacklistService.RecordFailure("gemini", provider.Name)
					return
				}

				// 失败，记录并继续
				lastError = errMsg
				_ = prs.blacklistService.RecordFailure("gemini", provider.Name)
			}

			fmt.Printf("[Gemini] Level %d 的所有 %d 个 provider 均失败，尝试下一 Level\n", level, len(providersInLevel))
		}

		// 所有 Level 都失败
		if requestLog.HttpCode == 0 {
			requestLog.HttpCode = http.StatusBadGateway
		}
		c.JSON(http.StatusBadGateway, gin.H{
			"error":   "all gemini providers failed",
			"details": lastError,
		})
		fmt.Printf("[Gemini] ✗ 所有 provider 均失败 | 最后错误: %s\n", lastError)
	}
}

// extractGeminiModelFromEndpoint 从 Gemini API endpoint 中提取模型名
// 例如 "/v1beta/models/gemini-2.5-pro:generateContent?alt=sse" -> "gemini-2.5-pro"
func extractGeminiModelFromEndpoint(endpoint string) string {
	if endpoint == "" {
		return ""
	}
	// 移除查询参数
	if qIdx := strings.Index(endpoint, "?"); qIdx >= 0 {
		endpoint = endpoint[:qIdx]
	}
	// 查找 models/ 后面的部分
	idx := strings.Index(endpoint, "models/")
	if idx == -1 {
		return ""
	}
	rest := endpoint[idx+len("models/"):]
	if rest == "" {
		return ""
	}
	// 移除动作部分（如 :generateContent, :streamGenerateContent）
	if colonIdx := strings.Index(rest, ":"); colonIdx >= 0 {
		rest = rest[:colonIdx]
	}
	return strings.TrimSpace(rest)
}

// forwardGeminiRequest 转发 Gemini 请求到指定 provider
// 返回 (成功, 错误信息, 是否已写入响应)
// 【重要】当 responseWritten=true 时，调用方不得重试或降级，因为响应头/数据已发送给客户端
func (prs *ProviderRelayService) forwardGeminiRequest(
	c *gin.Context,
	provider *GeminiProvider,
	endpoint string,
	bodyBytes []byte,
	isStream bool,
	requestLog *RequestLog,
) (success bool, errMsg string, responseWritten bool) {
	providerStart := time.Now()

	// 构建目标 URL
	targetURL := strings.TrimSuffix(provider.BaseURL, "/") + endpoint

	// 预先填充日志，保证失败也能记录 provider 和模型
	requestLog.Provider = provider.Name
	// 【修复】每次尝试开始前重置 HttpCode，避免重试时沿用上一次的状态码
	requestLog.HttpCode = 0
	// 优先从 endpoint 提取模型名（如 gemini-2.5-pro），否则回退到 provider.Model
	if extractedModel := extractGeminiModelFromEndpoint(endpoint); extractedModel != "" {
		requestLog.Model = extractedModel
	} else {
		requestLog.Model = provider.Model
	}

	// 创建 HTTP 请求
	req, err := http.NewRequest("POST", targetURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return false, fmt.Sprintf("创建请求失败: %v", err), false
	}

	// 复制请求头
	for key, values := range c.Request.Header {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}

	// 设置 API Key
	if provider.APIKey != "" {
		req.Header.Set("x-goog-api-key", provider.APIKey)
	}

	// 发送请求
	// 使用带网络级重试的 HTTP 客户端，处理瞬时网络错误
	// 【修复】使用 context 超时而非直接修改共享客户端的 Timeout 字段，避免影响其他请求
	client := newRetryHTTPClient("gemini", 1, 500*time.Millisecond)
	ctx, cancel := context.WithTimeout(c.Request.Context(), 300*time.Second)
	defer cancel()
	req = req.WithContext(ctx)
	resp, err := client.Do(req)
	providerDuration := time.Since(providerStart).Seconds()

	if err != nil {
		fmt.Printf("[Gemini]   ✗ 失败: %s | 错误: %v | 耗时: %.2fs\n", provider.Name, err, providerDuration)
		return false, fmt.Sprintf("请求失败: %v", err), false
	}
	defer resp.Body.Close()

	// 先记录上游状态码，失败场景也能落库
	requestLog.HttpCode = resp.StatusCode

	// 检查响应状态
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		errorBody, _ := io.ReadAll(resp.Body)
		fmt.Printf("[Gemini]   ✗ 失败: %s | HTTP %d | 耗时: %.2fs\n", provider.Name, resp.StatusCode, providerDuration)
		return false, fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(errorBody)), false
	}

	fmt.Printf("[Gemini]   ✓ 连接成功: %s | HTTP %d | 耗时: %.2fs\n", provider.Name, resp.StatusCode, providerDuration)

	// 处理响应
	if isStream {
		// 流式模式：先写 header 再流式传输
		for key, values := range resp.Header {
			for _, value := range values {
				c.Header(key, value)
			}
		}
		c.Status(resp.StatusCode)
		c.Writer.Flush()
		// 【重要】从 Flush() 开始，响应头已写入客户端，任何失败都不能重试
		copyErr := streamGeminiResponseWithHook(resp.Body, c.Writer, requestLog)
		if copyErr != nil {
			fmt.Printf("[Gemini]   ⚠️ 流式传输中断: %s | 错误: %v\n", provider.Name, copyErr)
			// 流式传输中断：已写入部分响应，客户端会收到不完整数据
			return false, fmt.Sprintf("流式传输中断: %v", copyErr), true
		}
	} else {
		// 非流式模式：先读完 body 再写 header（允许读取失败时重试）
		body, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			fmt.Printf("[Gemini]   ⚠️ 读取响应失败: %s | 错误: %v\n", provider.Name, readErr)
			// 【修复】此时 header 尚未写入客户端，可以重试/降级
			return false, fmt.Sprintf("读取响应失败: %v", readErr), false
		}
		// 解析 Gemini 用量数据
		parseGeminiUsageMetadata(body, requestLog)
		// 读取成功后再写 header 和 body
		for key, values := range resp.Header {
			for _, value := range values {
				c.Header(key, value)
			}
		}
		c.Data(resp.StatusCode, resp.Header.Get("Content-Type"), body)
	}

	return true, "", true
}

// parseGeminiUsageMetadata 从 Gemini 非流式响应中提取用量，填充 request_log
// 复用 mergeGeminiUsageMetadata 统一解析逻辑
func parseGeminiUsageMetadata(body []byte, reqLog *RequestLog) {
	if len(body) == 0 || reqLog == nil {
		return
	}
	usage := gjson.GetBytes(body, "usageMetadata")
	if !usage.Exists() {
		return
	}
	mergeGeminiUsageMetadata(usage, reqLog)
}

// injectThinkingIfNeeded 检查 URL 并注入 thinking 参数
func (prs *ProviderRelayService) injectThinkingIfNeeded(bodyBytes []byte, apiURL string) []byte {
	// 定义需要注入 thinking 的目标 URL
	targetURLs := []string{
		"https://open.bigmodel.cn/api/anthropic",
		"https://api.z.ai/api/anthropic",
	}

	// 1. 检查 URL 是否匹配
	matched := false
	for _, targetURL := range targetURLs {
		if apiURL == targetURL {
			matched = true
			break
		}
	}

	// 如果 URL 不匹配，直接返回
	if !matched {
		return bodyBytes
	}

	// 2. 检查 body 中是否已经存在 "thinking" 字段
	// 使用 gjson.GetBytes 快速检查，性能很高
	if gjson.GetBytes(bodyBytes, "thinking").Exists() {
		fmt.Printf("[INFO] 检测到目标 URL，但用户已包含 thinking 参数，跳过注入: %s\n", apiURL)
		return bodyBytes
	}

	// 3. 只有在不存在时，才注入默认参数
	fmt.Printf("[INFO] 检测到目标 URL 且无 thinking 参数，执行注入: %s\n", apiURL)

	modifiedBody, err := sjson.SetBytes(bodyBytes, "thinking", map[string]interface{}{
		"type":          "enabled",
		"budget_tokens": 4000,
	})
	if err != nil {
		fmt.Printf("[ERROR] 注入 thinking 参数失败: %v\n", err)
		return bodyBytes // 失败时返回原始数据，保证请求不挂
	}

	return modifiedBody
}

// customCliProxyHandler 处理自定义 CLI 工具的 API 请求
// 路由格式: /custom/:toolId/v1/messages
// toolId 用于区分不同的 CLI 工具，对应 provider kind 为 "custom:{toolId}"
func (prs *ProviderRelayService) customCliProxyHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 从 URL 参数提取 toolId
		toolId := c.Param("toolId")
		if toolId == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "toolId is required"})
			return
		}

		// 构建 provider kind（格式: "custom:{toolId}"）
		kind := "custom:" + toolId
		endpoint := "/v1/messages"

		fmt.Printf("[CustomCLI] 收到请求: toolId=%s, kind=%s\n", toolId, kind)

		// 读取请求体
		var bodyBytes []byte
		if c.Request.Body != nil {
			data, err := io.ReadAll(c.Request.Body)
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
				return
			}
			bodyBytes = data
			c.Request.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		}

		isStream := gjson.GetBytes(bodyBytes, "stream").Bool()
		requestedModel := gjson.GetBytes(bodyBytes, "model").String()

		if requestedModel == "" {
			fmt.Printf("[CustomCLI][WARN] 请求未指定模型名，无法执行模型智能降级\n")
		}

		// 【5分钟同源缓存】提取 user_id 用于缓存亲和性
		userID := prs.extractUserID(c)
		affinityKey := GenerateAffinityKey(userID, kind, requestedModel)

		// 加载该 CLI 工具的 providers
		providers, err := prs.providerService.LoadProviders(kind)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to load providers for %s: %v", kind, err)})
			return
		}

		// Filter available providers
		active := make([]Provider, 0, len(providers))
		reasons := skipReasons{} // track skip reasons
		for _, provider := range providers {
			if !provider.Enabled || provider.APIURL == "" || provider.APIKey == "" {
				reasons.disabled++
				continue
			}

			if errs := provider.ValidateConfiguration(); len(errs) > 0 {
				fmt.Printf("[CustomCLI][WARN] Provider %s config validation failed, skipped: %v\n", provider.Name, errs)
				reasons.configInvalid++
				continue
			}

			if requestedModel != "" && !provider.IsModelSupported(requestedModel) {
				fmt.Printf("[CustomCLI][INFO] Provider %s does not support model %s, skipped\n", provider.Name, requestedModel)
				reasons.modelUnsupported++
				continue
			}

			// Blacklist check
			if isBlacklisted, until := prs.blacklistService.IsBlacklisted(kind, provider.Name); isBlacklisted {
				fmt.Printf("[CustomCLI] ⛔ Provider %s blacklisted until %v\n", provider.Name, until.Format("15:04:05"))
				reasons.blacklisted++
				continue
			}

			active = append(active, provider)
		}

		if len(active) == 0 {
			errMsg := buildNoProviderError(requestedModel, kind, reasons)
			c.JSON(http.StatusNotFound, gin.H{"error": errMsg})
			return
		}

		// Build provider names list
		providerNames := make([]string, len(active))
		for i, p := range active {
			providerNames[i] = p.Name
		}
		fmt.Printf("[CustomCLI][INFO] Providers: total=%d, active=%d, skipped=%d (disabled=%d, config=%d, model=%d, blacklist=%d): %s\n",
			len(providers), len(active), reasons.total(),
			reasons.disabled, reasons.configInvalid, reasons.modelUnsupported, reasons.blacklisted,
			strings.Join(providerNames, ", "))

		// 按 Level 分组
		levelGroups := make(map[int][]Provider)
		for _, provider := range active {
			level := provider.Level
			if level <= 0 {
				level = 1
			}
			levelGroups[level] = append(levelGroups[level], provider)
		}

		levels := make([]int, 0, len(levelGroups))
		for level := range levelGroups {
			levels = append(levels, level)
		}
		sort.Ints(levels)

		fmt.Printf("[CustomCLI][INFO] 共 %d 个 Level 分组：%v\n", len(levels), levels)

		query := flattenQuery(c.Request.URL.Query())
		clientHeaders := cloneHeaders(c.Request.Header)
		// 注意：认证方式在每个 provider 转发时由 determineAuthMethod() 动态计算

		// 获取拉黑功能开关状态
		blacklistEnabled := prs.blacklistService.ShouldUseFixedMode()

		// 【拉黑模式】：同 Provider 重试直到被拉黑，然后切换到下一个 Provider
		if blacklistEnabled {
			fmt.Printf("[CustomCLI][INFO] 🔒 拉黑模式已开启（同 Provider 重试到拉黑再切换）\n")

			// 获取重试配置
			retryConfig := prs.blacklistService.GetRetryConfig()
			maxRetryPerProvider := retryConfig.FailureThreshold
			retryWaitSeconds := retryConfig.RetryWaitSeconds
			fmt.Printf("[CustomCLI][INFO] 重试配置: 每 Provider 最多 %d 次重试，间隔 %d 秒\n",
				maxRetryPerProvider, retryWaitSeconds)

			var lastError error
			var lastProvider string
			totalAttempts := 0

			// 遍历所有 Level 和 Provider
			for _, level := range levels {
				providersInLevel := levelGroups[level]
				fmt.Printf("[CustomCLI][INFO] === 尝试 Level %d（%d 个 provider）===\n", level, len(providersInLevel))

				for _, provider := range providersInLevel {
					// 检查是否已被拉黑（跳过已拉黑的 provider）
					if blacklisted, until := prs.blacklistService.IsBlacklisted(kind, provider.Name); blacklisted {
						fmt.Printf("[CustomCLI][INFO] ⏭️ 跳过已拉黑的 Provider: %s (解禁时间: %v)\n", provider.Name, until)
						continue
					}

					// 获取实际模型名
					effectiveModel := provider.GetEffectiveModel(requestedModel)
					currentBodyBytes := bodyBytes
					if effectiveModel != requestedModel && requestedModel != "" {
						fmt.Printf("[CustomCLI][INFO] Provider %s 映射模型: %s -> %s\n", provider.Name, requestedModel, effectiveModel)
						modifiedBody, err := ReplaceModelInRequestBody(bodyBytes, effectiveModel)
						if err != nil {
							fmt.Printf("[CustomCLI][ERROR] 模型映射失败: %v，跳过此 Provider\n", err)
							continue
						}
						currentBodyBytes = modifiedBody
					}

					// 获取有效端点
					effectiveEndpoint := provider.GetEffectiveEndpoint(endpoint)

					// 同 Provider 内重试循环
					for retryCount := 0; retryCount < maxRetryPerProvider; retryCount++ {
						totalAttempts++

						// 再次检查是否已被拉黑（重试过程中可能被拉黑）
						if blacklisted, _ := prs.blacklistService.IsBlacklisted(kind, provider.Name); blacklisted {
							fmt.Printf("[CustomCLI][INFO] 🚫 Provider %s 已被拉黑，切换到下一个\n", provider.Name)
							break
						}

						fmt.Printf("[CustomCLI][INFO] [拉黑模式] Provider: %s (Level %d) | 尝试 %d/%d | Model: %s\n",
							provider.Name, level, retryCount+1, maxRetryPerProvider, effectiveModel)

						startTime := time.Now()
						// 根据 Provider 配置决定认证方式（auto 时自动检测原始请求）
						authMethod := determineAuthMethod(&provider, c.Request.Header)
						ok, err := prs.forwardRequest(c, kind, provider, effectiveEndpoint, query, clientHeaders, currentBodyBytes, isStream, effectiveModel, authMethod)
						duration := time.Since(startTime)

						if ok {
							fmt.Printf("[CustomCLI][INFO] ✓ 成功: %s | 尝试 %d 次 | 耗时: %.2fs\n",
								provider.Name, retryCount+1, duration.Seconds())
							if err := prs.blacklistService.RecordSuccess(kind, provider.Name); err != nil {
								fmt.Printf("[CustomCLI][WARN] 清零失败计数失败: %v\n", err)
							}
							prs.setLastUsedProvider(kind, provider.Name)
							return
						}

						// 失败处理
						lastError = err
						lastProvider = provider.Name

						errorMsg := "未知错误"
						if err != nil {
							errorMsg = err.Error()
						}
						fmt.Printf("[CustomCLI][WARN] ✗ 失败: %s | 尝试 %d/%d | 错误: %s | 耗时: %.2fs\n",
							provider.Name, retryCount+1, maxRetryPerProvider, errorMsg, duration.Seconds())

						// 客户端中断不计入失败次数，直接返回
						if errors.Is(err, errClientAbort) {
							fmt.Printf("[CustomCLI][INFO] 客户端中断，停止重试\n")
							return
						}

						// 记录失败次数（可能触发拉黑）
						if err := prs.blacklistService.RecordFailure(kind, provider.Name); err != nil {
							fmt.Printf("[CustomCLI][ERROR] 记录失败到黑名单失败: %v\n", err)
						}

						// 检查是否刚被拉黑
						if blacklisted, _ := prs.blacklistService.IsBlacklisted(kind, provider.Name); blacklisted {
							fmt.Printf("[CustomCLI][INFO] 🚫 Provider %s 达到失败阈值，已被拉黑，切换到下一个\n", provider.Name)
							break
						}

						// 等待后重试（除非是最后一次）
						if retryCount < maxRetryPerProvider-1 {
							fmt.Printf("[CustomCLI][INFO] ⏳ 等待 %d 秒后重试...\n", retryWaitSeconds)
							time.Sleep(time.Duration(retryWaitSeconds) * time.Second)
						}
					}
				}
			}

			// 所有 Provider 都失败或被拉黑
			fmt.Printf("[CustomCLI][ERROR] 💥 拉黑模式：所有 Provider 都失败或被拉黑（共尝试 %d 次）\n", totalAttempts)

			errorMsg := "未知错误"
			if lastError != nil {
				errorMsg = lastError.Error()
			}
			c.JSON(http.StatusBadGateway, gin.H{
				"error":         fmt.Sprintf("所有 Provider 都失败或被拉黑，最后尝试: %s - %s", lastProvider, errorMsg),
				"lastProvider":  lastProvider,
				"totalAttempts": totalAttempts,
				"mode":          "blacklist_retry",
				"hint":          "拉黑模式已开启，同 Provider 重试到拉黑再切换。如需立即降级请关闭拉黑功能",
			})
			return
		}

		// 【降级模式】：失败自动尝试下一个 provider
		roundRobinEnabled := prs.isRoundRobinEnabled()
		if roundRobinEnabled {
			fmt.Printf("[CustomCLI][INFO] 🔄 降级模式 + 轮询负载均衡\n")
		} else {
			fmt.Printf("[CustomCLI][INFO] 🔄 降级模式（顺序降级）\n")
		}

		var lastError error
		var lastProvider string
		var lastDuration time.Duration
		totalAttempts := 0

		// 【5分钟同源缓存】检查是否有缓存的 provider
		cachedProviderName := ""
		if prs.affinityManager != nil {
			cachedProviderName = prs.affinityManager.Get(affinityKey)
		}

		// 【5分钟同源缓存】如果有缓存的 provider，优先尝试
		if cachedProviderName != "" {
			affinityResult := prs.tryAffinityProvider(
				c, kind, affinityKey, cachedProviderName, active,
				endpoint, query, clientHeaders, bodyBytes, isStream, requestedModel,
			)
			if affinityResult.Handled {
				return // 成功或客户端中断，不再继续
			}
			if affinityResult.UsedProvider != "" {
				totalAttempts++
				lastError = affinityResult.LastError
				lastProvider = affinityResult.UsedProvider
				lastDuration = affinityResult.Duration
			}
		}

		for _, level := range levels {
			providersInLevel := levelGroups[level]

			// 如果启用轮询，对同 Level 的 providers 进行轮询排序
			if roundRobinEnabled {
				providersInLevel = prs.roundRobinOrder(kind, level, providersInLevel)
			}

			fmt.Printf("[CustomCLI][INFO] === 尝试 Level %d（%d 个 provider）===\n", level, len(providersInLevel))

			for i, provider := range providersInLevel {
				// 【5分钟同源缓存】跳过已经尝试过的缓存 provider
				if provider.Name == cachedProviderName {
					fmt.Printf("[CustomCLI][INFO]   跳过已尝试的缓存 provider: %s\n", provider.Name)
					continue
				}

				totalAttempts++

				effectiveModel := provider.GetEffectiveModel(requestedModel)
				currentBodyBytes := bodyBytes
				if effectiveModel != requestedModel && requestedModel != "" {
					fmt.Printf("[CustomCLI][INFO] Provider %s 映射模型: %s -> %s\n", provider.Name, requestedModel, effectiveModel)
					modifiedBody, err := ReplaceModelInRequestBody(bodyBytes, effectiveModel)
					if err != nil {
						fmt.Printf("[CustomCLI][ERROR] 替换模型名失败: %v\n", err)
						continue
					}
					currentBodyBytes = modifiedBody
				}

				fmt.Printf("[CustomCLI][INFO]   [%d/%d] Provider: %s | Model: %s\n", i+1, len(providersInLevel), provider.Name, effectiveModel)
				// 获取有效的端点（用户配置优先）
				effectiveEndpoint := provider.GetEffectiveEndpoint(endpoint)

				startTime := time.Now()
				// 根据 Provider 配置决定认证方式（auto 时自动检测原始请求）
				authMethod := determineAuthMethod(&provider, c.Request.Header)
				ok, err := prs.forwardRequest(c, kind, provider, effectiveEndpoint, query, clientHeaders, currentBodyBytes, isStream, effectiveModel, authMethod)
				duration := time.Since(startTime)

				if ok {
					fmt.Printf("[CustomCLI][INFO]   ✓ Level %d 成功: %s | 耗时: %.2fs\n", level, provider.Name, duration.Seconds())

					// 【5分钟同源缓存】设置缓存亲和性
					if prs.affinityManager != nil {
						prs.affinityManager.Set(affinityKey, provider.Name)
					}

					if err := prs.blacklistService.RecordSuccess(kind, provider.Name); err != nil {
						fmt.Printf("[CustomCLI][WARN] 清零失败计数失败: %v\n", err)
					}
					prs.setLastUsedProvider(kind, provider.Name)
					return
				}

				lastError = err
				lastProvider = provider.Name
				lastDuration = duration

				errorMsg := "未知错误"
				if err != nil {
					errorMsg = err.Error()
				}
				fmt.Printf("[CustomCLI][WARN]   ✗ Level %d 失败: %s | 错误: %s | 耗时: %.2fs\n",
					level, provider.Name, errorMsg, duration.Seconds())

				if errors.Is(err, errClientAbort) {
					fmt.Printf("[CustomCLI][INFO] 客户端中断，跳过失败计数: %s\n", provider.Name)
				} else if err := prs.blacklistService.RecordFailure(kind, provider.Name); err != nil {
					fmt.Printf("[CustomCLI][ERROR] 记录失败到黑名单失败: %v\n", err)
				}

				// 发送切换通知
				if prs.notificationService != nil {
					nextProvider := ""
					if i+1 < len(providersInLevel) {
						nextProvider = providersInLevel[i+1].Name
					} else {
						for _, nextLevel := range levels {
							if nextLevel > level && len(levelGroups[nextLevel]) > 0 {
								nextProvider = levelGroups[nextLevel][0].Name
								break
							}
						}
					}
					if nextProvider != "" {
						prs.notificationService.NotifyProviderSwitch(SwitchNotification{
							FromProvider: provider.Name,
							ToProvider:   nextProvider,
							Reason:       errorMsg,
							Platform:     kind,
						})
					}
				}
			}

			fmt.Printf("[CustomCLI][WARN] Level %d 的所有 %d 个 provider 均失败，尝试下一 Level\n", level, len(providersInLevel))
		}

		// 所有 provider 都失败
		errorMsg := "未知错误"
		if lastError != nil {
			errorMsg = lastError.Error()
		}
		fmt.Printf("[CustomCLI][ERROR] 所有 %d 个 provider 均失败，最后尝试: %s | 错误: %s\n",
			totalAttempts, lastProvider, errorMsg)

		c.JSON(http.StatusBadGateway, gin.H{
			"error":          fmt.Sprintf("所有 %d 个 provider 均失败，最后错误: %s", totalAttempts, errorMsg),
			"last_provider":  lastProvider,
			"last_duration":  fmt.Sprintf("%.2fs", lastDuration.Seconds()),
			"total_attempts": totalAttempts,
		})
	}
}

// forwardModelsRequest 共享的 /v1/models 请求转发逻辑
// 返回 (selectedProvider, error)
func (prs *ProviderRelayService) forwardModelsRequest(
	c *gin.Context,
	kind string,
	logPrefix string,
) error {
	fmt.Printf("[%s] 收到 /v1/models 请求, kind=%s\n", logPrefix, kind)

	// 加载 providers
	providers, err := prs.providerService.LoadProviders(kind)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load providers"})
		return fmt.Errorf("failed to load providers: %w", err)
	}

	// 过滤可用的 providers（启用 + URL + APIKey）
	var activeProviders []Provider
	for _, provider := range providers {
		if !provider.Enabled || provider.APIURL == "" || provider.APIKey == "" {
			continue
		}

		// 黑名单检查：跳过已拉黑的 provider
		if isBlacklisted, until := prs.blacklistService.IsBlacklisted(kind, provider.Name); isBlacklisted {
			fmt.Printf("[%s] ⛔ Provider %s 已拉黑，过期时间: %v\n", logPrefix, provider.Name, until.Format("15:04:05"))
			continue
		}

		activeProviders = append(activeProviders, provider)
	}

	if len(activeProviders) == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "no providers available"})
		return fmt.Errorf("no providers available")
	}

	// 按 Level 分组并排序
	levelGroups := make(map[int][]Provider)
	for _, provider := range activeProviders {
		level := provider.Level
		if level <= 0 {
			level = 1
		}
		levelGroups[level] = append(levelGroups[level], provider)
	}

	levels := make([]int, 0, len(levelGroups))
	for level := range levelGroups {
		levels = append(levels, level)
	}
	sort.Ints(levels)

	// 尝试第一个可用的 provider（按 Level 升序）
	var selectedProvider *Provider
	for _, level := range levels {
		if len(levelGroups[level]) > 0 {
			p := levelGroups[level][0]
			selectedProvider = &p
			break
		}
	}

	if selectedProvider == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "no providers available"})
		return fmt.Errorf("no providers available after filtering")
	}

	fmt.Printf("[%s] 使用 Provider: %s | URL: %s\n", logPrefix, selectedProvider.Name, selectedProvider.APIURL)

	// 构建目标 URL（拼接 provider 的 APIURL 和 /v1/models）
	targetURL := joinURL(selectedProvider.APIURL, "/v1/models")

	// 创建 HTTP 请求
	req, err := http.NewRequest("GET", targetURL, nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("创建请求失败: %v", err)})
		return fmt.Errorf("failed to create request: %w", err)
	}

	// 复制客户端请求头（使用标准库处理，过滤认证头）
	clientHeaders := cloneHeaders(c.Request.Header)
	for key, values := range clientHeaders {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}

	// 检测原始请求的认证方式，转发时使用相同方式
	authMethod := detectAuthMethod(c.Request.Header)
	switch authMethod {
	case AuthMethodXAPIKey:
		// 原始请求使用 x-api-key，转发也使用 x-api-key
		req.Header.Set("X-Api-Key", selectedProvider.APIKey)
		// 删除可能存在的 Authorization 头
		req.Header.Del("Authorization")
	default:
		// 原始请求使用 Authorization Bearer，转发也使用 Authorization
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", selectedProvider.APIKey))
		// 删除可能存在的 x-api-key 头
		req.Header.Del("X-Api-Key")
	}

	// 设置默认 Accept 头
	if req.Header.Get("Accept") == "" {
		req.Header.Set("Accept", "application/json")
	}

	// 发送请求（按渠道应用代理配置）
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	req = req.WithContext(ctx)

	// 网络级重试：1 次，间隔 500ms，仅针对瞬时网络错误
	client := newRetryHTTPClient(kind, 1, 500*time.Millisecond)
	resp, err := client.Do(req)
	if err != nil {
		fmt.Printf("[%s] ✗ 请求失败: %s | 错误: %v\n", logPrefix, selectedProvider.Name, err)
		c.JSON(http.StatusBadGateway, gin.H{"error": fmt.Sprintf("请求失败: %v", err)})
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	// 读取响应
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Printf("[%s] ✗ 读取响应失败: %s | 错误: %v\n", logPrefix, selectedProvider.Name, err)
		c.JSON(http.StatusBadGateway, gin.H{"error": fmt.Sprintf("读取响应失败: %v", err)})
		return fmt.Errorf("failed to read response: %w", err)
	}

	// 复制响应头
	for key, values := range resp.Header {
		for _, value := range values {
			c.Header(key, value)
		}
	}

	fmt.Printf("[%s] ✓ 成功: %s | HTTP %d\n", logPrefix, selectedProvider.Name, resp.StatusCode)

	// 返回响应
	c.Data(resp.StatusCode, resp.Header.Get("Content-Type"), body)
	return nil
}

// modelsHandler 处理 /v1/models 请求（OpenAI-compatible API）
// 将请求转发到第一个可用的 provider 并注入 API Key
func (prs *ProviderRelayService) modelsHandler(kind string) gin.HandlerFunc {
	return func(c *gin.Context) {
		_ = prs.forwardModelsRequest(c, kind, "Models")
	}
}

// customModelsHandler 处理自定义 CLI 工具的 /v1/models 请求
// 路由格式: /custom/:toolId/v1/models
func (prs *ProviderRelayService) customModelsHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 从 URL 参数提取 toolId
		toolId := c.Param("toolId")
		if toolId == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "toolId is required"})
			return
		}

		// 构建 provider kind（格式: "custom:{toolId}"）
		kind := "custom:" + toolId

		_ = prs.forwardModelsRequest(c, kind, "CustomModels")
	}
}

// decompressGzip 解压 gzip 压缩的数据
// 用于请求详情缓存中解压 gzip 响应体，以便正确显示内容
func decompressGzip(data []byte) ([]byte, error) {
	reader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	return io.ReadAll(reader)
}
