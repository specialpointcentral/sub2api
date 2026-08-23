package service

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// codexFingerprintIDsContextKey 是暂存在 gin context 的收敛 ID 集合键。
// 由 Forward（非透传）或 forwardOpenAIPassthrough（透传）解析后写入，请求
// 构造器读取用于出站头改写——请求体与出站头必须共享同一份 IDs，保证
// turn_id 等随机字段一致。
const codexFingerprintIDsContextKey = "codex_fingerprint_ids"

// stageCodexFingerprintIDs 将本 attempt 解析出的收敛 ID 暂存到 gin context。
// 必须无条件覆写（含 nil）：failover 从收敛账号切到 off 账号时，上一账号的
// IDs 不得残留并被误应用到新账号的出站头（typed-nil 由应用侧 nil 守卫吸收）。
func stageCodexFingerprintIDs(c *gin.Context, ids *codexFingerprintIDs) {
	if c != nil {
		c.Set(codexFingerprintIDsContextKey, ids)
	}
}

func stagedCodexFingerprintIDs(c *gin.Context, account *Account) *codexFingerprintIDs {
	if c == nil || account == nil || (!account.UsesOpenAICodexProtocol() && !isOpenAICodexFingerprintAccount(account)) {
		return nil
	}
	value, ok := c.Get(codexFingerprintIDsContextKey)
	if !ok {
		return nil
	}
	ids, ok := value.(*codexFingerprintIDs)
	if !ok || ids == nil || ids.accountID != account.ID {
		return nil
	}
	return ids
}

func stagedCodexFingerprintUserAgent(c *gin.Context, account *Account) string {
	ids := stagedCodexFingerprintIDs(c, account)
	if ids == nil {
		return ""
	}
	return strings.TrimSpace(ids.userAgent)
}

// applyStagedCodexFingerprintHeaders 读取 context 暂存的收敛 ID 并改写出站头。
// 非透传与透传两个请求构造器共用本函数，防止应用语义漂移。仅解析该
// snapshot 的 OpenAI OAuth/API Key 账号可读取，避免 stale context 跨账号
// failover 泄漏。
func applyStagedCodexFingerprintHeaders(c *gin.Context, account *Account, h http.Header) {
	applyCodexFingerprintHeaders(h, stagedCodexFingerprintIDs(c, account))
}

func applyStagedCodexFingerprintClientMetadata(c *gin.Context, account *Account, reqBody map[string]any) bool {
	return applyCodexFingerprintClientMetadata(reqBody, stagedCodexFingerprintIDs(c, account))
}

// stageCodexFingerprintForMap resolves one account-scoped identity snapshot and
// applies it to a decoded request body. All map-based forwarding paths use this
// seam so API-key eligibility and off semantics cannot drift by endpoint.
func (s *OpenAIGatewayService) stageCodexFingerprintForMap(c *gin.Context, account *Account, reqBody map[string]any, promptCacheFallback any, enforceIdentity bool) (bool, error) {
	stageCodexFingerprintIDs(c, nil)
	if !isOpenAICodexFingerprintAccount(account) {
		return false, nil
	}
	var clientHeaders http.Header
	if c != nil && c.Request != nil {
		clientHeaders = c.Request.Header
	}
	headerSessionID := extractClientSessionID(clientHeaders)
	bodySessionID := codexFingerprintSessionHint(reqBody["client_metadata"], nil)
	bodyPromptCacheKey := codexFingerprintSessionHint(nil, reqBody["prompt_cache_key"])
	promptCacheKey := reqBody["prompt_cache_key"]
	if promptCacheKey == nil {
		promptCacheKey = promptCacheFallback
	}
	clientHeaders = withCodexFingerprintSessionHint(clientHeaders, codexFingerprintSessionHint(reqBody["client_metadata"], promptCacheKey))
	ids, err := s.resolveCodexFingerprintIDsForOutbound(c, account, clientHeaders, enforceIdentity)
	if err != nil {
		return false, err
	}
	changed := false
	if ids != nil {
		captureCodexFingerprintPromptCacheSessionHint(ids, headerSessionID, bodySessionID, bodyPromptCacheKey)
		captureCodexFingerprintPromptCacheKeyFallback(ids, promptCacheFallback)
		changed = applyCodexFingerprintClientMetadata(reqBody, ids)
	}
	stageCodexFingerprintIDs(c, ids)
	return changed, nil
}

