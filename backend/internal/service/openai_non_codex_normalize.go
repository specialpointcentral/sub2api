package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/cespare/xxhash/v2"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const (
	openAINonCodexPiProjectionContextKey = "openai_non_codex_pi_projection"
	maxOpenAIPromptCacheKeyRunes         = 64
)

type openAINonCodexPiProjection struct {
	SessionID  string
	Originator string
	UserAgent  string
}

type openAINonCodexTrafficAction string

const (
	openAINonCodexTrafficActionPassthrough openAINonCodexTrafficAction = "passthrough"
	openAINonCodexTrafficActionPiNormalize openAINonCodexTrafficAction = "pi-normalize"
)

type openAINonCodexPiPlatform struct {
	platform string
	release  string
	arch     string
}

func openAINonCodexPiPlatformForPersona(persona codexUAPersona) openAINonCodexPiPlatform {
	switch persona {
	case codexUAPersonaMac:
		return openAINonCodexPiPlatform{platform: "darwin", release: "24.6.0", arch: "arm64"}
	case codexUAPersonaWindows:
		return openAINonCodexPiPlatform{platform: "win32", release: "10.0.26100", arch: "x64"}
	default:
		return openAINonCodexPiPlatform{platform: "linux", release: "6.8.0", arch: "x64"}
	}
}

func renderOpenAINonCodexPiUserAgent(persona codexUAPersona) string {
	platform := openAINonCodexPiPlatformForPersona(persona)
	replacer := strings.NewReplacer(
		"{platform}", platform.platform,
		"{release}", platform.release,
		"{arch}", platform.arch,
	)
	return replacer.Replace(DefaultOpenAINonCodexUserAgent)
}

func resolveOpenAINonCodexPiPersonaSelection(account *Account, seed string, userID int64, devicePoolActive bool) codexUAPersonaSelection {
	if devicePoolActive {
		if state, valid := canonicalCodexDevicePoolState(account.Extra[codexDevicePoolExtraKey]); valid && len(state.Slots) > 0 {
			slot := codexRendezvousPoolSlot(seed, userID, state.Slots)
			if selection, canonical := canonicalCodexUAPersonaSelection(map[string]any{
				"platform": string(slot.Platform),
				"sandbox":  slot.Sandbox,
			}); canonical {
				return selection
			}
		}
	}
	if selection, frozen := canonicalCodexUAPersonaSelection(account.Extra[codexUAPersonaExtraKey]); frozen {
		return selection
	}
	return weightedCodexUAPersonaSelection(seed)
}

func resolveOpenAINonCodexPiProjection(account *Account, stickySession string, userID int64, devicePoolActive bool) (openAINonCodexPiProjection, error) {
	if !isOpenAINonCodexTrafficAccount(account) {
		return openAINonCodexPiProjection{}, errors.New("pi normalization requires an OpenAI OAuth or API key account")
	}
	seed, ok := codexFingerprintSeed(account.Extra)
	if !ok {
		return openAINonCodexPiProjection{}, errors.New("pi normalization requires a stable account fingerprint seed")
	}
	stickySession = strings.TrimSpace(stickySession)
	if stickySession == "" {
		return openAINonCodexPiProjection{}, errors.New("pi normalization requires a downstream sticky session")
	}
	sessionID := resolveNamespacedCodexSessionID(seed, stickySession)
	if sessionID == "" {
		return openAINonCodexPiProjection{}, errors.New("derive pi-normalize session id")
	}
	personaSelection := resolveOpenAINonCodexPiPersonaSelection(account, seed, userID, devicePoolActive)
	return openAINonCodexPiProjection{
		SessionID:  sessionID,
		Originator: DefaultOpenAINonCodexOriginator,
		UserAgent:  renderOpenAINonCodexPiUserAgent(personaSelection.Platform),
	}, nil
}