// stageCodexFingerprintForRaw is the raw-JSON counterpart used by passthrough
// and native WebSocket ingress. It preserves the hot path's surgical JSON
// rewrite while sharing the same account gate and staged header snapshot.
func (s *OpenAIGatewayService) stageCodexFingerprintForRaw(c *gin.Context, account *Account, body []byte, enforceIdentity bool) ([]byte, bool, error) {
	stageCodexFingerprintIDs(c, nil)
	if !isOpenAICodexFingerprintAccount(account) {
		return body, false, nil
	}
	var clientHeaders http.Header
	if c != nil && c.Request != nil {
		clientHeaders = c.Request.Header
	}
	headerSessionID := extractClientSessionID(clientHeaders)
	bodySessionID := strings.TrimSpace(gjson.GetBytes(body, "client_metadata.session_id").String())
	bodyPromptCacheKey := strings.TrimSpace(gjson.GetBytes(body, "prompt_cache_key").String())
	clientHeaders = withCodexFingerprintSessionHint(clientHeaders, codexFingerprintSessionHintRaw(body))
	ids, err := s.resolveCodexFingerprintIDsForOutbound(c, account, clientHeaders, enforceIdentity)
	if err != nil {
		return body, false, err
	}
	captureCodexFingerprintPromptCacheSessionHint(ids, headerSessionID, bodySessionID, bodyPromptCacheKey)
	rewritten, changed, err := applyCodexFingerprintClientMetadataRaw(body, ids)
	if err != nil {
		return body, false, err
	}
	stageCodexFingerprintIDs(c, ids)
	return rewritten, changed, nil
}

// codexFingerprintMode 控制 OpenAI OAuth/API Key 账号出站请求的设备指纹
// 收敛强度。多人共享同一账号时，每个用户的 Codex 客户端会携带各自不同的
// installation_id / session_id / thread_id，上游据此判定设备数和会话数。
// OAuth off 保留既有账号 session 命名空间；API Key off 不启用新增的指纹
// 收敛，并保留各入口原有的 session 兼容行为。其余模式在平台池 slot 内逐级收敛。
type codexFingerprintMode string

const (
	// codexFingerprintOff 不做设备/线程/窗口收敛，仅统一 session 命名空间。
	// 这是默认值：收敛是显式 opt-in 的（见 GetCodexFingerprintMode）。
	codexFingerprintOff codexFingerprintMode = "off"
	// codexFingerprintDevice 按 slot 收敛 installation_id，并按 slot + 客户端
	// session 派生 session_id；thread 仍由客户端透传。
	codexFingerprintDevice codexFingerprintMode = "device"
	// codexFingerprintSession 每个 slot 只有一个长 session，客户端真实 session
	// 映射为其下 thread；持久 slot 记录首个 root session，使根 thread == session。
	codexFingerprintSession codexFingerprintMode = "session"
	// codexFingerprintFull 每个 slot 只有一个 installation/session/thread，且 thread == session。
	codexFingerprintFull codexFingerprintMode = "full"
)

const (
	codexFingerprintModeExtraKey = "codex_fingerprint_mode"
	codexFingerprintSeedExtraKey = "codex_fingerprint_seed"
)

func canonicalCodexFingerprintSeed(value any) (string, bool) {
	raw, ok := value.(string)
	if !ok {
		return "", false
	}
	trimmed := strings.TrimSpace(raw)
	parsed, err := uuid.Parse(trimmed)
	if err != nil || parsed == uuid.Nil || trimmed != parsed.String() {
		return "", false
	}
	return trimmed, true
}

func newCodexFingerprintSeed() string {
	return uuid.NewString()
}

func stripCodexFingerprintSeed(extra map[string]any) map[string]any {
	if extra == nil {
		return nil
	}
	stripped := maps.Clone(extra)
	delete(stripped, codexFingerprintSeedExtraKey)
	return stripped
}

func codexFingerprintModeFromExtra(extra map[string]any) codexFingerprintMode {
	if extra == nil {
		return codexFingerprintOff
	}
	raw, _ := extra[codexFingerprintModeExtraKey].(string)
	switch codexFingerprintMode(strings.TrimSpace(raw)) {
	case codexFingerprintOff, codexFingerprintDevice, codexFingerprintSession, codexFingerprintFull:
		return codexFingerprintMode(strings.TrimSpace(raw))
	default:
		return codexFingerprintOff
	}
}

func codexFingerprintModeRequiresSeed(mode codexFingerprintMode) bool {
	switch mode {
	case codexFingerprintDevice, codexFingerprintSession, codexFingerprintFull:
		return true
	default:
		return false
	}
}

func isOpenAICodexFingerprintAccount(account *Account) bool {
	return account != nil && (account.IsOpenAIOAuthLike() || account.IsOpenAIApiKey())
}

func shouldResolveCodexFingerprint(account *Account) bool {
	// SetupToken 账号不进入收敛：seed 兜底需要写库，写库不可用时不能让请求硬失败；
	// 其会话级隔离由 isolateOpenAISessionID / upstream 侧保留路径承担。
	return account != nil && (account.IsOpenAIOAuth() || (account.IsOpenAIApiKey() && account.GetCodexFingerprintMode() != codexFingerprintOff))
}

func codexFingerprintSeed(extra map[string]any) (string, bool) {
	if extra == nil {
		return "", false
	}
	return canonicalCodexFingerprintSeed(extra[codexFingerprintSeedExtraKey])
}

func prepareCodexFingerprintExtraForCreate(platform, accountType string, extra map[string]any) map[string]any {
	prepared := stripCodexFingerprintSeed(extra)
	if platform != PlatformOpenAI || (accountType != AccountTypeOAuth && accountType != AccountTypeSetupToken && accountType != AccountTypeAPIKey) || !codexFingerprintModeRequiresSeed(codexFingerprintModeFromExtra(prepared)) {
		return prepared
	}
	if prepared == nil {
		prepared = make(map[string]any, 1)
	}
	prepared[codexFingerprintSeedExtraKey] = newCodexFingerprintSeed()
	return prepared
}

func prepareCodexFingerprintExtraForUpdate(account *Account, extra map[string]any) map[string]any {
	prepared := stripCodexFingerprintSeed(extra)
	if !isOpenAICodexFingerprintAccount(account) {
		return prepared
	}
	if seed, ok := codexFingerprintSeed(account.Extra); ok {
		if prepared == nil {
			prepared = make(map[string]any, 1)
		}
		prepared[codexFingerprintSeedExtraKey] = seed
		return prepared
	}
	if codexFingerprintModeRequiresSeed(codexFingerprintModeFromExtra(prepared)) {
		if prepared == nil {
			prepared = make(map[string]any, 1)
		}
		prepared[codexFingerprintSeedExtraKey] = newCodexFingerprintSeed()
	}
	return prepared
}

func sanitizedCodexFingerprintExtraUpdates(updates map[string]any) map[string]any {
	if updates == nil {
		return nil
	}
	sanitized := maps.Clone(updates)
	delete(sanitized, codexFingerprintSeedExtraKey)
	return sanitized
}

// ShouldEnsureCodexFingerprintSeedForExtraUpdates reports whether a JSONB key-level
// extra update is enabling Codex fingerprint convergence and therefore must atomically
// preserve or create the system-managed per-account seed in the repository update.
func ShouldEnsureCodexFingerprintSeedForExtraUpdates(updates map[string]any) bool {
	if updates == nil {
		return false
	}
	return codexFingerprintModeRequiresSeed(codexFingerprintModeFromExtra(updates))
}

// GetCodexFingerprintMode 从账号 extra JSON 读取指纹收敛模式。
//
// **收敛是显式 opt-in**：未设置、空值或非法值一律按 off 处理，只有管理员
// 明确配置 device / session / full 才收敛。
//
// 历史：v0.1.175（#5553）把缺省值当作 session，导致升级后存量 OAuth 账号
// （普遍没有这个 extra 键）的每个非透传请求都被静默改写 installation /
// session / thread / turn / window 五类标识；#5555、#5556、#5582 报告的额度
// 缩水都卡在该版本边界，并有"回退 v0.1.173 即恢复"与"新账号开收敛后降额"
// 的 A/B 实测。上游的配额判定策略不可观测，因此这里取兼容安全的一侧：
// 不显式 opt-in 就保持 v0.1.175 之前的客户端身份（#5610）。
func (a *Account) GetCodexFingerprintMode() codexFingerprintMode {
	if !isOpenAICodexFingerprintAccount(a) {
		return codexFingerprintOff
	}
	return codexFingerprintModeFromExtra(a.Extra)
}

// deriveStableUUIDv4 从种子确定性派生一个 UUIDv4 格式的字符串。
// 同一种子永远返回同一值。
func deriveStableUUIDv4(seed string) string {
	h := sha256.Sum256([]byte(seed))
	b := h[:16]
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 1
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		binary.BigEndian.Uint32(b[0:4]),
		binary.BigEndian.Uint16(b[4:6]),
		binary.BigEndian.Uint16(b[6:8]),
		binary.BigEndian.Uint16(b[8:10]),
		b[10:16])
}

// deriveStableUUIDv7 从种子确定性派生一个 UUIDv7 形态的字符串。
// 前 48 位使用种子摘要作为稳定的类时间戳位；其余位保留摘要熵并设置
// RFC 9562 要求的 version 7 / RFC 4122 variant 位。同一种子跨重启不变。
// 从旧 UUIDv4 派生切换到 UUIDv7 会造成一次性、有意的 session/thread ID
// 旋转；此后固定种子仍保持固定输出。
func deriveStableUUIDv7(seed string) string {
	h := sha256.Sum256([]byte(seed))
	b := h[:16]
	b[6] = (b[6] & 0x0f) | 0x70 // version 7
	b[8] = (b[8] & 0x3f) | 0x80 // variant 1
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		binary.BigEndian.Uint32(b[0:4]),
		binary.BigEndian.Uint16(b[4:6]),
		binary.BigEndian.Uint16(b[6:8]),
		binary.BigEndian.Uint16(b[8:10]),
		b[10:16])
}

// resolveConvergedInstallationID 返回账号级恒定的 installation_id。
// 优先使用管理员配置的真实 device_id，无则从系统管理的账号随机种子确定性派生。
func resolveConvergedInstallationID(account *Account, seed string) string {
	if account == nil {
		return ""
	}
	if deviceID := account.GetOpenAIDeviceID(); deviceID != "" {
		return deviceID
	}
	if seed == "" {
		return ""
	}
	return deriveStableUUIDv4("sub2api:codex-install-id:v2:" + seed)
}