func truncateOpenAIPromptCacheKey(value string) string {
	chars := []rune(value)
	if len(chars) <= maxOpenAIPromptCacheKeyRunes {
		return value
	}
	return string(chars[:maxOpenAIPromptCacheKeyRunes])
}

func applyOpenAINonCodexPiBodyProjection(body []byte, projection openAINonCodexPiProjection) ([]byte, error) {
	if !json.Valid(body) {
		return nil, errors.New("pi-normalize request body must be valid JSON")
	}
	next, err := sjson.DeleteBytes(body, "client_metadata")
	if err != nil {
		return nil, fmt.Errorf("remove client_metadata for pi-normalize: %w", err)
	}
	next, err = sjson.DeleteBytes(next, "previous_response_id")
	if err != nil {
		return nil, fmt.Errorf("remove previous_response_id for pi-normalize: %w", err)
	}
	set := func(path string, value any) error {
		next, err = sjson.SetBytes(next, path, value)
		if err != nil {
			return fmt.Errorf("set %s for pi-normalize: %w", path, err)
		}
		return nil
	}
	if err := set("store", false); err != nil {
		return nil, err
	}
	if err := set("stream", true); err != nil {
		return nil, err
	}
	include := make([]string, 0, 1)
	seenInclude := make(map[string]struct{})
	if existing := gjson.GetBytes(body, "include"); existing.IsArray() {
		for _, item := range existing.Array() {
			value := item.String()
			if _, seen := seenInclude[value]; seen {
				continue
			}
			seenInclude[value] = struct{}{}
			include = append(include, value)
		}
	}
	const encryptedReasoningInclude = "reasoning.encrypted_content"
	if _, seen := seenInclude[encryptedReasoningInclude]; !seen {
		include = append(include, encryptedReasoningInclude)
	}
	if err := set("include", include); err != nil {
		return nil, err
	}
	if err := set("prompt_cache_key", truncateOpenAIPromptCacheKey(projection.SessionID)); err != nil {
		return nil, err
	}
	return next, nil
}

func applyOpenAINonCodexPiHeadersProjection(headers http.Header, projection openAINonCodexPiProjection) {
	if headers == nil {
		return
	}
	for key := range headers {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(key)), "x-codex-") {
			delete(headers, key)
		}
	}
	headers.Del("conversation_id")
	headers.Del("thread-id")
	headers.Del("version")
	headers.Set("OpenAI-Beta", "responses=experimental")
	headers.Set("originator", projection.Originator)
	headers.Set("user-agent", projection.UserAgent)
	headers.Set("session-id", projection.SessionID)
	// session_id remains within the existing outbound allowlist. When an earlier
	// compatibility stage emitted it, align it instead of leaving two identities.
	if headers.Get("session_id") != "" {
		headers.Set("session_id", projection.SessionID)
	}
	headers.Set("x-client-request-id", projection.SessionID)
}

func isOpenAINonCodexTrafficRequest(c *gin.Context, account *Account) bool {
	if !isOpenAINonCodexTrafficAccount(account) {
		return false
	}
	userAgent := ""
	originator := ""
	if c != nil {
		userAgent = c.GetHeader("User-Agent")
		originator = c.GetHeader("originator")
	}
	// Deliberately classify only the official identity reasons from the shared
	// detector. Whitelist and AppServer candidates are allowed third parties,
	// not official Codex clients, and therefore remain in this policy's scope.
	return !openai.IsCodexOfficialClientRequestStrict(userAgent) &&
		!openai.IsCodexOfficialClientOriginator(originator)
}