// resolveNamespacedCodexSessionID 把客户端原始 session 放入账号 seed 命名空间，
// 并统一输出为 UUIDv7 形态。相同账号/原始 session 跨重启稳定，不同账号即使
// 收到同一原始值也不会在上游碰撞。
func resolveNamespacedCodexSessionID(seed, clientSessionID string) string {
	seed = strings.TrimSpace(seed)
	clientSessionID = strings.TrimSpace(clientSessionID)
	if seed == "" || clientSessionID == "" {
		return ""
	}
	return deriveStableUUIDv7(seed + ":" + clientSessionID)
}

// resolveStableCodexDeviceSessionID 为一个设备 slot 派生稳定的长 session。
// session/full 模式共享该值；full 的 thread 直接等于它，session 的根 thread 也等于它。
func resolveStableCodexDeviceSessionID(seed string) string {
	if seed == "" {
		return ""
	}
	return deriveStableUUIDv7("sub2api:codex-session-id:v2:" + seed)
}

// resolveConvergedThreadID 按客户端原始 session-id 确定性派生 thread_id。
// 每个真实 Codex 会话（不同客户端启动实例）获得一个独立线程，
// 模拟正常用户 spawn 子代理或开多窗口的模式。
func resolveConvergedThreadID(seed, clientSessionID string) string {
	if seed == "" || clientSessionID == "" {
		return ""
	}
	return deriveStableUUIDv7("sub2api:codex-thread-id:v2:" + seed + ":" + clientSessionID)
}

// codexFingerprintIDs 收敛后的完整 ID 集合。
// 由 resolveCodexFingerprintIDs 一次性生成，同一个实例在头改写和体改写之间共享，
// 确保所有载体中的 turn_id 等随机字段一致。体改写时还会补记原始
// client_metadata.session_id，用于识别 root prompt_cache_key 的默认值。
type codexFingerprintIDs struct {
	accountID                     int64
	accountSeed                   string
	mode                          codexFingerprintMode
	deviceSlot                    int
	installationID                string
	sessionID                     string
	threadID                      string
	turnID                        string
	windowID                      string
	turnStartedAtUnixMs           int64
	sandbox                       string
	userAgent                     string
	originalBodySessionID         string
	originalBodySessionIDCaptured bool
	rootClientSessionID           string
	clientSessionID               string
}

// resolveCodexFingerprintIDs 按收敛模式计算出站 ID 集合。
// clientSessionID 是客户端原始的 session-id 头值（连字符形式），用于 session 模式下
// 的 thread_id 派生——每个真实 Codex 会话得到一个独立线程。
// 返回 nil 表示账号缺失或没有合法的系统 seed，无法安全改写。
// 注意：包含随机生成的 turn_id，调用方必须只调用一次并共享结果给头改写和体改写。
func resolveCodexFingerprintIDs(account *Account, clientSessionID string, mode codexFingerprintMode) *codexFingerprintIDs {
	return resolveCodexFingerprintIDsForDeviceSlot(account, clientSessionID, mode, 0)
}

func codexDeviceSeed(seed string, deviceSlot int) string {
	// Slot 0 is the implicit legacy device while pooling is disabled. Persisted
	// pool slots start at 1 and deliberately use a disjoint seed namespace, so
	// enabling the incompatible pool contract rotates every user identity once.
	if deviceSlot <= 0 {
		return seed
	}
	return seed + ":device-pool:" + strconv.Itoa(deviceSlot)
}

func resolveConvergedInstallationIDForSlot(account *Account, seed string, deviceSlot int) string {
	if deviceSlot <= 0 {
		return resolveConvergedInstallationID(account, seed)
	}
	return deriveStableUUIDv4("sub2api:codex-install-id:v2:" + codexDeviceSeed(seed, deviceSlot))
}

func resolveCodexFingerprintIDsForDeviceSlot(account *Account, clientSessionID string, mode codexFingerprintMode, deviceSlot int) *codexFingerprintIDs {
	return resolveCodexFingerprintIDsForDeviceSlotWithRoot(account, clientSessionID, mode, deviceSlot, "")
}

func resolveCodexFingerprintIDsForDeviceSlotWithRoot(account *Account, clientSessionID string, mode codexFingerprintMode, deviceSlot int, rootClientSessionID string) *codexFingerprintIDs {
	if account == nil {
		return nil
	}
	seed, ok := codexFingerprintSeed(account.Extra)
	if !ok {
		return nil
	}

	deviceSeed := codexDeviceSeed(seed, deviceSlot)
	ids := &codexFingerprintIDs{
		accountID:           account.ID,
		accountSeed:         seed,
		mode:                mode,
		deviceSlot:          deviceSlot,
		rootClientSessionID: strings.TrimSpace(rootClientSessionID),
		clientSessionID:     strings.TrimSpace(clientSessionID),
		turnStartedAtUnixMs: time.Now().UnixMilli(),
	}
	if mode == codexFingerprintOff {
		ids.sessionID = resolveNamespacedCodexSessionID(seed, clientSessionID)
		return ids
	}

	ids.installationID = resolveConvergedInstallationIDForSlot(account, seed, deviceSlot)
	if ids.installationID == "" {
		return nil
	}

	switch mode {
	case codexFingerprintDevice:
		ids.sessionID = resolveNamespacedCodexSessionID(deviceSeed, clientSessionID)
		return ids

	case codexFingerprintSession:
		ids.sessionID = resolveStableCodexDeviceSessionID(deviceSeed)
		ids.threadID = resolveCodexDeviceSessionThreadID(deviceSeed, ids.sessionID, clientSessionID, ids.rootClientSessionID)
		ids.turnID = uuid.Must(uuid.NewV7()).String()
		ids.windowID = ids.threadID + ":0"
		return ids

	case codexFingerprintFull:
		ids.sessionID = resolveStableCodexDeviceSessionID(deviceSeed)
		ids.threadID = ids.sessionID
		ids.turnID = uuid.Must(uuid.NewV7()).String()
		ids.windowID = ids.threadID + ":0"
		return ids
	}

	return nil
}

func resolveCodexDeviceSessionThreadID(deviceSeed, deviceSessionID, clientSessionID, rootClientSessionID string) string {
	clientSessionID = strings.TrimSpace(clientSessionID)
	rootClientSessionID = strings.TrimSpace(rootClientSessionID)
	if clientSessionID == "" || (rootClientSessionID != "" && clientSessionID == rootClientSessionID) {
		return deviceSessionID
	}
	return resolveConvergedThreadID(deviceSeed, clientSessionID)
}

// extractClientSessionID 从请求头中提取客户端原始的会话标识。
// 优先取 session-id（连字符形式，Codex CLI 标准），回退到 session_id（下划线形式）。
// 返回的值尚未被 isolateOpenAISessionID 改写，是客户端的真实标识。
func extractClientSessionID(h http.Header) string {
	if v := strings.TrimSpace(h.Get("session-id")); v != "" {
		return v
	}
	return strings.TrimSpace(h.Get("session_id"))
}

// codexFingerprintSessionHint 按指纹解析在请求头之后使用的同一优先级，返回请求体中
// 首个由客户端持有的 session：先取 client_metadata，再回退 prompt_cache_key。
// 此函数只读取输入，不做改写。
func codexFingerprintSessionHint(clientMetadata, promptCacheKey any) string {
	switch metadata := clientMetadata.(type) {
	case map[string]any:
		if sessionID, ok := metadata["session_id"].(string); ok {
			if sessionID = strings.TrimSpace(sessionID); sessionID != "" {
				return sessionID
			}
		}
	case map[string]string:
		if sessionID := strings.TrimSpace(metadata["session_id"]); sessionID != "" {
			return sessionID
		}
	}
	if sessionID, ok := promptCacheKey.(string); ok {
		return strings.TrimSpace(sessionID)
	}
	return ""
}

func codexFingerprintSessionHintRaw(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	if sessionID := strings.TrimSpace(gjson.GetBytes(body, "client_metadata.session_id").String()); sessionID != "" {
		return sessionID
	}
	return strings.TrimSpace(gjson.GetBytes(body, "prompt_cache_key").String())
}

// withCodexFingerprintSessionHint 仅在请求头没有 session 且请求体提供 hint 时复制请求头
// 并暂存该 hint；其余情况原样返回，确保路由与客户端请求头保持原值。
func withCodexFingerprintSessionHint(headers http.Header, hint string) http.Header {
	if strings.TrimSpace(hint) == "" || extractClientSessionID(headers) != "" {
		return headers
	}
	cloned := headers.Clone()
	if cloned == nil {
		cloned = make(http.Header, 1)
	}
	cloned.Set("session-id", hint)
	return cloned
}

// resolveCodexFingerprintIDsFromRequest 从客户端原始请求头中提取 session-id，
// 结合账号配置一次性解析收敛 ID 集合。调用方应将返回的 ids 同时传给
// applyCodexFingerprintHeaders 和 applyCodexFingerprintClientMetadata。
func resolveCodexFingerprintIDsFromRequest(account *Account, clientHeaders http.Header) *codexFingerprintIDs {
	if account == nil {
		return nil
	}
	mode := account.GetCodexFingerprintMode()
	clientSessionID := ""
	if clientHeaders != nil {
		clientSessionID = extractClientSessionID(clientHeaders)
	}
	return resolveCodexFingerprintIDsForDeviceSlot(account, clientSessionID, mode, 0)
}