func resolveOpenAINonCodexTrafficAction(c *gin.Context, account *Account, transport OpenAIUpstreamTransport) openAINonCodexTrafficAction {
	if !isOpenAINonCodexTrafficRequest(c, account) {
		return openAINonCodexTrafficActionPassthrough
	}
	switch account.GetOpenAINonCodexTrafficPolicy() {
	case NonCodexTrafficPolicyPi:
		// WS has connection-pool identity and reconnect semantics that cannot safely
		// consume the per-request HTTP projection yet. Explicitly degrade to passthrough.
		switch transport {
		case OpenAIUpstreamTransportResponsesWebsocket,
			OpenAIUpstreamTransportResponsesWebsocketV2,
			OpenAIUpstreamTransportResponsesWebsocketV2Ingress:
			return openAINonCodexTrafficActionPassthrough
		default:
			return openAINonCodexTrafficActionPiNormalize
		}
	default:
		return openAINonCodexTrafficActionPassthrough
	}
}

func shouldApplyOpenAINonCodexPiNormalization(c *gin.Context, account *Account, transport OpenAIUpstreamTransport) bool {
	return resolveOpenAINonCodexTrafficAction(c, account, transport) == openAINonCodexTrafficActionPiNormalize
}

func stageOpenAINonCodexPiProjection(c *gin.Context, projection *openAINonCodexPiProjection) {
	if c != nil {
		c.Set(openAINonCodexPiProjectionContextKey, projection)
	}
}

func stagedOpenAINonCodexPiProjection(c *gin.Context) *openAINonCodexPiProjection {
	if c == nil {
		return nil
	}
	value, ok := c.Get(openAINonCodexPiProjectionContextKey)
	if !ok {
		return nil
	}
	projection, _ := value.(*openAINonCodexPiProjection)
	return projection
}

func (s *OpenAIGatewayService) prepareOpenAINonCodexPiProjection(c *gin.Context, account *Account, body []byte, transport OpenAIUpstreamTransport) error {
	stageOpenAINonCodexPiProjection(c, nil)
	if s == nil || isOpenAIResponsesCompactPath(c) || !isOpenAINonCodexTrafficRequest(c, account) {
		return nil
	}
	ctx := openAINonCodexRequestContext(c)
	if !shouldApplyOpenAINonCodexPiNormalization(c, account, transport) {
		return nil
	}
	stickySession := s.GenerateSessionHash(c, body)
	if stickySession == "" {
		stickySession = fmt.Sprintf("%016x", xxhash.Sum64(body))
	}
	stickySession = isolateOpenAISessionID(getAPIKeyIDFromContext(c), stickySession)
	resolvedAccount, err := s.withEnsuredCodexFingerprintSeed(ctx, account)
	if err != nil {
		return fmt.Errorf("ensure pi-normalize account namespace: %w", err)
	}
	userID := Sub2APIUserIDFromContext(ctx)
	devicePoolActive := false
	if s.settingService != nil {
		poolSize := s.settingService.GetOpenAICodexDevicePoolSize(ctx)
		personaEnabled := s.settingService.GetOpenAICodexUAPersonaEnabled(ctx)
		devicePoolActive = poolSize >= 3 && (resolvedAccount.GetCodexFingerprintMode() != codexFingerprintOff ||
			(personaEnabled && resolvedAccount.IsOpenAIOAuth()))
	}
	projection, err := resolveOpenAINonCodexPiProjection(resolvedAccount, stickySession, userID, devicePoolActive)
	if err != nil {
		return err
	}
	stageOpenAINonCodexPiProjection(c, &projection)
	return nil
}

func (s *OpenAIGatewayService) applyOpenAINonCodexPiHTTPProjection(c *gin.Context, account *Account, body []byte) ([]byte, error) {
	if err := s.prepareOpenAINonCodexPiProjection(c, account, body, OpenAIUpstreamTransportHTTPSSE); err != nil {
		return nil, err
	}
	projection := stagedOpenAINonCodexPiProjection(c)
	if projection == nil {
		return body, nil
	}
	return applyOpenAINonCodexPiBodyProjection(body, *projection)
}

func openAINonCodexRequestContext(c *gin.Context) context.Context {
	if c != nil && c.Request != nil {
		return c.Request.Context()
	}
	return context.Background()
}