// applyCodexFingerprintHeaders 按预计算的收敛 ID 改写出站 HTTP 头中的设备指纹。
// 在 buildUpstreamRequest 的白名单透传之后、enforceCodexIdentityHeaders 之前调用。
func applyCodexFingerprintHeaders(h http.Header, ids *codexFingerprintIDs) {
	if h == nil || ids == nil {
		return
	}
	if ids.sessionID != "" {
		h.Set("session-id", ids.sessionID)
		h.Set("session_id", ids.sessionID)
	}
	if ids.mode == codexFingerprintOff {
		rewriteCodexTurnMetadataFields(h, codexFingerprintTurnMetadataFields(ids))
		return
	}

	// 所有非 off 模式都收敛 installation_id
	h.Set("x-codex-installation-id", ids.installationID)

	if ids.mode == codexFingerprintDevice {
		rewriteCodexTurnMetadataFields(h, codexFingerprintTurnMetadataFields(ids))
		return
	}

	// session / full 模式：继续改写线程、窗口等收敛头。session 双头已由
	// 当前模式按账号或设备 slot 语义在共同入口完成统一。
	h.Set("x-codex-window-id", ids.windowID)
	h.Set("x-client-request-id", ids.threadID)
	h.Set("thread-id", ids.threadID)

	rewriteCodexTurnMetadataFields(h, codexFingerprintTurnMetadataFields(ids))
}

func codexFingerprintTurnMetadataFields(ids *codexFingerprintIDs) map[string]any {
	if ids == nil {
		return nil
	}
	fields := make(map[string]any, 2)
	if ids.installationID != "" {
		fields["installation_id"] = ids.installationID
	}
	if ids.sandbox != "" {
		fields["sandbox"] = ids.sandbox
	}
	if ids.sessionID != "" {
		fields["session_id"] = ids.sessionID
	}
	if ids.mode == codexFingerprintOff || ids.mode == codexFingerprintDevice {
		return fields
	}
	fields["thread_id"] = ids.threadID
	fields["turn_id"] = ids.turnID
	fields["window_id"] = ids.windowID
	fields["turn_started_at_unix_ms"] = ids.turnStartedAtUnixMs
	return fields
}

// rewriteCodexTurnMetadataFields 解析 x-codex-turn-metadata 头中的 JSON，
// 替换指定字段后回写。合法对象保留未指定字段（如 sandbox、thread_source）；
// 非法/非对象值重建为最小合法 metadata，避免 flat 与 embedded identity 分裂。
func rewriteCodexTurnMetadataFields(h http.Header, fields map[string]any) {
	raw := strings.TrimSpace(h.Get("x-codex-turn-metadata"))
	if raw == "" {
		return
	}
	var metadata map[string]any
	if err := json.Unmarshal([]byte(raw), &metadata); err != nil || metadata == nil {
		metadata = make(map[string]any, len(fields))
	}
	if turnID, ok := fields["turn_id"].(string); ok {
		rewriteLinkedCodexRootTurnID(metadata, turnID)
	}
	for k, v := range fields {
		metadata[k] = v
	}
	rebuilt, err := json.Marshal(metadata)
	if err != nil {
		return
	}
	h.Set("x-codex-turn-metadata", string(rebuilt))
}

// applyCodexFingerprintClientMetadata 按预计算的收敛 ID 改写请求体中的 client_metadata。
// 使用与头改写相同的 ids 实例，确保 turn_id 等随机字段一致。
func applyCodexFingerprintClientMetadata(reqBody map[string]any, ids *codexFingerprintIDs) bool {
	if reqBody == nil || ids == nil {
		return false
	}

	captureCodexFingerprintOriginalBodySessionID(ids, reqBody["client_metadata"])
	captureCodexFingerprintPromptCacheKeyFallback(ids, reqBody["prompt_cache_key"])
	existing, _ := reqBody["client_metadata"].(map[string]any)
	if existing == nil {
		existing = make(map[string]any)
	}

	modified := false
	if applyCodexFingerprintToClientMetadataMap(existing, ids) {
		reqBody["client_metadata"] = existing
		modified = true
	}
	if applyCodexFingerprintPromptCacheKey(reqBody, ids) {
		modified = true
	}
	return modified
}

// applyCodexFingerprintToClientMetadataMap 是 client_metadata 改写的共享核心，
// map 版（非透传，body 已解码）与 raw 字节版（透传热路径）都经由它，保证两条
// 路径的收敛语义永不漂移。
func applyCodexFingerprintToClientMetadataMap(existing map[string]any, ids *codexFingerprintIDs) bool {
	if existing == nil || ids == nil {
		return false
	}

	modified := false
	if ids.sessionID != "" {
		existing["session_id"] = ids.sessionID
		modified = true
	}
	if ids.mode == codexFingerprintOff {
		rawMetadata, hasMetadata := existing["x-codex-turn-metadata"].(string)
		fields := codexFingerprintTurnMetadataFields(ids)
		rewriteClientMetadataEmbeddedTurnMetadata(existing, fields)
		return modified || (hasMetadata && rawMetadata != "" && len(fields) > 0)
	}

	if ids.installationID != "" {
		existing["x-codex-installation-id"] = ids.installationID
		modified = true
	}

	if ids.mode == codexFingerprintDevice {
		rewriteClientMetadataEmbeddedTurnMetadata(existing, codexFingerprintTurnMetadataFields(ids))
		return modified
	}

	// session / full 模式
	rewriteLinkedCodexRootTurnID(existing, ids.turnID)
	existing["thread_id"] = ids.threadID
	existing["turn_id"] = ids.turnID
	existing["x-codex-window-id"] = ids.windowID

	rewriteClientMetadataEmbeddedTurnMetadata(existing, codexFingerprintTurnMetadataFields(ids))
	return true
}

func captureCodexFingerprintOriginalBodySessionID(ids *codexFingerprintIDs, clientMetadata any) {
	if ids == nil || ids.originalBodySessionIDCaptured {
		return
	}
	ids.originalBodySessionIDCaptured = true
	if clientMetadata == nil {
		return
	}
	switch metadata := clientMetadata.(type) {
	case map[string]any:
		if sessionID, ok := metadata["session_id"].(string); ok {
			ids.originalBodySessionID = strings.TrimSpace(sessionID)
		}
	case map[string]string:
		ids.originalBodySessionID = strings.TrimSpace(metadata["session_id"])
	}
	ensureCodexFingerprintSessionID(ids, ids.originalBodySessionID)
}

func captureCodexFingerprintOriginalBodySessionIDRaw(ids *codexFingerprintIDs, value gjson.Result) {
	if ids == nil || ids.originalBodySessionIDCaptured {
		return
	}
	ids.originalBodySessionIDCaptured = true
	if value.Exists() && value.Type == gjson.String {
		ids.originalBodySessionID = strings.TrimSpace(value.String())
	}
	ensureCodexFingerprintSessionID(ids, ids.originalBodySessionID)
}

func ensureCodexFingerprintSessionID(ids *codexFingerprintIDs, clientSessionID string) {
	if ids == nil {
		return
	}
	deviceSeed := codexDeviceSeed(ids.accountSeed, ids.deviceSlot)
	switch ids.mode {
	case codexFingerprintOff:
		if ids.sessionID == "" {
			ids.sessionID = resolveNamespacedCodexSessionID(ids.accountSeed, clientSessionID)
		}
	case codexFingerprintDevice:
		if ids.sessionID == "" {
			ids.sessionID = resolveNamespacedCodexSessionID(deviceSeed, clientSessionID)
		}
	case codexFingerprintSession:
		if ids.clientSessionID == "" && strings.TrimSpace(clientSessionID) != "" {
			ids.threadID = resolveCodexDeviceSessionThreadID(deviceSeed, ids.sessionID, clientSessionID, ids.rootClientSessionID)
			ids.windowID = ids.threadID + ":0"
		}
	}
}

// captureCodexFingerprintPromptCacheKeyFallback 仅在请求头与 client_metadata 都未提供
// 原始 session 时使用 prompt_cache_key。优先级有意保持为：请求头 >
// client_metadata.session_id > prompt_cache_key。
func captureCodexFingerprintPromptCacheKeyFallback(ids *codexFingerprintIDs, value any) {
	if ids == nil || ids.clientSessionID != "" || ids.originalBodySessionID != "" {
		return
	}
	if (ids.mode == codexFingerprintOff || ids.mode == codexFingerprintDevice) && ids.sessionID != "" {
		return
	}
	promptCacheKey, ok := value.(string)
	promptCacheKey = strings.TrimSpace(promptCacheKey)
	if !ok || promptCacheKey == "" {
		return
	}
	ids.originalBodySessionID = promptCacheKey
	ensureCodexFingerprintSessionID(ids, promptCacheKey)
}

// captureCodexFingerprintPromptCacheSessionHint records that prompt_cache_key
// was the request's only client-owned session source before stage synthesized a
// temporary session header for pool selection. Without this provenance, the
// body pass mistakes that same key for an explicit cache override and leaves it
// outside session/full convergence.
func captureCodexFingerprintPromptCacheSessionHint(ids *codexFingerprintIDs, headerSessionID, bodySessionID, promptCacheKey string) {
	if ids == nil || strings.TrimSpace(headerSessionID) != "" || strings.TrimSpace(bodySessionID) != "" {
		return
	}
	promptCacheKey = strings.TrimSpace(promptCacheKey)
	if promptCacheKey != "" {
		ids.originalBodySessionID = promptCacheKey
	}
}

func shouldRewriteCodexFingerprintPromptCacheKey(ids *codexFingerprintIDs, promptCacheKey string) bool {
	if ids == nil || !ids.originalBodySessionIDCaptured || ids.originalBodySessionID == "" || ids.sessionID == "" {
		return false
	}
	if ids.mode != codexFingerprintSession && ids.mode != codexFingerprintFull {
		return false
	}
	return promptCacheKey == ids.originalBodySessionID
}

func applyCodexFingerprintPromptCacheKey(reqBody map[string]any, ids *codexFingerprintIDs) bool {
	if reqBody == nil {
		return false
	}
	promptCacheKey, ok := reqBody["prompt_cache_key"].(string)
	if !ok || strings.TrimSpace(promptCacheKey) == "" || !shouldRewriteCodexFingerprintPromptCacheKey(ids, promptCacheKey) {
		return false
	}
	if promptCacheKey == ids.sessionID {
		return false
	}
	reqBody["prompt_cache_key"] = ids.sessionID
	return true
}

// applyCodexFingerprintClientMetadataRaw 在原始 JSON 字节上改写 client_metadata，
// 供透传路径使用——透传是热路径，禁止对可能高达数十 MB 的 body 做全量
// Unmarshal（见 forwardOpenAIPassthrough 的轻量提取注释）。实现为：gjson 提取
// client_metadata 小对象单独解码，经共享核心改写后 sjson 一次性拼回，body
// 其余字节原样保留；root prompt_cache_key 仅在可证明是 body session 默认值时
// 做标量改写。语义与 applyCodexFingerprintClientMetadata 逐点一致（含
// "非对象值整体替换为收敛集合"的行为）。
func applyCodexFingerprintClientMetadataRaw(body []byte, ids *codexFingerprintIDs) ([]byte, bool, error) {
	if len(body) == 0 || ids == nil {
		return body, false, nil
	}
	// 非 JSON 对象的 body（数组/标量/畸形）没有 client_metadata 语义，
	// sjson 在这类根上写字段会改写整体结构，直接放行保持原样。
	root := gjson.ParseBytes(body)
	if !root.IsObject() {
		captureCodexFingerprintOriginalBodySessionIDRaw(ids, gjson.Result{})
		return body, false, nil
	}

	existing := map[string]any{}
	if cm := gjson.GetBytes(body, "client_metadata"); cm.IsObject() {
		captureCodexFingerprintOriginalBodySessionIDRaw(ids, gjson.GetBytes(body, "client_metadata.session_id"))
		if err := json.Unmarshal([]byte(cm.Raw), &existing); err != nil {
			return body, false, fmt.Errorf("decode client_metadata for fingerprint: %w", err)
		}
	} else {
		captureCodexFingerprintOriginalBodySessionIDRaw(ids, gjson.Result{})
	}
	promptCacheKey := gjson.GetBytes(body, "prompt_cache_key")
	if promptCacheKey.Exists() && promptCacheKey.Type == gjson.String {
		captureCodexFingerprintPromptCacheKeyFallback(ids, promptCacheKey.String())
	}

	next := body
	modified := false
	if applyCodexFingerprintToClientMetadataMap(existing, ids) {
		raw, err := json.Marshal(existing)
		if err != nil {
			return body, false, fmt.Errorf("encode converged client_metadata: %w", err)
		}
		var setErr error
		next, setErr = sjson.SetRawBytes(body, "client_metadata", raw)
		if setErr != nil {
			return body, false, fmt.Errorf("splice converged client_metadata: %w", setErr)
		}
		modified = true
	}
	if promptCacheKey.Exists() && promptCacheKey.Type == gjson.String && strings.TrimSpace(promptCacheKey.String()) != "" && shouldRewriteCodexFingerprintPromptCacheKey(ids, promptCacheKey.String()) {
		rewritten, err := sjson.SetBytes(next, "prompt_cache_key", ids.sessionID)
		if err != nil {
			return body, false, fmt.Errorf("splice converged prompt_cache_key: %w", err)
		}
		next = rewritten
		modified = true
	}
	return next, modified, nil
}

// rewriteClientMetadataEmbeddedTurnMetadata 改写 client_metadata 中内嵌的
// x-codex-turn-metadata JSON 字符串里的指定字段。非法/非对象值会重建，
// 避免 flat client_metadata 与 embedded metadata 暴露两套身份。
func rewriteClientMetadataEmbeddedTurnMetadata(clientMetadata map[string]any, fields map[string]any) {
	raw, ok := clientMetadata["x-codex-turn-metadata"].(string)
	if !ok || raw == "" {
		return
	}
	var metadata map[string]any
	if err := json.Unmarshal([]byte(raw), &metadata); err != nil || metadata == nil {
		metadata = make(map[string]any, len(fields))
	}
	if turnID, ok := fields["turn_id"].(string); ok {
		rewriteLinkedCodexRootTurnID(metadata, turnID)
	}
	for k, v := range fields {
		metadata[k] = v
	}
	if rebuilt, err := json.Marshal(metadata); err == nil {
		clientMetadata["x-codex-turn-metadata"] = string(rebuilt)
	}
}

// rewriteLinkedCodexRootTurnID 保持真实 Codex 的根 turn 不变量：根 turn 的
// root_turn_id 与 turn_id 相同，因此 turn_id 被收敛时二者必须联动；子代理 turn
// 的 root_turn_id 指向祖先 turn，与自身 turn_id 不同，必须原样保留。
func rewriteLinkedCodexRootTurnID(metadata map[string]any, nextTurnID string) {
	if metadata == nil || nextTurnID == "" {
		return
	}
	turnID, turnOK := metadata["turn_id"].(string)
	rootTurnID, rootOK := metadata["root_turn_id"].(string)
	if turnOK && rootOK && rootTurnID == turnID {
		metadata["root_turn_id"] = nextTurnID
	}
}
