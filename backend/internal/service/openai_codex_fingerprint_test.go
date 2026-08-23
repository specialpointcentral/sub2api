package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const testCodexFingerprintSeed = "11111111-1111-4111-8111-111111111111"

type codexPersonaFirstWriterRepo struct {
	AccountRepository
	seed          string
	ensureErr     error
	winner        any
	ensureSeedN   int
	firstWriteN   int
	firstWriteKey string
	candidate     any
	casCurrent    any
	casN          int
}

func (r *codexPersonaFirstWriterRepo) EnsureCodexFingerprintSeed(_ context.Context, _ int64) (string, error) {
	r.ensureSeedN++
	return r.seed, r.ensureErr
}

func (r *codexPersonaFirstWriterRepo) EnsureAccountExtraValue(_ context.Context, _ int64, key string, value any) (any, error) {
	r.firstWriteN++
	r.firstWriteKey = key
	r.candidate = value
	if r.winner != nil {
		return r.winner, nil
	}
	return value, nil
}

func (r *codexPersonaFirstWriterRepo) CompareAndSwapAccountExtraValue(_ context.Context, _ int64, _ string, expected, replacement any) (any, bool, error) {
	r.casN++
	if reflect.DeepEqual(r.casCurrent, expected) {
		r.casCurrent = replacement
		return replacement, true, nil
	}
	return r.casCurrent, false, nil
}

func newTestOAuthAccount(id int64, extra map[string]any) *Account {
	if extra == nil {
		extra = make(map[string]any)
	}
	if _, exists := extra[codexFingerprintSeedExtraKey]; !exists {
		extra[codexFingerprintSeedExtraKey] = testCodexFingerprintSeed
	}
	return &Account{
		ID:       id,
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Extra:    extra,
	}
}

func newTestAPIKeyAccount(id int64, extra map[string]any) *Account {
	if extra == nil {
		extra = make(map[string]any)
	}
	if _, exists := extra[codexFingerprintSeedExtraKey]; !exists {
		extra[codexFingerprintSeedExtraKey] = testCodexFingerprintSeed
	}
	return &Account{
		ID:       id,
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Extra:    extra,
	}
}

// withTestCodexFingerprintSeed makes an OAuth fixture eligible for the
// fail-closed fingerprint boundary while preserving explicit fixture data.
// API-key fixtures and explicitly supplied seeds are intentionally unchanged.
func withTestCodexFingerprintSeed(account *Account) *Account {
	if account == nil || account.Type != AccountTypeOAuth {
		return account
	}
	if account.Extra == nil {
		account.Extra = make(map[string]any)
	}
	if _, exists := account.Extra[codexFingerprintSeedExtraKey]; !exists {
		account.Extra[codexFingerprintSeedExtraKey] = testCodexFingerprintSeed
	}
	return account
}

// --- deriveStableUUIDv4 ---

func TestDeriveStableUUIDv4_Deterministic(t *testing.T) {
	a := deriveStableUUIDv4("test-seed-1")
	b := deriveStableUUIDv4("test-seed-1")
	assert.Equal(t, a, b, "同一种子应返回相同结果")
}

func TestDeriveStableUUIDv4_DifferentSeeds(t *testing.T) {
	a := deriveStableUUIDv4("seed-a")
	b := deriveStableUUIDv4("seed-b")
	assert.NotEqual(t, a, b, "不同种子应返回不同结果")
}

func TestDeriveStableUUIDv4_ValidFormat(t *testing.T) {
	result := deriveStableUUIDv4("test-seed")
	parsed, err := uuid.Parse(result)
	require.NoError(t, err, "应返回合法 UUID 格式")
	assert.Equal(t, uuid.Version(4), parsed.Version(), "应为 UUIDv4")
	assert.Equal(t, uuid.RFC4122, parsed.Variant(), "应为 RFC4122 变体")
}

func TestDeriveStableUUIDv7Golden(t *testing.T) {
	require.Equal(t, "ef2550e2-5c74-790c-b75d-f259aae65dda", deriveStableUUIDv7("fixed-uuidv7-seed"))
}

func TestConvergedCodexIdentityUUIDVersions(t *testing.T) {
	account := newTestOAuthAccount(7, map[string]any{codexFingerprintModeExtraKey: "session"})
	seed, ok := codexFingerprintSeed(account.Extra)
	require.True(t, ok)

	tests := []struct {
		name    string
		value   string
		version uuid.Version
	}{
		{name: "installation", value: resolveConvergedInstallationID(account, seed), version: uuid.Version(4)},
		{name: "session", value: resolveNamespacedCodexSessionID(seed, "real-client-session"), version: uuid.Version(7)},
		{name: "thread", value: resolveConvergedThreadID(seed, "real-client-session"), version: uuid.Version(7)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed, err := uuid.Parse(tt.value)
			require.NoError(t, err)
			require.Equal(t, tt.version, parsed.Version())
			require.Equal(t, uuid.RFC4122, parsed.Variant())
		})
	}
}

// --- GetCodexFingerprintMode ---

func TestGetCodexFingerprintMode(t *testing.T) {
	tests := []struct {
		name     string
		account  *Account
		expected codexFingerprintMode
	}{
		{"nil 账号", nil, codexFingerprintOff},
		{"非 OAuth 账号", &Account{Platform: PlatformOpenAI, Type: "api_key"}, codexFingerprintOff},
		{"OpenAI setup token", &Account{Platform: PlatformOpenAI, Type: AccountTypeSetupToken, Extra: map[string]any{codexFingerprintModeExtraKey: "session"}}, codexFingerprintSession},
		{"Anthropic setup token", &Account{Platform: PlatformAnthropic, Type: AccountTypeSetupToken, Extra: map[string]any{codexFingerprintModeExtraKey: "session"}}, codexFingerprintOff},
		{"非 OpenAI 账号", &Account{Platform: PlatformGrok, Type: AccountTypeAPIKey, Extra: map[string]any{codexFingerprintModeExtraKey: "full"}}, codexFingerprintOff},
		// 收敛是显式 opt-in：缺省/空/非法一律 off（#5610）。存量账号普遍没有这个
		// extra 键，升级不得把它们静默切进收敛。
		{"无 extra 默认 off", newTestOAuthAccount(1, nil), codexFingerprintOff},
		{"空值默认 off", newTestOAuthAccount(1, map[string]any{codexFingerprintModeExtraKey: ""}), codexFingerprintOff},
		{"非法值默认 off", newTestOAuthAccount(1, map[string]any{codexFingerprintModeExtraKey: "invalid"}), codexFingerprintOff},
		{"显式 off", newTestOAuthAccount(1, map[string]any{codexFingerprintModeExtraKey: "off"}), codexFingerprintOff},
		{"device", newTestOAuthAccount(1, map[string]any{codexFingerprintModeExtraKey: "device"}), codexFingerprintDevice},
		{"session", newTestOAuthAccount(1, map[string]any{codexFingerprintModeExtraKey: "session"}), codexFingerprintSession},
		{"full", newTestOAuthAccount(1, map[string]any{codexFingerprintModeExtraKey: "full"}), codexFingerprintFull},
		{"API key 缺省 off", newTestAPIKeyAccount(2, nil), codexFingerprintOff},
		{"API key 显式 off", newTestAPIKeyAccount(2, map[string]any{codexFingerprintModeExtraKey: "off"}), codexFingerprintOff},
		{"API key device", newTestAPIKeyAccount(2, map[string]any{codexFingerprintModeExtraKey: "device"}), codexFingerprintDevice},
		{"API key session", newTestAPIKeyAccount(2, map[string]any{codexFingerprintModeExtraKey: "session"}), codexFingerprintSession},
		{"API key full", newTestAPIKeyAccount(2, map[string]any{codexFingerprintModeExtraKey: "full"}), codexFingerprintFull},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.account.GetCodexFingerprintMode())
		})
	}
}

func TestResolveCodexFingerprintIDsForOutbound_APIKeyOffAlwaysFullyPassesThrough(t *testing.T) {
	svc := &OpenAIGatewayService{accountRepo: &codexPersonaFirstWriterRepo{seed: testCodexFingerprintSeed}}
	c := newFingerprintStageTestContext(t)
	c.Request.Header.Set("session-id", "client-session")

	for _, extra := range []map[string]any{
		{codexFingerprintModeExtraKey: "off"},
		{codexFingerprintModeExtraKey: "off", codexFingerprintSeedExtraKey: testCodexFingerprintSeed},
	} {
		account := &Account{ID: 200, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Extra: extra}
		ids, err := svc.resolveCodexFingerprintIDsForOutbound(c, account, c.Request.Header, true)
		require.NoError(t, err)
		require.Nil(t, ids, "API key off must not namespace or rewrite client identity even when a historical seed remains")
	}
}

func TestResolveCodexFingerprintIDsForOutbound_OAuthOffKeepsSessionNamespace(t *testing.T) {
	svc := &OpenAIGatewayService{}
	c := newFingerprintStageTestContext(t)
	c.Request.Header.Set("session-id", "client-session")
	account := newTestOAuthAccount(201, map[string]any{codexFingerprintModeExtraKey: "off"})

	ids, err := svc.resolveCodexFingerprintIDsForOutbound(c, account, c.Request.Header, true)

	require.NoError(t, err)
	require.NotNil(t, ids)
	require.Equal(t, codexFingerprintOff, ids.mode)
	// Independently derived from SHA-256(seed + ":client-session") with the
	// UUIDv7 version and RFC 4122 variant bits applied.
	require.Equal(t, "b3d6411d-9d52-79ec-914b-644e6a8f40b3", ids.sessionID)
}

func TestResolveCodexFingerprintIDsForOutbound_APIKeyEnabledEnsuresSeedOnDemand(t *testing.T) {
	repo := &codexPersonaFirstWriterRepo{seed: testCodexFingerprintSeed}
	svc := &OpenAIGatewayService{accountRepo: repo}
	c := newFingerprintStageTestContext(t)
	c.Request.Header.Set("session-id", "client-session")
	account := &Account{
		ID:       202,
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Extra:    map[string]any{codexFingerprintModeExtraKey: "session"},
	}

	ids, err := svc.resolveCodexFingerprintIDsForOutbound(c, account, c.Request.Header, true)

	require.NoError(t, err)
	require.NotNil(t, ids)
	require.Equal(t, codexFingerprintSession, ids.mode)
	require.NotEmpty(t, ids.installationID)
	require.Equal(t, 1, repo.ensureSeedN)
}

func TestStageCodexFingerprintSeams_APIKeyOffPreservesMapAndRawBody(t *testing.T) {
	account := newTestAPIKeyAccount(203, map[string]any{codexFingerprintModeExtraKey: "off"})
	svc := &OpenAIGatewayService{}

	mapContext := newFingerprintStageTestContext(t)
	mapContext.Request.Header.Set("session-id", "client-session")
	reqBody := map[string]any{
		"model":            "gpt-5.6-sol",
		"prompt_cache_key": "client-session",
		"client_metadata":  map[string]any{"session_id": "client-session"},
	}
	wantMap := maps.Clone(reqBody)
	clientMetadata, ok := reqBody["client_metadata"].(map[string]any)
	require.True(t, ok)
	wantMap["client_metadata"] = maps.Clone(clientMetadata)
	changed, err := svc.stageCodexFingerprintForMap(mapContext, account, reqBody, "client-session", true)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, wantMap, reqBody)
	require.Nil(t, stagedCodexFingerprintIDs(mapContext, account))

	rawContext := newFingerprintStageTestContext(t)
	rawContext.Request.Header.Set("session-id", "client-session")
	rawBody := []byte(`{"model":"gpt-5.6-sol","prompt_cache_key":"client-session","client_metadata":{"session_id":"client-session"}}`)
	rewritten, changed, err := svc.stageCodexFingerprintForRaw(rawContext, account, rawBody, true)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, rawBody, rewritten)
	require.Nil(t, stagedCodexFingerprintIDs(rawContext, account))
}

func TestStageCodexFingerprintSeams_APIKeySessionRewritesMapAndRawBody(t *testing.T) {
	account := newTestAPIKeyAccount(204, map[string]any{codexFingerprintModeExtraKey: "session"})
	svc := &OpenAIGatewayService{}
	const wantSession = "4065c8ec-c2ce-78bd-9198-53c4c5e30d6e"

	mapContext := newFingerprintStageTestContext(t)
	mapContext.Request.Header.Set("session-id", "client-session")
	reqBody := map[string]any{
		"model":            "gpt-5.6-sol",
		"prompt_cache_key": "client-session",
		"client_metadata":  map[string]any{"session_id": "client-session"},
	}
	changed, err := svc.stageCodexFingerprintForMap(mapContext, account, reqBody, "client-session", true)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, wantSession, reqBody["prompt_cache_key"])
	clientMetadata, ok := reqBody["client_metadata"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, wantSession, clientMetadata["session_id"])
	require.NotNil(t, stagedCodexFingerprintIDs(mapContext, account))

	rawContext := newFingerprintStageTestContext(t)
	rawContext.Request.Header.Set("session-id", "client-session")
	rawBody := []byte(`{"model":"gpt-5.6-sol","prompt_cache_key":"client-session","client_metadata":{"session_id":"client-session"}}`)
	rewritten, changed, err := svc.stageCodexFingerprintForRaw(rawContext, account, rawBody, true)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, wantSession, gjson.GetBytes(rewritten, "prompt_cache_key").String())
	require.Equal(t, wantSession, gjson.GetBytes(rewritten, "client_metadata.session_id").String())
	require.NotNil(t, stagedCodexFingerprintIDs(rawContext, account))
}

func TestStageCodexFingerprintSeams_APIKeySessionUsesPromptCacheFallback(t *testing.T) {
	account := newTestAPIKeyAccount(205, map[string]any{codexFingerprintModeExtraKey: "session"})
	svc := &OpenAIGatewayService{}
	const wantSession = "4065c8ec-c2ce-78bd-9198-53c4c5e30d6e"

	mapContext := newFingerprintStageTestContext(t)
	reqBody := map[string]any{
		"model":            "gpt-5.6-sol",
		"prompt_cache_key": "client-session",
	}
	changed, err := svc.stageCodexFingerprintForMap(mapContext, account, reqBody, nil, true)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, wantSession, reqBody["prompt_cache_key"])
	clientMetadata, ok := reqBody["client_metadata"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, wantSession, clientMetadata["session_id"])

	rawContext := newFingerprintStageTestContext(t)
	rawBody := []byte(`{"model":"gpt-5.6-sol","prompt_cache_key":"client-session"}`)
	rewritten, changed, err := svc.stageCodexFingerprintForRaw(rawContext, account, rawBody, true)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, wantSession, gjson.GetBytes(rewritten, "prompt_cache_key").String())
	require.Equal(t, wantSession, gjson.GetBytes(rewritten, "client_metadata.session_id").String())
}

// --- resolveConvergedInstallationID ---

func TestResolveConvergedInstallationID_UsesDeviceID(t *testing.T) {
	account := newTestOAuthAccount(1, map[string]any{"openai_device_id": "real-device-id"})
	assert.Equal(t, "real-device-id", resolveConvergedInstallationID(account, testCodexFingerprintSeed))
}

func TestResolveConvergedInstallationID_DerivesFromSeed(t *testing.T) {
	account := newTestOAuthAccount(42, nil)
	result := resolveConvergedInstallationID(account, testCodexFingerprintSeed)
	_, err := uuid.Parse(result)
	require.NoError(t, err, "派生值应为合法 UUID")
	assert.Equal(t, result, resolveConvergedInstallationID(account, testCodexFingerprintSeed), "确定性")
}

func TestResolveConvergedInstallationID_DifferentSeeds(t *testing.T) {
	account := newTestOAuthAccount(1, nil)
	a := resolveConvergedInstallationID(account, testCodexFingerprintSeed)
	b := resolveConvergedInstallationID(account, "22222222-2222-4222-8222-222222222222")
	assert.NotEqual(t, a, b)
}

func TestResolveCodexFingerprintIDsPoolOneUsesLegacyDeviceSlot(t *testing.T) {
	account := newTestOAuthAccount(42, map[string]any{codexFingerprintModeExtraKey: "session"})

	ids := resolveCodexFingerprintIDsForDeviceSlot(account, "client-session", codexFingerprintSession, 0)
	require.NotNil(t, ids)
	require.Zero(t, ids.deviceSlot)
	require.Equal(t, "4065c8ec-c2ce-78bd-9198-53c4c5e30d6e", ids.sessionID)
	require.Equal(t, "5483e12a-a512-7334-899e-d8d3a9b29dc8", ids.threadID)
}

func TestResolveCodexFingerprintIDsKeepsAccountsIsolatedAcrossAllModes(t *testing.T) {
	const rawSession = "user-original-session"
	accountA := newTestOAuthAccount(51, map[string]any{
		codexFingerprintSeedExtraKey: testCodexFingerprintSeed,
	})
	accountB := newTestOAuthAccount(52, map[string]any{
		codexFingerprintSeedExtraKey: "22222222-2222-4222-8222-222222222222",
	})
	for _, mode := range []codexFingerprintMode{
		codexFingerprintOff,
		codexFingerprintDevice,
		codexFingerprintSession,
		codexFingerprintFull,
	} {
		t.Run(string(mode), func(t *testing.T) {
			idsA := resolveCodexFingerprintIDsForDeviceSlot(accountA, rawSession, mode, 4)
			idsB := resolveCodexFingerprintIDsForDeviceSlot(accountB, rawSession, mode, 4)
			require.NotNil(t, idsA)
			require.NotNil(t, idsB)
			require.NotEqual(t, idsA.sessionID, idsB.sessionID)
			other := resolveCodexFingerprintIDsForDeviceSlot(accountA, "other-session", mode, 4)
			if mode == codexFingerprintOff || mode == codexFingerprintDevice {
				require.NotEqual(t, idsA.sessionID, other.sessionID)
			} else {
				require.Equal(t, idsA.sessionID, other.sessionID)
			}
		})
	}
}

func TestResolveCodexFingerprintIDsPreservesSessionAndFullConvergenceShapes(t *testing.T) {
	account := newTestOAuthAccount(53, map[string]any{codexFingerprintSeedExtraKey: testCodexFingerprintSeed})

	sessionA := resolveCodexFingerprintIDsForDeviceSlot(account, "client-A", codexFingerprintSession, 0)
	sessionB := resolveCodexFingerprintIDsForDeviceSlot(account, "client-B", codexFingerprintSession, 0)
	require.NotNil(t, sessionA)
	require.NotNil(t, sessionB)
	require.Equal(t, sessionA.installationID, sessionB.installationID)
	require.Equal(t, sessionA.sessionID, sessionB.sessionID)
	require.NotEqual(t, sessionA.threadID, sessionB.threadID)
	require.NotEqual(t, sessionA.windowID, sessionB.windowID)

	fullA := resolveCodexFingerprintIDsForDeviceSlot(account, "client-A", codexFingerprintFull, 0)
	fullB := resolveCodexFingerprintIDsForDeviceSlot(account, "client-B", codexFingerprintFull, 0)
	require.NotNil(t, fullA)
	require.NotNil(t, fullB)
	require.Equal(t, fullA.installationID, fullB.installationID)
	require.Equal(t, fullA.sessionID, fullB.sessionID)
	require.Equal(t, fullA.sessionID, fullA.threadID)
	require.Equal(t, fullA.threadID, fullB.threadID)
	require.Equal(t, fullA.windowID, fullB.windowID)
	require.NotEqual(t, fullA.turnID, fullB.turnID)
}

func TestApplyCodexFingerprintClientMetadataDerivesSessionThreadFromBodyWhenHeaderMissing(t *testing.T) {
	account := newTestOAuthAccount(55, map[string]any{codexFingerprintModeExtraKey: "session"})
	ids := resolveCodexFingerprintIDs(account, "", codexFingerprintSession)
	require.NotNil(t, ids)
	body := map[string]any{"client_metadata": map[string]any{
		"session_id":            "body-original-session",
		"x-codex-turn-metadata": `{"session_id":"body-original-session","thread_id":"body-thread","window_id":"body-window"}`,
	}}

	require.True(t, applyCodexFingerprintClientMetadata(body, ids))

	const wantSession = "4065c8ec-c2ce-78bd-9198-53c4c5e30d6e"
	const wantThread = "8341a780-33b6-7322-aa37-1d55bc4db8b0"
	require.Equal(t, wantSession, ids.sessionID)
	require.Equal(t, wantThread, ids.threadID)
	require.Equal(t, wantThread+":0", ids.windowID)
	metadata, ok := body["client_metadata"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, wantSession, metadata["session_id"])
	require.Equal(t, wantThread, metadata["thread_id"])
}

func TestApplyCodexFingerprintClientMetadataUsesPromptCacheKeyAsSessionFallbackInAllModes(t *testing.T) {
	// This literal is independently derived from SHA-256("11111111-1111-4111-8111-111111111111:prompt-cache-only"),
	// with the RFC 9562 v7 and RFC 4122 variant bits set. Do not compute this expectation via production helpers.
	const wantSession = "3d8441fc-7536-79fe-9822-629201d97d5a"

	for _, mode := range []codexFingerprintMode{codexFingerprintOff, codexFingerprintDevice, codexFingerprintSession, codexFingerprintFull} {
		t.Run(string(mode), func(t *testing.T) {
			account := newTestOAuthAccount(56, map[string]any{
				codexFingerprintModeExtraKey: string(mode),
				codexFingerprintSeedExtraKey: testCodexFingerprintSeed,
			})
			ids := resolveCodexFingerprintIDsForDeviceSlot(account, "", mode, 0)
			require.NotNil(t, ids)
			body := map[string]any{"prompt_cache_key": "prompt-cache-only"}

			require.True(t, applyCodexFingerprintClientMetadata(body, ids))
			modeSession := wantSession
			if mode == codexFingerprintSession || mode == codexFingerprintFull {
				modeSession = "4065c8ec-c2ce-78bd-9198-53c4c5e30d6e"
			}
			require.Equal(t, modeSession, ids.sessionID)
			if mode == codexFingerprintSession || mode == codexFingerprintFull {
				require.Equal(t, modeSession, body["prompt_cache_key"])
			} else {
				require.Equal(t, "prompt-cache-only", body["prompt_cache_key"])
			}
			metadata, ok := body["client_metadata"].(map[string]any)
			require.True(t, ok)
			require.Equal(t, modeSession, metadata["session_id"])
		})
	}
}

func TestApplyCodexFingerprintClientMetadataRawUsesPromptCacheKeyAsSessionFallbackInAllModes(t *testing.T) {
	// Independently derived as in the decoded test above, for raw-passthrough input.
	const wantSession = "38976fe7-b057-72f7-b9d6-8dd53800d2ac"

	for _, mode := range []codexFingerprintMode{codexFingerprintOff, codexFingerprintDevice, codexFingerprintSession, codexFingerprintFull} {
		t.Run(string(mode), func(t *testing.T) {
			account := newTestOAuthAccount(57, map[string]any{
				codexFingerprintModeExtraKey: string(mode),
				codexFingerprintSeedExtraKey: testCodexFingerprintSeed,
			})
			ids := resolveCodexFingerprintIDsForDeviceSlot(account, "", mode, 0)
			require.NotNil(t, ids)

			out, changed, err := applyCodexFingerprintClientMetadataRaw([]byte(`{"prompt_cache_key":"raw-prompt-cache-only"}`), ids)
			require.NoError(t, err)
			require.True(t, changed)
			modeSession := wantSession
			if mode == codexFingerprintSession || mode == codexFingerprintFull {
				modeSession = "4065c8ec-c2ce-78bd-9198-53c4c5e30d6e"
			}
			require.Equal(t, modeSession, ids.sessionID)
			if mode == codexFingerprintSession || mode == codexFingerprintFull {
				require.Equal(t, modeSession, gjson.GetBytes(out, "prompt_cache_key").String())
			} else {
				require.Equal(t, "raw-prompt-cache-only", gjson.GetBytes(out, "prompt_cache_key").String())
			}
			require.Equal(t, modeSession, gjson.GetBytes(out, "client_metadata.session_id").String())
		})
	}
}

func TestOpenAIGatewayService_FailsClosedWhenSeedlessOAuthCannotEnsureFingerprintSeed(t *testing.T) {
	newAccount := func() *Account {
		return &Account{
			ID:          58,
			Platform:    PlatformOpenAI,
			Type:        AccountTypeOAuth,
			Concurrency: 1,
			Credentials: map[string]any{"access_token": "oauth-token", "chatgpt_account_id": "chatgpt-account"},
			Status:      StatusActive,
			Schedulable: true,
		}
	}

	for _, tt := range []struct {
		name string
		repo AccountRepository
	}{
		{name: "repository lacks seed capability", repo: &stubOpenAIAccountRepo{}},
		{name: "seed ensure returns error", repo: &codexPersonaFirstWriterRepo{ensureErr: errors.New("seed persistence unavailable")}},
		{name: "seed ensure returns malformed value", repo: &codexPersonaFirstWriterRepo{seed: "not-a-uuid"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			c.Request.Header.Set("session-id", "seed-required-header-session")
			upstream := &httpUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       http.NoBody,
			}}
			svc := &OpenAIGatewayService{cfg: &config.Config{}, accountRepo: tt.repo, httpUpstream: upstream}

			result, err := svc.Forward(context.Background(), c, newAccount(), []byte(`{"model":"gpt-5.2","input":"hello"}`))
			require.Error(t, err)
			require.Nil(t, result)
			require.Nil(t, upstream.lastReq, "seedless OAuth must fail before any upstream request")
		})
	}
}

func TestOpenAIGatewayService_SeedlessOffWithoutSessionDoesNotRequireFingerprintSeed(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusBadRequest,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"captured upstream request"}}`)),
	}}
	svc := &OpenAIGatewayService{
		cfg:          &config.Config{},
		accountRepo:  &stubOpenAIAccountRepo{},
		httpUpstream: upstream,
	}
	account := &Account{
		ID:          59,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{"access_token": "oauth-token", "chatgpt_account_id": "chatgpt-account"},
		Status:      StatusActive,
		Schedulable: true,
	}

	_, err := svc.Forward(context.Background(), c, account, []byte(`{"model":"gpt-5.2","input":"hello"}`))

	require.Error(t, err, "the captured upstream 400 remains the request result")
	require.NotNil(t, upstream.lastReq, "seedless off-mode request without any session source must reach upstream")
}

func TestWithCodexFingerprintSessionHintUsesBodyPrecedenceWithoutMutatingHeaders(t *testing.T) {
	noHeaders := http.Header(nil)
	var hinted http.Header
	require.NotPanics(t, func() {
		hinted = withCodexFingerprintSessionHint(noHeaders, codexFingerprintSessionHint(map[string]any{"session_id": "body-session"}, "cache-session"))
	})
	require.Equal(t, "body-session", hinted.Get("session-id"))
	require.Nil(t, noHeaders, "body-only session hints must not materialize or mutate original request headers")

	headers := http.Header{"User-Agent": []string{"client"}}
	cacheHinted := withCodexFingerprintSessionHint(headers, codexFingerprintSessionHint(nil, "cache-session"))
	require.Equal(t, "cache-session", cacheHinted.Get("session-id"))
	require.Empty(t, headers.Get("session-id"), "hint must be confined to the resolver clone")

	headers.Set("session_id", "header-session")
	headerPreferred := withCodexFingerprintSessionHint(headers, "body-session")
	require.Equal(t, "header-session", extractClientSessionID(headerPreferred))
	require.Empty(t, headers.Get("session-id"), "existing header session must not be rewritten")
}

func TestResolveCodexFingerprintIDsForOutboundEnsuresSeedForOffMode(t *testing.T) {
	repo := &codexPersonaFirstWriterRepo{seed: testCodexFingerprintSeed}
	svc := &OpenAIGatewayService{accountRepo: repo, cfg: &config.Config{}}
	account := newTestOAuthAccount(54, map[string]any{codexFingerprintModeExtraKey: "off"})
	delete(account.Extra, codexFingerprintSeedExtraKey)
	c := newFingerprintStageTestContext(t)
	c.Request.Header.Set("session-id", "user-session")

	ids, err := svc.resolveCodexFingerprintIDsForOutbound(c, account, c.Request.Header, true)

	require.NoError(t, err)
	require.NotNil(t, ids)
	require.Equal(t, 1, repo.ensureSeedN)
	require.Equal(t, deriveStableUUIDv7(testCodexFingerprintSeed+":user-session"), ids.sessionID)
	require.NotContains(t, account.Extra, codexFingerprintSeedExtraKey)
}

func TestResolveCodexFingerprintIDsModeMatrix(t *testing.T) {
	account := newTestOAuthAccount(61, nil)
	const clientSession = "real-user-session"

	off := resolveCodexFingerprintIDsForDeviceSlot(account, clientSession, codexFingerprintOff, 7)
	require.Empty(t, off.installationID)
	require.Equal(t, resolveNamespacedCodexSessionID(testCodexFingerprintSeed, clientSession), off.sessionID)
	require.Empty(t, off.threadID)

	device := resolveCodexFingerprintIDsForDeviceSlot(account, clientSession, codexFingerprintDevice, 7)
	require.Equal(t, "5da4983e-ed9c-4ce5-8dfe-921ac95c1b2d", device.installationID)
	require.Equal(t, "a1dbc76f-e12c-7607-b06f-0e1c9dd210a8", device.sessionID)
	require.Empty(t, device.threadID)

	session := resolveCodexFingerprintIDsForDeviceSlot(account, clientSession, codexFingerprintSession, 7)
	require.Equal(t, device.installationID, session.installationID)
	require.Equal(t, "9de6ae31-bbd6-7df2-bd6b-f87a873aca7d", session.sessionID)
	require.Equal(t, "9e14e6a4-47d0-736f-a3ff-1988bfabfe17", session.threadID)

	full := resolveCodexFingerprintIDsForDeviceSlot(account, clientSession, codexFingerprintFull, 7)
	require.Equal(t, device.installationID, full.installationID)
	require.Equal(t, session.sessionID, full.sessionID)
	require.Equal(t, full.sessionID, full.threadID)
}

func TestResolveCodexFingerprintIDsFullUsesDistinctSessionPerSlot(t *testing.T) {
	account := newTestOAuthAccount(62, nil)
	first := resolveCodexFingerprintIDsForDeviceSlot(account, "same-client", codexFingerprintFull, 1)
	second := resolveCodexFingerprintIDsForDeviceSlot(account, "same-client", codexFingerprintFull, 2)
	require.NotEqual(t, first.installationID, second.installationID)
	require.NotEqual(t, first.sessionID, second.sessionID)
	require.Equal(t, first.sessionID, first.threadID)
	require.Equal(t, second.sessionID, second.threadID)
}

func TestResolveCodexFingerprintIDsSessionKeepsRootThreadEqualToSession(t *testing.T) {
	account := newTestOAuthAccount(63, nil)
	root := resolveCodexFingerprintIDsForDeviceSlotWithRoot(account, "root-client-session", codexFingerprintSession, 3, "root-client-session")
	child := resolveCodexFingerprintIDsForDeviceSlotWithRoot(account, "child-client-session", codexFingerprintSession, 3, "root-client-session")
	require.Equal(t, root.sessionID, root.threadID)
	require.Equal(t, root.sessionID, child.sessionID)
	require.NotEqual(t, child.sessionID, child.threadID)
}

// --- resolveConvergedThreadID ---

func TestResolveConvergedThreadID_PerClientSession(t *testing.T) {
	a := resolveConvergedThreadID(testCodexFingerprintSeed, "session-aaa")
	b := resolveConvergedThreadID(testCodexFingerprintSeed, "session-bbb")
	assert.NotEqual(t, a, b, "不同客户端 session 应得到不同 thread_id")
}

func TestResolveConvergedThreadID_Deterministic(t *testing.T) {
	a := resolveConvergedThreadID(testCodexFingerprintSeed, "session-aaa")
	b := resolveConvergedThreadID(testCodexFingerprintSeed, "session-aaa")
	assert.Equal(t, a, b, "同一客户端 session 应得到相同 thread_id")
}

func TestResolveConvergedThreadID_EmptySession(t *testing.T) {
	assert.Equal(t, "", resolveConvergedThreadID(testCodexFingerprintSeed, ""))
}

// --- off/default-off 模式：有合法 seed 时仅建立 namespaced session ---

func TestResolveCodexFingerprintIDsFromRequest_ExplicitOff(t *testing.T) {
	account := newTestOAuthAccount(1, map[string]any{codexFingerprintModeExtraKey: "off"})
	headers := make(http.Header)
	headers.Set("session-id", "explicit-off-session")
	ids := resolveCodexFingerprintIDsFromRequest(account, headers)
	require.NotNil(t, ids)
	require.Equal(t, codexFingerprintOff, ids.mode)
	// Independently derived from SHA-256("11111111-1111-4111-8111-111111111111:explicit-off-session")
	// after applying UUIDv7/RFC4122 bits; no production helper is used for the expectation.
	require.Equal(t, "5c107a65-2723-7388-af7b-3a509381c824", ids.sessionID)
	require.Empty(t, ids.installationID)
	require.Empty(t, ids.threadID)
	require.Empty(t, ids.windowID)
}

// 未显式配置仍为 off：不收敛 device/thread/window，但有合法系统 seed 时
// 必须将原始 session 放入账号命名空间。
func TestResolveCodexFingerprintIDsFromRequest_DefaultIsOff(t *testing.T) {
	account := newTestOAuthAccount(1, nil)
	headers := make(http.Header)
	headers.Set("session_id", "default-off-session")
	ids := resolveCodexFingerprintIDsFromRequest(account, headers)
	require.NotNil(t, ids)
	require.Equal(t, codexFingerprintOff, ids.mode)
	// Independently derived from SHA-256("11111111-1111-4111-8111-111111111111:default-off-session")
	// after applying UUIDv7/RFC4122 bits; no production helper is used for the expectation.
	require.Equal(t, "43624239-45a2-715d-b4d0-dc55f4c51540", ids.sessionID)
	require.Empty(t, ids.installationID)
	require.Empty(t, ids.threadID)
	require.Empty(t, ids.windowID)
}

// 管理员显式 opt-in 的账号行为不变。
func TestResolveCodexFingerprintIDsFromRequest_ExplicitOptInHonored(t *testing.T) {
	for _, mode := range []string{"device", "session", "full"} {
		t.Run(mode, func(t *testing.T) {
			account := newTestOAuthAccount(1, map[string]any{codexFingerprintModeExtraKey: mode})
			ids := resolveCodexFingerprintIDsFromRequest(account, nil)
			require.NotNil(t, ids, "显式配置必须生效")
			assert.Equal(t, codexFingerprintMode(mode), ids.mode)
			assert.NotEmpty(t, ids.installationID)
		})
	}
}

func TestResolveCodexFingerprintIDsFromRequest_EnabledModesRequireValidSeed(t *testing.T) {
	for _, tt := range []struct {
		name  string
		extra map[string]any
	}{
		{name: "missing", extra: map[string]any{codexFingerprintModeExtraKey: "device"}},
		{name: "missing with device override", extra: map[string]any{codexFingerprintModeExtraKey: "device", "openai_device_id": "real-device"}},
		{name: "blank", extra: map[string]any{codexFingerprintModeExtraKey: "session", codexFingerprintSeedExtraKey: ""}},
		{name: "uppercase", extra: map[string]any{codexFingerprintModeExtraKey: "full", codexFingerprintSeedExtraKey: "11111111-1111-4111-8111-AAAAAAAAAAAA"}},
		{name: "nil uuid", extra: map[string]any{codexFingerprintModeExtraKey: "device", codexFingerprintSeedExtraKey: "00000000-0000-0000-0000-000000000000"}},
		{name: "non string", extra: map[string]any{codexFingerprintModeExtraKey: "session", codexFingerprintSeedExtraKey: 123}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: tt.extra}
			require.Nil(t, resolveCodexFingerprintIDsFromRequest(account, nil))
		})
	}
}

// --- applyCodexFingerprintHeaders: off 模式 ---

func TestApplyCodexFingerprintHeaders_OffMode(t *testing.T) {
	h := http.Header{}
	h.Set("x-codex-installation-id", "original-install-id")
	h.Set("x-codex-window-id", "original-window-id")

	applyCodexFingerprintHeaders(h, nil)

	assert.Equal(t, "original-install-id", h.Get("x-codex-installation-id"), "nil ids 不改写")
	assert.Equal(t, "original-window-id", h.Get("x-codex-window-id"), "nil ids 不改写")
}

// --- applyCodexFingerprintHeaders: device 模式 ---

func TestApplyCodexFingerprintHeaders_DeviceMode(t *testing.T) {
	account := newTestOAuthAccount(1, map[string]any{
		codexFingerprintModeExtraKey: "device",
		"openai_device_id":           "converged-device",
	})
	turnMetadata := `{"installation_id":"user-install","session_id":"user-session","sandbox":"seccomp"}`
	h := http.Header{}
	h.Set("x-codex-installation-id", "user-install")
	h.Set("x-codex-window-id", "user-window:0")
	h.Set("session-id", "user-session")
	h.Set("session_id", "other-session-shape")
	h.Set("x-codex-turn-metadata", turnMetadata)

	ids := resolveCodexFingerprintIDsFromRequest(account, h)
	applyCodexFingerprintHeaders(h, ids)

	wantSession := resolveNamespacedCodexSessionID(testCodexFingerprintSeed, "user-session")
	assert.Equal(t, "converged-device", h.Get("x-codex-installation-id"), "installation_id 应收敛")
	assert.Equal(t, "user-window:0", h.Get("x-codex-window-id"), "device 模式不改写 window_id")
	assert.Equal(t, wantSession, h.Get("session-id"))
	assert.Equal(t, wantSession, h.Get("session_id"))

	var meta map[string]any
	require.NoError(t, json.Unmarshal([]byte(h.Get("x-codex-turn-metadata")), &meta))
	assert.Equal(t, "converged-device", meta["installation_id"])
	assert.Equal(t, wantSession, meta["session_id"])
	assert.Equal(t, "seccomp", meta["sandbox"], "非指纹字段保留原样")
}

// --- applyCodexFingerprintHeaders: session 模式 ---

func TestApplyCodexFingerprintHeaders_SessionMode(t *testing.T) {
	account := newTestOAuthAccount(1, map[string]any{
		codexFingerprintModeExtraKey: "session",
	})
	clientHeaders := http.Header{}
	clientHeaders.Set("session-id", "client-session-aaa")

	turnMetadata := `{"installation_id":"user-install","session_id":"user-session","thread_id":"user-thread","turn_id":"user-turn","window_id":"user-thread:0","sandbox":"seccomp","thread_source":"user"}`
	h := http.Header{}
	h.Set("x-codex-installation-id", "user-install")
	h.Set("x-codex-window-id", "user-thread:0")
	h.Set("x-codex-turn-metadata", turnMetadata)
	h.Set("x-client-request-id", "user-thread")

	ids := resolveCodexFingerprintIDsFromRequest(account, clientHeaders)
	applyCodexFingerprintHeaders(h, ids)

	seed, ok := codexFingerprintSeed(account.Extra)
	require.True(t, ok)
	convergedInstall := resolveConvergedInstallationID(account, seed)
	convergedSession := resolveStableCodexDeviceSessionID(seed)
	convergedThread := resolveConvergedThreadID(seed, "client-session-aaa")

	assert.Equal(t, convergedInstall, h.Get("x-codex-installation-id"))
	assert.Equal(t, convergedSession, h.Get("session-id"))
	assert.Equal(t, convergedSession, h.Get("session_id"), "下划线形式也应被改写")
	assert.Equal(t, convergedThread, h.Get("thread-id"))
	assert.Equal(t, convergedThread, h.Get("x-client-request-id"))
	assert.Equal(t, convergedThread+":0", h.Get("x-codex-window-id"))

	var meta map[string]any
	require.NoError(t, json.Unmarshal([]byte(h.Get("x-codex-turn-metadata")), &meta))
	assert.Equal(t, convergedInstall, meta["installation_id"])
	assert.Equal(t, convergedSession, meta["session_id"])
	assert.Equal(t, convergedThread, meta["thread_id"])
	assert.NotEqual(t, "user-turn", meta["turn_id"], "turn_id 应被新生成的值替换")
	assert.Equal(t, "seccomp", meta["sandbox"], "sandbox 保留原样")
	assert.Equal(t, "user", meta["thread_source"], "thread_source 保留原样")
}

// --- session 模式：不同客户端得到不同 thread ---

func TestApplyCodexFingerprintHeaders_SessionMode_DifferentClients(t *testing.T) {
	account := newTestOAuthAccount(1, map[string]any{
		codexFingerprintModeExtraKey: "session",
	})

	makeTurnMeta := func() string {
		return `{"installation_id":"x","session_id":"x","thread_id":"x","turn_id":"x","window_id":"x:0"}`
	}

	clientA := http.Header{}
	clientA.Set("session-id", "client-A")
	idsA := resolveCodexFingerprintIDsFromRequest(account, clientA)
	hA := http.Header{}
	hA.Set("x-codex-turn-metadata", makeTurnMeta())
	applyCodexFingerprintHeaders(hA, idsA)

	clientB := http.Header{}
	clientB.Set("session-id", "client-B")
	idsB := resolveCodexFingerprintIDsFromRequest(account, clientB)
	hB := http.Header{}
	hB.Set("x-codex-turn-metadata", makeTurnMeta())
	applyCodexFingerprintHeaders(hB, idsB)

	assert.Equal(t, hA.Get("session-id"), hB.Get("session-id"), "session 模式每台设备只有一个长 session")
	assert.NotEqual(t, hA.Get("thread-id"), hB.Get("thread-id"), "不同客户端 thread_id 应不同")
	assert.NotEqual(t, hA.Get("x-codex-window-id"), hB.Get("x-codex-window-id"), "不同客户端 window_id 应不同")
	assert.Equal(t, hA.Get("x-codex-installation-id"), hB.Get("x-codex-installation-id"))
}

// --- full 模式 ---

func TestApplyCodexFingerprintHeaders_FullMode(t *testing.T) {
	account := newTestOAuthAccount(1, map[string]any{
		codexFingerprintModeExtraKey: "full",
	})
	seed, ok := codexFingerprintSeed(account.Extra)
	require.True(t, ok)
	convergedThread := resolveStableCodexDeviceSessionID(seed)

	clientA := http.Header{}
	clientA.Set("session-id", "client-A")
	idsA := resolveCodexFingerprintIDsFromRequest(account, clientA)
	hA := http.Header{}
	hA.Set("x-codex-turn-metadata", `{"installation_id":"x","session_id":"x","thread_id":"x","turn_id":"x","window_id":"x:0"}`)
	applyCodexFingerprintHeaders(hA, idsA)

	clientB := http.Header{}
	clientB.Set("session-id", "client-B")
	idsB := resolveCodexFingerprintIDsFromRequest(account, clientB)
	hB := http.Header{}
	hB.Set("x-codex-turn-metadata", `{"installation_id":"x","session_id":"x","thread_id":"x","turn_id":"x","window_id":"x:0"}`)
	applyCodexFingerprintHeaders(hB, idsB)

	assert.Equal(t, hA.Get("thread-id"), hB.Get("thread-id"), "full 模式 thread_id 应相同")
	assert.Equal(t, convergedThread, hA.Get("thread-id"), "full 模式应保留账号级单线程")
	assert.Equal(t, hA.Get("session-id"), hB.Get("session-id"), "full 模式每台设备只有一个长 session")
	assert.Equal(t, hA.Get("session-id"), hA.Get("thread-id"), "full 模式根 thread 必须等于 session")
	assert.Equal(t, hA.Get("x-codex-window-id"), hB.Get("x-codex-window-id"), "full 模式 window_id 应相同")
}

// --- H1 修复验证：头和体的 turn_id 一致性 ---

func TestFingerprintIDs_HeaderAndBody_TurnID_Consistent(t *testing.T) {
	account := newTestOAuthAccount(1, map[string]any{
		codexFingerprintModeExtraKey: "session",
	})
	clientHeaders := http.Header{}
	clientHeaders.Set("session-id", "client-session-xyz")

	ids := resolveCodexFingerprintIDsFromRequest(account, clientHeaders)
	require.NotNil(t, ids)
	const fixedTurnStartedAtUnixMs int64 = 1_777_777_777_777
	ids.turnStartedAtUnixMs = fixedTurnStartedAtUnixMs

	// 头改写
	h := http.Header{}
	h.Set("x-codex-turn-metadata", `{"installation_id":"x","session_id":"x","thread_id":"x","turn_id":"x","window_id":"x:0"}`)
	applyCodexFingerprintHeaders(h, ids)

	// 体改写（使用同一份 ids）
	reqBody := map[string]any{
		"client_metadata": map[string]any{
			"x-codex-installation-id": "x",
			"session_id":              "x",
			"turn_id":                 "x",
			"x-codex-turn-metadata":   `{"installation_id":"x","session_id":"x","thread_id":"x","turn_id":"x","window_id":"x:0"}`,
		},
	}
	applyCodexFingerprintClientMetadata(reqBody, ids)

	// 从头 turn-metadata JSON 提取 turn_id
	var headerMeta map[string]any
	require.NoError(t, json.Unmarshal([]byte(h.Get("x-codex-turn-metadata")), &headerMeta))
	headerTurnID, ok := headerMeta["turn_id"].(string)
	require.True(t, ok, "头 turn-metadata 应包含 string 类型的 turn_id")

	// 从体 client_metadata 提取 turn_id
	cm, ok := reqBody["client_metadata"].(map[string]any)
	require.True(t, ok, "请求体应包含 client_metadata")
	bodyTurnID, ok := cm["turn_id"].(string)
	require.True(t, ok, "体 client_metadata 应包含 string 类型的 turn_id")

	// 从体内嵌 turn-metadata JSON 提取 turn_id
	embeddedRaw, ok := cm["x-codex-turn-metadata"].(string)
	require.True(t, ok, "体 client_metadata 应包含 x-codex-turn-metadata 字符串")
	var bodyMeta map[string]any
	require.NoError(t, json.Unmarshal([]byte(embeddedRaw), &bodyMeta))
	bodyEmbeddedTurnID, ok := bodyMeta["turn_id"].(string)
	require.True(t, ok, "体内嵌 turn-metadata 应包含 string 类型的 turn_id")

	assert.Equal(t, headerTurnID, bodyTurnID, "头和体的 turn_id 必须一致")
	assert.Equal(t, headerTurnID, bodyEmbeddedTurnID, "头和体内嵌 turn-metadata 的 turn_id 必须一致")
	assert.Equal(t, ids.turnID, headerTurnID, "所有 turn_id 都应来自同一份 ids")
	assert.Equal(t, headerMeta["turn_started_at_unix_ms"], bodyMeta["turn_started_at_unix_ms"], "头和体的 timestamp 必须一致")
	assert.Equal(t, float64(fixedTurnStartedAtUnixMs), headerMeta["turn_started_at_unix_ms"])
	assert.Equal(t, float64(fixedTurnStartedAtUnixMs), bodyMeta["turn_started_at_unix_ms"])
}

func TestCodexFingerprintRootTurnLinkage_MapRawAndHeader(t *testing.T) {
	account := newTestOAuthAccount(8, map[string]any{codexFingerprintModeExtraKey: "session"})
	ids := resolveCodexFingerprintIDs(account, "client-root-linkage", codexFingerprintSession)
	require.NotNil(t, ids)

	tests := []struct {
		name             string
		originalRootTurn string
		wantRootTurn     string
	}{
		{name: "root turn follows rewritten turn", originalRootTurn: "old-turn", wantRootTurn: ids.turnID},
		{name: "child turn preserves parent root", originalRootTurn: "parent-root-turn", wantRootTurn: "parent-root-turn"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			embedded := fmt.Sprintf(`{"turn_id":"old-turn","root_turn_id":%q,"request_kind":"user"}`, tt.originalRootTurn)
			body := []byte(fmt.Sprintf(`{"client_metadata":{"turn_id":"old-turn","root_turn_id":%q,"x-codex-turn-metadata":%q}}`, tt.originalRootTurn, embedded))
			mapBody, rawBody := applyMapAndRawFingerprintBodiesForTest(t, body, ids)
			require.Equal(t, mapBody, rawBody, "map 与 raw 字节路径必须等价")

			mapClientMetadata, ok := mapBody["client_metadata"].(map[string]any)
			require.True(t, ok)
			require.Equal(t, ids.turnID, mapClientMetadata["turn_id"])
			require.Equal(t, tt.wantRootTurn, mapClientMetadata["root_turn_id"])

			bodyEmbeddedRaw, ok := mapClientMetadata["x-codex-turn-metadata"].(string)
			require.True(t, ok)
			var bodyEmbedded map[string]any
			require.NoError(t, json.Unmarshal([]byte(bodyEmbeddedRaw), &bodyEmbedded))
			require.Equal(t, ids.turnID, bodyEmbedded["turn_id"])
			require.Equal(t, tt.wantRootTurn, bodyEmbedded["root_turn_id"])

			header := make(http.Header)
			header.Set("x-codex-turn-metadata", fmt.Sprintf(`{"turn_id":"old-turn","root_turn_id":%q,"request_kind":"user"}`, tt.originalRootTurn))
			applyCodexFingerprintHeaders(header, ids)
			var headerEmbedded map[string]any
			require.NoError(t, json.Unmarshal([]byte(header.Get("x-codex-turn-metadata")), &headerEmbedded))
			require.Equal(t, ids.turnID, headerEmbedded["turn_id"])
			require.Equal(t, tt.wantRootTurn, headerEmbedded["root_turn_id"])
			require.Equal(t, bodyEmbedded["root_turn_id"], headerEmbedded["root_turn_id"])
		})
	}
}

func TestFingerprintIDs_MalformedEmbeddedMetadataRebuiltConsistently(t *testing.T) {
	account := newTestOAuthAccount(2, map[string]any{codexFingerprintModeExtraKey: "session"})
	clientHeaders := make(http.Header)
	clientHeaders.Set("session-id", "client-session-malformed")
	ids := resolveCodexFingerprintIDsFromRequest(account, clientHeaders)
	require.NotNil(t, ids)

	h := make(http.Header)
	h.Set("x-codex-turn-metadata", "{malformed")
	applyCodexFingerprintHeaders(h, ids)

	reqBody := map[string]any{
		"client_metadata": map[string]any{
			"session_id":            "client-session-malformed",
			"x-codex-turn-metadata": "[malformed",
		},
	}
	require.True(t, applyCodexFingerprintClientMetadata(reqBody, ids))

	var headerMeta map[string]any
	require.NoError(t, json.Unmarshal([]byte(h.Get("x-codex-turn-metadata")), &headerMeta))
	clientMetadata, ok := reqBody["client_metadata"].(map[string]any)
	require.True(t, ok)
	bodyRaw, ok := clientMetadata["x-codex-turn-metadata"].(string)
	require.True(t, ok)
	var bodyMeta map[string]any
	require.NoError(t, json.Unmarshal([]byte(bodyRaw), &bodyMeta))

	for _, key := range []string{"installation_id", "session_id", "thread_id", "turn_id", "window_id", "turn_started_at_unix_ms"} {
		assert.Equal(t, headerMeta[key], bodyMeta[key], "rebuilt metadata field %s must match", key)
	}
}

// --- applyCodexFingerprintClientMetadata ---

func TestApplyCodexFingerprintClientMetadata_OffMode(t *testing.T) {
	reqBody := map[string]any{
		"client_metadata": map[string]any{
			"x-codex-installation-id": "original",
		},
	}
	modified := applyCodexFingerprintClientMetadata(reqBody, nil)
	assert.False(t, modified, "nil ids 不改写")
}

func TestApplyCodexFingerprintClientMetadata_DeviceMode(t *testing.T) {
	account := newTestOAuthAccount(1, map[string]any{
		codexFingerprintModeExtraKey: "device",
		"openai_device_id":           "converged-device",
	})
	ids := resolveCodexFingerprintIDsFromRequest(account, nil)
	require.NotNil(t, ids)

	embeddedMeta := `{"installation_id":"x","session_id":"user-session","sandbox":"seccomp"}`
	reqBody := map[string]any{
		"client_metadata": map[string]any{
			"x-codex-installation-id": "original-install",
			"session_id":              "user-session",
			"x-codex-turn-metadata":   embeddedMeta,
		},
	}

	modified := applyCodexFingerprintClientMetadata(reqBody, ids)
	require.True(t, modified)

	cm, ok := reqBody["client_metadata"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "converged-device", cm["x-codex-installation-id"])
	wantSession := resolveNamespacedCodexSessionID(testCodexFingerprintSeed, "user-session")
	assert.Equal(t, wantSession, cm["session_id"])

	turnMetaStr, ok := cm["x-codex-turn-metadata"].(string)
	require.True(t, ok)
	var meta map[string]any
	require.NoError(t, json.Unmarshal([]byte(turnMetaStr), &meta))
	assert.Equal(t, "converged-device", meta["installation_id"])
	assert.Equal(t, wantSession, meta["session_id"])
	assert.Equal(t, "seccomp", meta["sandbox"], "非指纹字段保留原样")
}

func TestApplyCodexFingerprintClientMetadata_SessionMode(t *testing.T) {
	account := newTestOAuthAccount(1, map[string]any{
		codexFingerprintModeExtraKey: "session",
	})
	clientHeaders := http.Header{}
	clientHeaders.Set("session-id", "client-session-aaa")

	ids := resolveCodexFingerprintIDsFromRequest(account, clientHeaders)
	require.NotNil(t, ids)

	embeddedMeta := `{"installation_id":"x","session_id":"x","thread_id":"x","turn_id":"x","window_id":"x:0","sandbox":"seccomp"}`
	reqBody := map[string]any{
		"client_metadata": map[string]any{
			"x-codex-installation-id": "original-install",
			"session_id":              "original-session",
			"x-codex-turn-metadata":   embeddedMeta,
		},
	}

	modified := applyCodexFingerprintClientMetadata(reqBody, ids)
	require.True(t, modified)

	cm, ok := reqBody["client_metadata"].(map[string]any)
	require.True(t, ok)
	seed, ok := codexFingerprintSeed(account.Extra)
	require.True(t, ok)
	convergedInstall := resolveConvergedInstallationID(account, seed)
	convergedSession := resolveStableCodexDeviceSessionID(seed)
	convergedThread := resolveConvergedThreadID(seed, "client-session-aaa")

	assert.Equal(t, convergedInstall, cm["x-codex-installation-id"])
	assert.Equal(t, convergedSession, cm["session_id"])
	assert.Equal(t, convergedThread, cm["thread_id"])
	assert.Equal(t, convergedThread+":0", cm["x-codex-window-id"])

	turnMetaStr, ok := cm["x-codex-turn-metadata"].(string)
	require.True(t, ok)
	var meta map[string]any
	require.NoError(t, json.Unmarshal([]byte(turnMetaStr), &meta))
	assert.Equal(t, convergedInstall, meta["installation_id"])
	assert.Equal(t, convergedSession, meta["session_id"])
	assert.Equal(t, "seccomp", meta["sandbox"], "非指纹字段保留原样")
}

func TestApplyCodexFingerprintClientMetadata_FullMode(t *testing.T) {
	account := newTestOAuthAccount(1, map[string]any{
		codexFingerprintModeExtraKey: "full",
	})
	clientHeaders := http.Header{}
	clientHeaders.Set("session-id", "any-client")

	ids := resolveCodexFingerprintIDsFromRequest(account, clientHeaders)
	require.NotNil(t, ids)

	reqBody := map[string]any{
		"client_metadata": map[string]any{
			"session_id":            "x",
			"thread_id":             "x",
			"x-codex-turn-metadata": `{"installation_id":"x","session_id":"x","thread_id":"x","turn_id":"x","window_id":"x:0"}`,
		},
	}

	modified := applyCodexFingerprintClientMetadata(reqBody, ids)
	require.True(t, modified)

	cm, ok := reqBody["client_metadata"].(map[string]any)
	require.True(t, ok)
	seed, ok := codexFingerprintSeed(account.Extra)
	require.True(t, ok)
	convergedSession := resolveStableCodexDeviceSessionID(seed)
	convergedThread := resolveStableCodexDeviceSessionID(seed)

	assert.Equal(t, convergedSession, cm["session_id"])
	assert.Equal(t, convergedThread, cm["thread_id"], "full 模式应保留账号级单线程")
	assert.Equal(t, cm["session_id"], cm["thread_id"])
}

// --- extractClientSessionID ---

func TestExtractClientSessionID(t *testing.T) {
	tests := []struct {
		name     string
		headers  http.Header
		expected string
	}{
		{"连字符形式优先", func() http.Header {
			h := http.Header{}
			h.Set("session-id", "hyphen-form")
			h.Set("session_id", "underscore-form")
			return h
		}(), "hyphen-form"},
		{"回退到下划线形式", func() http.Header {
			h := http.Header{}
			h.Set("session_id", "underscore-form")
			return h
		}(), "underscore-form"},
		{"都没有", http.Header{}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, extractClientSessionID(tt.headers))
		})
	}
}

// --- 透传路径：raw 字节版 client_metadata 改写 ---

// rawVsMapClientMetadata 用同一份 ids 分别跑 map 版与 raw 字节版，
// 返回两侧最终的 client_metadata 解码结果。
func rawVsMapClientMetadata(t *testing.T, body []byte, ids *codexFingerprintIDs) (map[string]any, map[string]any) {
	t.Helper()

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(body, &decoded))
	applyCodexFingerprintClientMetadata(decoded, ids)
	mapCM, _ := decoded["client_metadata"].(map[string]any)

	rawBody, changed, err := applyCodexFingerprintClientMetadataRaw(body, ids)
	require.NoError(t, err)
	require.True(t, changed)
	var rawDecoded map[string]any
	require.NoError(t, json.Unmarshal(rawBody, &rawDecoded))
	rawCM, _ := rawDecoded["client_metadata"].(map[string]any)
	return mapCM, rawCM
}

func cloneCodexFingerprintIDsForTest(ids *codexFingerprintIDs) *codexFingerprintIDs {
	if ids == nil {
		return nil
	}
	cloned := *ids
	cloned.originalBodySessionID = ""
	cloned.originalBodySessionIDCaptured = false
	return &cloned
}

func applyMapAndRawFingerprintBodiesForTest(t *testing.T, body []byte, ids *codexFingerprintIDs) (map[string]any, map[string]any) {
	t.Helper()

	mapIDs := cloneCodexFingerprintIDsForTest(ids)
	rawIDs := cloneCodexFingerprintIDsForTest(ids)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(body, &decoded))
	applyCodexFingerprintClientMetadata(decoded, mapIDs)

	rawBody, _, err := applyCodexFingerprintClientMetadataRaw(body, rawIDs)
	require.NoError(t, err)
	var rawDecoded map[string]any
	require.NoError(t, json.Unmarshal(rawBody, &rawDecoded))
	return decoded, rawDecoded
}

func TestApplyCodexFingerprintPromptCacheKey_MapRawEquivalence(t *testing.T) {
	for _, mode := range []codexFingerprintMode{codexFingerprintSession, codexFingerprintFull} {
		t.Run(string(mode)+"/default", func(t *testing.T) {
			account := newTestOAuthAccount(4300, map[string]any{codexFingerprintModeExtraKey: string(mode)})
			ids := resolveCodexFingerprintIDs(account, "header-session", mode)
			require.NotNil(t, ids)

			body := []byte(`{"model":"gpt-5.6-sol","prompt_cache_key":"body-session","client_metadata":{"session_id":" body-session ","trace":"keep"},"input":[]}`)
			mapBody, rawBody := applyMapAndRawFingerprintBodiesForTest(t, body, ids)

			require.Equal(t, mapBody["prompt_cache_key"], rawBody["prompt_cache_key"])
			require.Equal(t, ids.sessionID, mapBody["prompt_cache_key"])
			mapCM, _ := mapBody["client_metadata"].(map[string]any)
			rawCM, _ := rawBody["client_metadata"].(map[string]any)
			require.Equal(t, ids.sessionID, mapCM["session_id"])
			require.Equal(t, mapCM["session_id"], rawCM["session_id"])
			require.Equal(t, "keep", rawCM["trace"])
		})
	}

	t.Run("explicit override", func(t *testing.T) {
		account := newTestOAuthAccount(4301, map[string]any{codexFingerprintModeExtraKey: "session"})
		ids := resolveCodexFingerprintIDs(account, "header-session", codexFingerprintSession)
		require.NotNil(t, ids)

		body := []byte(`{"model":"gpt-5.6-sol","prompt_cache_key":"explicit-cache","client_metadata":{"session_id":"body-session"},"input":[]}`)
		mapBody, rawBody := applyMapAndRawFingerprintBodiesForTest(t, body, ids)

		require.Equal(t, "explicit-cache", mapBody["prompt_cache_key"])
		require.Equal(t, "explicit-cache", rawBody["prompt_cache_key"])
		mapCM, _ := mapBody["client_metadata"].(map[string]any)
		rawCM, _ := rawBody["client_metadata"].(map[string]any)
		require.Equal(t, ids.sessionID, mapCM["session_id"])
		require.Equal(t, ids.sessionID, rawCM["session_id"])
	})
}

func TestApplyCodexFingerprintPromptCacheKey_Negatives(t *testing.T) {
	sessionAccount := newTestOAuthAccount(4310, map[string]any{codexFingerprintModeExtraKey: "session"})
	sessionIDs := resolveCodexFingerprintIDs(sessionAccount, "header-session", codexFingerprintSession)
	require.NotNil(t, sessionIDs)
	deviceAccount := newTestOAuthAccount(4311, map[string]any{codexFingerprintModeExtraKey: "device"})
	deviceIDs := resolveCodexFingerprintIDs(deviceAccount, "header-session", codexFingerprintDevice)
	require.NotNil(t, deviceIDs)

	tests := []struct {
		name          string
		body          []byte
		ids           *codexFingerprintIDs
		wantExists    bool
		wantCacheKey  any
		wantRawString string
	}{
		{
			name:       "missing key is not injected",
			body:       []byte(`{"client_metadata":{"session_id":"body-session"}}`),
			ids:        sessionIDs,
			wantExists: false,
		},
		{
			name:         "empty key preserved",
			body:         []byte(`{"prompt_cache_key":"","client_metadata":{"session_id":"body-session"}}`),
			ids:          sessionIDs,
			wantExists:   true,
			wantCacheKey: "",
		},
		{
			name:         "whitespace-different key is an explicit override",
			body:         []byte(`{"prompt_cache_key":" body-session ","client_metadata":{"session_id":"body-session"}}`),
			ids:          sessionIDs,
			wantExists:   true,
			wantCacheKey: " body-session ",
		},
		{
			name:         "non-string key preserved",
			body:         []byte(`{"prompt_cache_key":123,"client_metadata":{"session_id":"body-session"}}`),
			ids:          sessionIDs,
			wantExists:   true,
			wantCacheKey: float64(123),
		},
		{
			name:         "missing source metadata preserves key",
			body:         []byte(`{"prompt_cache_key":"body-session"}`),
			ids:          sessionIDs,
			wantExists:   true,
			wantCacheKey: "body-session",
		},
		{
			name:         "non-string source session preserves key",
			body:         []byte(`{"prompt_cache_key":"123","client_metadata":{"session_id":123}}`),
			ids:          sessionIDs,
			wantExists:   true,
			wantCacheKey: "123",
		},
		{
			name:         "non-object source metadata preserves key",
			body:         []byte(`{"prompt_cache_key":"body-session","client_metadata":"bad"}`),
			ids:          sessionIDs,
			wantExists:   true,
			wantCacheKey: "body-session",
		},
		{
			name:         "device mode preserves key",
			body:         []byte(`{"prompt_cache_key":"body-session","client_metadata":{"session_id":"body-session"}}`),
			ids:          deviceIDs,
			wantExists:   true,
			wantCacheKey: "body-session",
		},
		{
			name:          "off mode preserves body",
			body:          []byte(`{"prompt_cache_key":"body-session","client_metadata":{"session_id":"body-session"}}`),
			ids:           nil,
			wantExists:    true,
			wantCacheKey:  "body-session",
			wantRawString: `{"prompt_cache_key":"body-session","client_metadata":{"session_id":"body-session"}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var mapBody map[string]any
			require.NoError(t, json.Unmarshal(tt.body, &mapBody))
			changedMap := applyCodexFingerprintClientMetadata(mapBody, cloneCodexFingerprintIDsForTest(tt.ids))

			rawBody, changedRaw, err := applyCodexFingerprintClientMetadataRaw(tt.body, cloneCodexFingerprintIDsForTest(tt.ids))
			require.NoError(t, err)
			if tt.ids == nil {
				require.False(t, changedMap)
				require.False(t, changedRaw)
				require.JSONEq(t, tt.wantRawString, string(rawBody))
				return
			}
			require.True(t, changedMap)
			require.True(t, changedRaw)

			rawDecoded := map[string]any{}
			require.NoError(t, json.Unmarshal(rawBody, &rawDecoded))
			_, mapExists := mapBody["prompt_cache_key"]
			_, rawExists := rawDecoded["prompt_cache_key"]
			require.Equal(t, tt.wantExists, mapExists)
			require.Equal(t, tt.wantExists, rawExists)
			if tt.wantExists {
				require.Equal(t, tt.wantCacheKey, mapBody["prompt_cache_key"])
				require.Equal(t, tt.wantCacheKey, rawDecoded["prompt_cache_key"])
			}
		})
	}
}

func TestApplyCodexFingerprintClientMetadataRaw_MatchesMapVariant(t *testing.T) {
	const fixedTurnStartedAtUnixMs int64 = 1_777_777_777_777
	embedded := `{\"installation_id\":\"real-install\",\"session_id\":\"real-session\",\"sandbox\":\"seatbelt\"}`
	bodies := map[string]string{
		"no_client_metadata": `{"model":"gpt-5.6-sol","input":[],"stream":true}`,
		"object_with_extras": `{"model":"gpt-5.6-sol","client_metadata":{"session_id":"client-session","traceparent":"00-abc-def-01","x-codex-turn-metadata":"` + embedded + `"},"stream":true}`,
		"non_object_value":   `{"model":"gpt-5.6-sol","client_metadata":"bogus","stream":true}`,
	}
	for _, mode := range []codexFingerprintMode{codexFingerprintDevice, codexFingerprintSession, codexFingerprintFull} {
		account := newTestOAuthAccount(4242, map[string]any{codexFingerprintModeExtraKey: string(mode)})
		ids := resolveCodexFingerprintIDs(account, "client-sess-raw", mode)
		require.NotNil(t, ids)
		ids.turnStartedAtUnixMs = fixedTurnStartedAtUnixMs
		for name, body := range bodies {
			t.Run(string(mode)+"/"+name, func(t *testing.T) {
				mapCM, rawCM := rawVsMapClientMetadata(t, []byte(body), ids)
				assert.Equal(t, mapCM, rawCM, "raw 字节版与 map 版的 client_metadata 结果必须逐点一致")
				if mode == codexFingerprintDevice || name != "object_with_extras" {
					return
				}
				for _, clientMetadata := range []map[string]any{mapCM, rawCM} {
					rawMetadata, ok := clientMetadata["x-codex-turn-metadata"].(string)
					require.True(t, ok)
					var metadata map[string]any
					require.NoError(t, json.Unmarshal([]byte(rawMetadata), &metadata))
					require.Equal(t, float64(fixedTurnStartedAtUnixMs), metadata["turn_started_at_unix_ms"])
				}
			})
		}
	}
}

func TestApplyCodexFingerprintClientMetadataRaw_PreservesUnrelatedFields(t *testing.T) {
	account := newTestOAuthAccount(4243, map[string]any{codexFingerprintModeExtraKey: "session"})
	ids := resolveCodexFingerprintIDs(account, "client-sess-preserve", codexFingerprintSession)
	require.NotNil(t, ids)

	body := []byte(`{"model":"gpt-5.6-sol","input":[{"type":"message","role":"user","content":"hi"}],"stream":true,"prompt_cache_key":"pck-1"}`)
	out, changed, err := applyCodexFingerprintClientMetadataRaw(body, ids)
	require.NoError(t, err)
	require.True(t, changed)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(out, &decoded))
	assert.Equal(t, "gpt-5.6-sol", decoded["model"])
	assert.Equal(t, "pck-1", decoded["prompt_cache_key"])
	assert.Equal(t, true, decoded["stream"])
	cm, _ := decoded["client_metadata"].(map[string]any)
	require.NotNil(t, cm)
	assert.Equal(t, ids.sessionID, cm["session_id"])
	assert.Equal(t, ids.turnID, cm["turn_id"])
}

func TestApplyCodexFingerprintClientMetadataRaw_Noop(t *testing.T) {
	body := []byte(`{"model":"gpt-5.6-sol"}`)
	out, changed, err := applyCodexFingerprintClientMetadataRaw(body, nil)
	require.NoError(t, err)
	assert.False(t, changed)
	assert.Equal(t, body, out)

	out, changed, err = applyCodexFingerprintClientMetadataRaw(nil, &codexFingerprintIDs{mode: codexFingerprintSession, installationID: "x"})
	require.NoError(t, err)
	assert.False(t, changed)
	assert.Nil(t, out)
}

// --- context 暂存与出站头应用（透传/非透传共用 seam）---

func newFingerprintStageTestContext(t *testing.T) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	return c
}

func TestStageCodexFingerprintIDs_NilOverwritesPreviousAccount(t *testing.T) {
	c := newFingerprintStageTestContext(t)
	accountA := newTestOAuthAccount(1001, map[string]any{codexFingerprintModeExtraKey: "session"})
	idsA := resolveCodexFingerprintIDs(accountA, "sess-x", codexFingerprintSession)
	require.NotNil(t, idsA)
	stageCodexFingerprintIDs(c, idsA)

	// failover 切到 off 模式账号：无条件覆写为 nil，上一账号 IDs 不得残留
	stageCodexFingerprintIDs(c, nil)

	h := http.Header{}
	h.Set("session_id", "isolated-session")
	accountB := newTestOAuthAccount(1002, map[string]any{"codex_fingerprint_mode": "off"})
	applyStagedCodexFingerprintHeaders(c, accountB, h)
	assert.Equal(t, "isolated-session", h.Get("session_id"), "off 账号不得应用上一账号的收敛 ID")
	assert.Empty(t, h.Get("x-codex-installation-id"))
}

func TestApplyStagedCodexFingerprintRejectsDifferentOAuthAccount(t *testing.T) {
	c := newFingerprintStageTestContext(t)
	accountA := newTestOAuthAccount(1003, map[string]any{codexFingerprintModeExtraKey: "session"})
	idsA := resolveCodexFingerprintIDs(accountA, "sess-a", codexFingerprintSession)
	require.NotNil(t, idsA)
	stageCodexFingerprintIDs(c, idsA)

	accountB := newTestOAuthAccount(1004, map[string]any{codexFingerprintModeExtraKey: "session"})
	h := make(http.Header)
	h.Set("session-id", "account-b-session")
	applyStagedCodexFingerprintHeaders(c, accountB, h)
	assert.Equal(t, "account-b-session", h.Get("session-id"))
	assert.Empty(t, h.Get("x-codex-installation-id"))

	body := map[string]any{"client_metadata": map[string]any{"session_id": "account-b-session"}}
	assert.False(t, applyStagedCodexFingerprintClientMetadata(c, accountB, body))
	clientMetadata, ok := body["client_metadata"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "account-b-session", clientMetadata["session_id"])
}

func TestApplyStagedCodexFingerprintHeaders_RejectsStaleIDsFromDifferentAccountType(t *testing.T) {
	c := newFingerprintStageTestContext(t)
	oauthIDs := resolveCodexFingerprintIDs(newTestOAuthAccount(1003, map[string]any{codexFingerprintModeExtraKey: "session"}), "sess-y", codexFingerprintSession)
	require.NotNil(t, oauthIDs)
	stageCodexFingerprintIDs(c, oauthIDs)

	h := http.Header{}
	apiKeyAccount := &Account{ID: 1004, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	applyStagedCodexFingerprintHeaders(c, apiKeyAccount, h)
	assert.Empty(t, h.Get("x-codex-installation-id"), "stale 收敛 ID 不得应用到不匹配的账号类型")
}

func TestApplyStagedCodexFingerprintHeaders_AppliesOwnAPIKeyIDs(t *testing.T) {
	c := newFingerprintStageTestContext(t)
	account := newTestAPIKeyAccount(1005, map[string]any{codexFingerprintModeExtraKey: "session"})
	ids := resolveCodexFingerprintIDs(account, "api-key-session", codexFingerprintSession)
	require.NotNil(t, ids)
	stageCodexFingerprintIDs(c, ids)

	h := http.Header{"session-id": []string{"api-key-session"}}
	applyStagedCodexFingerprintHeaders(c, account, h)

	require.Equal(t, ids.installationID, h.Get("x-codex-installation-id"))
	require.Equal(t, ids.sessionID, h.Get("session-id"))
}

func TestBuildUpstreamRequestOpenAIPassthrough_AppliesStagedFingerprint(t *testing.T) {
	svc := &OpenAIGatewayService{}
	// 收敛是显式 opt-in（#5610）：显式开启后验证透传路径的出站头收敛。
	account := newTestOAuthAccount(2001, map[string]any{
		"openai_oauth_passthrough": true,
		"codex_fingerprint_mode":   "session",
	})

	c := newFingerprintStageTestContext(t)
	c.Request.Header.Set("session_id", "real-client-session")
	c.Request.Header.Set("User-Agent", "codex_cli_rs/0.144.1 (Ubuntu 22.4.0; x86_64) xterm-256color")
	c.Request.Header.Set("originator", "codex_cli_rs")
	c.Request.Header.Set("x-codex-turn-metadata", `{"installation_id":"real-install","session_id":"real-session","sandbox":"seatbelt"}`)

	// 复刻 forwardOpenAIPassthrough 的解析+暂存 seam（默认 session 模式）
	ids, err := svc.resolveCodexFingerprintIDsForOutbound(c, account, c.Request.Header, true)
	require.NoError(t, err)
	require.NotNil(t, ids)
	stageCodexFingerprintIDs(c, ids)

	body := []byte(`{"model":"gpt-5.6-sol","input":[],"stream":true}`)
	req, err := svc.buildUpstreamRequestOpenAIPassthrough(context.Background(), c, account, body, "test-token")
	require.NoError(t, err)

	assert.Equal(t, codexCLIUserAgent, req.Header.Get("user-agent"))
	assert.Equal(t, ids.sessionID, req.Header.Get("session_id"), "session 模式下出站 session_id 应为账号级收敛值")
	assert.Equal(t, ids.installationID, req.Header.Get("x-codex-installation-id"))
	assert.Equal(t, ids.windowID, req.Header.Get("x-codex-window-id"))
	assert.Equal(t, ids.threadID, req.Header.Get("x-client-request-id"))
	turnMetadata := req.Header.Get("x-codex-turn-metadata")
	require.NotEmpty(t, turnMetadata)
	assert.Contains(t, turnMetadata, ids.sessionID, "turn-metadata JSON 中的 session_id 应被收敛")
	assert.Contains(t, turnMetadata, `"sandbox":"seccomp"`, "turn-metadata sandbox 应与最终 Ubuntu UA 一致")
}

func TestBuildUpstreamRequestOpenAIPassthrough_AppliesStableDevicePersona(t *testing.T) {
	repo := &codexPersonaFirstWriterRepo{}
	settingService := NewSettingService(&codexVersionSettingRepoStub{values: map[string]string{
		SettingKeyOpenAICodexDevicePoolSize:   "8",
		SettingKeyOpenAICodexUAPersonaEnabled: "true",
	}}, &config.Config{})
	svc := &OpenAIGatewayService{accountRepo: repo, cfg: &config.Config{}, settingService: settingService}
	account := newTestOAuthAccount(2010, map[string]any{
		"openai_oauth_passthrough":   true,
		codexFingerprintModeExtraKey: "session",
	})

	c := newFingerprintStageTestContext(t)
	c.Request = c.Request.WithContext(WithSub2APIUserID(c.Request.Context(), 99))
	c.Request.Header.Set("session_id", "real-client-session")
	c.Request.Header.Set("originator", "codex_cli_rs")
	c.Request.Header.Set("x-codex-turn-metadata", `{"sandbox":"seatbelt"}`)

	ids, err := svc.resolveCodexFingerprintIDsForOutbound(c, account, c.Request.Header, true)
	require.NoError(t, err)
	require.NotNil(t, ids)
	require.NotEmpty(t, ids.userAgent)
	require.Contains(t, []string{"seatbelt", "seccomp", "none"}, ids.sandbox)
	stageCodexFingerprintIDs(c, ids)

	req, err := svc.buildUpstreamRequestOpenAIPassthrough(
		context.Background(), c, account,
		[]byte(`{"model":"gpt-5.6-sol","input":[],"stream":true}`), "test-token",
	)
	require.NoError(t, err)
	require.Equal(t, ids.userAgent, req.Header.Get("user-agent"))
	require.Contains(t, req.Header.Get("x-codex-turn-metadata"), `"sandbox":"`+ids.sandbox+`"`)
}

func TestBuildOpenAIWSHeadersUsesSameUserAgentDecisionAsFingerprintResolution(t *testing.T) {
	SetCodexIdentityEnforcementEnabled(false)
	t.Cleanup(func() { SetCodexIdentityEnforcementEnabled(true) })

	svc := &OpenAIGatewayService{cfg: &config.Config{}}
	account := newTestOAuthAccount(2015, map[string]any{codexFingerprintModeExtraKey: "session"})
	account.Credentials = map[string]any{
		"access_token":       "test-token",
		"chatgpt_account_id": "chatgpt-account",
	}
	c := newFingerprintStageTestContext(t)
	c.Request.Header.Set("User-Agent", "codex-tui/0.146.0 (Ubuntu 22.4.0; x86_64) xterm (codex-tui; 0.146.0)")
	c.Request.Header.Set("originator", "codex-tui")

	ids := resolveCodexFingerprintIDs(account, "real-client-session", codexFingerprintSession)
	require.NotNil(t, ids)
	ids.userAgent = buildCodexPersonaUserAgent(codexUAPersonaWindows, CodexCanonicalClientVersion())
	ids.sandbox = "none"
	stageCodexFingerprintIDs(c, ids)

	headers, _, err := svc.buildOpenAIWSHeaders(
		context.Background(), c, account, "test-token",
		OpenAIWSProtocolDecision{Transport: OpenAIUpstreamTransportResponsesWebsocket},
		true, "", "", "", "gpt-5.2", "",
	)
	require.NoError(t, err)
	require.Equal(t, ids.userAgent, headers.Get("User-Agent"),
		"fingerprint parsing and the actual WS handshake must share one UA decision")
}

func TestResolveCodexFingerprintIDsForOutboundPersistsObservedPlatformSlot(t *testing.T) {
	repo := &codexPersonaFirstWriterRepo{}
	settingService := NewSettingService(&codexVersionSettingRepoStub{values: map[string]string{
		SettingKeyOpenAICodexDevicePoolSize:          "4",
		SettingKeyOpenAICodexDevicePoolPlatformRatio: "1:1:2",
		SettingKeyOpenAICodexUAPersonaEnabled:        "true",
	}}, &config.Config{})
	svc := &OpenAIGatewayService{accountRepo: repo, cfg: &config.Config{}, settingService: settingService}
	account := newTestOAuthAccount(2011, map[string]any{codexFingerprintModeExtraKey: "session"})
	c := newFingerprintStageTestContext(t)
	c.Request = c.Request.WithContext(WithSub2APIUserID(c.Request.Context(), 99))
	c.Request.Header.Set("session-id", "real-client-session")
	c.Request.Header.Set("user-agent", "codex-tui/0.146.0 (Windows 11.0.0; x86_64) WindowsTerminal (codex-tui; 0.146.0)")
	c.Request.Header.Set("x-codex-turn-metadata", `{"sandbox":"none"}`)

	first, err := svc.resolveCodexFingerprintIDsForOutbound(c, account, c.Request.Header, true)
	require.NoError(t, err)
	require.NotNil(t, first)
	require.Equal(t, 1, first.deviceSlot)
	require.Equal(t, resolveConvergedInstallationIDForSlot(account, testCodexFingerprintSeed, 1), first.installationID)
	require.Contains(t, first.userAgent, "(Windows 11.0.0; x86_64)")
	require.Equal(t, "none", first.sandbox)
	require.Equal(t, first.sessionID, first.threadID, "the first real session bound to a device is its root thread")
	require.Equal(t, 1, repo.casN)
	persisted, ok := repo.casCurrent.(codexDevicePoolState)
	require.True(t, ok)
	require.Equal(t, "real-client-session", persisted.Slots[0].RootSession)
	require.NotContains(t, account.Extra, codexDevicePoolExtraKey)

	second, err := svc.resolveCodexFingerprintIDsForOutbound(c, account, c.Request.Header, true)
	require.NoError(t, err)
	require.NotNil(t, second)
	require.Equal(t, first.deviceSlot, second.deviceSlot)
	require.Equal(t, first.installationID, second.installationID)
	require.Equal(t, 2, repo.casN, "stale scheduler snapshots converge through the persisted CAS winner")
}

func TestResolveCodexFingerprintIDsForOutboundFirstEnablementRotatesEveryIdentity(t *testing.T) {
	const users = 10000
	state := codexDevicePoolState{
		Version:  1,
		NextSlot: 4,
		Slots: []codexDeviceSlot{
			{ID: 1, Platform: codexUAPersonaMac, Sandbox: "seatbelt", CreatedFor: "101"},
			{ID: 2, Platform: codexUAPersonaWindows, Sandbox: "none", CreatedFor: "202"},
			{ID: 3, Platform: codexUAPersonaUbuntu, Sandbox: "seccomp", CreatedFor: "303"},
		},
	}
	account := newTestOAuthAccount(2012, map[string]any{
		codexFingerprintModeExtraKey: "device",
		codexDevicePoolExtraKey:      state,
	})
	poolOne := &OpenAIGatewayService{
		cfg: &config.Config{},
		settingService: NewSettingService(&codexVersionSettingRepoStub{values: map[string]string{
			SettingKeyOpenAICodexDevicePoolSize: "1",
		}}, &config.Config{}),
	}
	poolThree := &OpenAIGatewayService{
		accountRepo: &codexPersonaFirstWriterRepo{casCurrent: state},
		cfg:         &config.Config{},
		settingService: NewSettingService(&codexVersionSettingRepoStub{values: map[string]string{
			SettingKeyOpenAICodexDevicePoolSize: "3",
		}}, &config.Config{}),
	}
	c := newFingerprintStageTestContext(t)
	moved := 0
	for userID := int64(1); userID <= users; userID++ {
		c.Request = c.Request.WithContext(WithSub2APIUserID(c.Request.Context(), userID))
		before, err := poolOne.resolveCodexFingerprintIDsForOutbound(c, account, nil, false)
		require.NoError(t, err)
		require.NotNil(t, before)
		after, err := poolThree.resolveCodexFingerprintIDsForOutbound(c, account, nil, false)
		require.NoError(t, err)
		require.NotNil(t, after)
		if before.installationID != after.installationID {
			moved++
		}
	}
	require.Equal(t, users, moved, "persisted pool slots must never reuse the disabled pool's slot-0 identity")
}

func TestResolveCodexFingerprintIDsForDeviceSlotRootSessionIntroductionMayRotateThreadOnce(t *testing.T) {
	account := newTestOAuthAccount(2014, map[string]any{codexFingerprintModeExtraKey: "session"})
	before := resolveCodexFingerprintIDsForDeviceSlotWithRoot(account, "first-client-session", codexFingerprintSession, 1, "")
	after := resolveCodexFingerprintIDsForDeviceSlotWithRoot(account, "first-client-session", codexFingerprintSession, 1, "first-client-session")

	require.NotNil(t, before)
	require.NotNil(t, after)
	require.Equal(t, before.installationID, after.installationID)
	require.Equal(t, before.sessionID, after.sessionID)
	require.NotEqual(t, before.threadID, after.threadID)
	require.Equal(t, after.sessionID, after.threadID, "the persisted root session becomes the device root thread")
}

func TestResolveCodexFingerprintIDsForOutboundUsesFrozenPersonaFromAccountExtra(t *testing.T) {
	settingService := NewSettingService(&codexVersionSettingRepoStub{values: map[string]string{
		SettingKeyOpenAICodexUAPersonaEnabled: "true",
	}}, &config.Config{})
	svc := &OpenAIGatewayService{cfg: &config.Config{}, settingService: settingService}
	account := newTestOAuthAccount(2020, map[string]any{
		codexFingerprintModeExtraKey: "session",
		"codex_ua_persona": map[string]any{
			"platform": "mac",
			"sandbox":  "seatbelt",
		},
	})
	c := newFingerprintStageTestContext(t)
	c.Request.Header.Set("session_id", "real-client-session")
	c.Request.Header.Set("user-agent", "codex-tui/0.146.0 (Ubuntu 22.4.0; x86_64) xterm-256color (codex-tui; 0.146.0)")
	c.Request.Header.Set("x-codex-turn-metadata", `{"sandbox":"seccomp"}`)

	ids, err := svc.resolveCodexFingerprintIDsForOutbound(c, account, c.Request.Header, true)
	require.NoError(t, err)

	require.NotNil(t, ids)
	require.Contains(t, ids.userAgent, "(Mac OS X 15.6.1; arm64)")
	require.Equal(t, "seatbelt", ids.sandbox)
}

func TestResolveCodexFingerprintIDsForOutboundFreezesFirstObservedPersonaAndUsesWinner(t *testing.T) {
	repo := &codexPersonaFirstWriterRepo{winner: map[string]any{
		"platform": "windows",
		"sandbox":  "none",
	}}
	settingService := NewSettingService(&codexVersionSettingRepoStub{values: map[string]string{
		SettingKeyOpenAICodexUAPersonaEnabled: "true",
	}}, &config.Config{})
	svc := &OpenAIGatewayService{accountRepo: repo, cfg: &config.Config{}, settingService: settingService}
	account := newTestOAuthAccount(2021, map[string]any{codexFingerprintModeExtraKey: "session"})
	c := newFingerprintStageTestContext(t)
	c.Request.Header.Set("session_id", "real-client-session")
	c.Request.Header.Set("user-agent", "codex-tui/0.146.0 (Mac OS X 15.6.1; arm64) Apple_Terminal (codex-tui; 0.146.0)")
	c.Request.Header.Set("x-codex-turn-metadata", `{"sandbox":"seatbelt"}`)

	ids, err := svc.resolveCodexFingerprintIDsForOutbound(c, account, c.Request.Header, true)
	require.NoError(t, err)

	require.NotNil(t, ids)
	require.Equal(t, 1, repo.firstWriteN)
	require.Equal(t, "codex_ua_persona", repo.firstWriteKey)
	require.Equal(t, map[string]any{"platform": "mac", "sandbox": "seatbelt"}, repo.candidate)
	require.Contains(t, ids.userAgent, "(Windows 11.0.0; x86_64)")
	require.Equal(t, "none", ids.sandbox)
	require.NotContains(t, account.Extra, "codex_ua_persona", "request resolution must not mutate the scheduler account snapshot")
}

func TestResolveCodexFingerprintIDsForOutboundFallsBackToWeightedPersonaAndFreezesIt(t *testing.T) {
	repo := &codexPersonaFirstWriterRepo{}
	settingService := NewSettingService(&codexVersionSettingRepoStub{values: map[string]string{
		SettingKeyOpenAICodexUAPersonaEnabled: "true",
	}}, &config.Config{})
	svc := &OpenAIGatewayService{accountRepo: repo, cfg: &config.Config{}, settingService: settingService}
	account := newTestOAuthAccount(2022, map[string]any{codexFingerprintModeExtraKey: "session"})
	c := newFingerprintStageTestContext(t)
	c.Request.Header.Set("session_id", "real-client-session")
	c.Request.Header.Set("user-agent", "unknown-client/1.0")
	c.Request.Header.Set("x-codex-turn-metadata", `{"sandbox":"mystery","sandbox_mode":"workspace-write"}`)

	ids, err := svc.resolveCodexFingerprintIDsForOutbound(c, account, c.Request.Header, true)
	require.NoError(t, err)

	require.NotNil(t, ids)
	require.Equal(t, 1, repo.firstWriteN)
	require.Equal(t, map[string]any{"platform": "ubuntu", "sandbox": "seccomp"}, repo.candidate)
	require.Contains(t, ids.userAgent, "(Ubuntu 22.4.0; x86_64)")
	require.Equal(t, "seccomp", ids.sandbox)
}

func TestApplyCodexFingerprintHeadersRewritesSandboxWithoutChangingSandboxMode(t *testing.T) {
	account := newTestOAuthAccount(2023, map[string]any{codexFingerprintModeExtraKey: "session"})
	ids := resolveCodexFingerprintIDs(account, "real-client-session", codexFingerprintSession)
	require.NotNil(t, ids)
	ids.sandbox = "seatbelt"
	h := make(http.Header)
	h.Set("x-codex-turn-metadata", `{"sandbox":"seccomp","sandbox_mode":"workspace-write"}`)

	applyCodexFingerprintHeaders(h, ids)

	var metadata map[string]any
	require.NoError(t, json.Unmarshal([]byte(h.Get("x-codex-turn-metadata")), &metadata))
	require.Equal(t, "seatbelt", metadata["sandbox"])
	require.Equal(t, "workspace-write", metadata["sandbox_mode"])

	body := map[string]any{"client_metadata": map[string]any{
		"x-codex-turn-metadata": `{"sandbox":"seccomp","sandbox_mode":"danger-full-access"}`,
	}}
	require.True(t, applyCodexFingerprintClientMetadata(body, ids))
	clientMetadata, ok := body["client_metadata"].(map[string]any)
	require.True(t, ok)
	turnMetadata, ok := clientMetadata["x-codex-turn-metadata"].(string)
	require.True(t, ok)
	require.NoError(t, json.Unmarshal([]byte(turnMetadata), &metadata))
	require.Equal(t, "seatbelt", metadata["sandbox"])
	require.Equal(t, "danger-full-access", metadata["sandbox_mode"])
}

func TestApplyCodexPersonaInOffModeRewritesNamespacedSessionAndSandbox(t *testing.T) {
	settingService := NewSettingService(&codexVersionSettingRepoStub{values: map[string]string{
		SettingKeyOpenAICodexUAPersonaEnabled: "true",
	}}, &config.Config{})
	svc := &OpenAIGatewayService{cfg: &config.Config{}, settingService: settingService}
	account := newTestOAuthAccount(2024, map[string]any{
		codexFingerprintModeExtraKey: "off",
		codexFingerprintSeedExtraKey: testCodexFingerprintSeed,
		codexUAPersonaExtraKey: map[string]any{
			"platform": "windows",
			"sandbox":  "none",
		},
	})
	c := newFingerprintStageTestContext(t)
	c.Request.Header.Set("session-id", "hyphen-session")
	c.Request.Header.Set("session_id", "underscore-session")
	c.Request.Header.Set("x-codex-turn-metadata", `{"installation_id":"client-install","sandbox":"seccomp","sandbox_mode":"workspace-write"}`)

	ids, err := svc.resolveCodexFingerprintIDsForOutbound(c, account, c.Request.Header, true)
	require.NoError(t, err)
	require.NotNil(t, ids)
	require.Equal(t, codexFingerprintOff, ids.mode)
	applyCodexFingerprintHeaders(c.Request.Header, ids)

	wantSession := resolveNamespacedCodexSessionID(testCodexFingerprintSeed, "hyphen-session")
	require.Equal(t, wantSession, c.Request.Header.Get("session-id"))
	require.Equal(t, wantSession, c.Request.Header.Get("session_id"))
	require.Empty(t, c.Request.Header.Get("x-codex-installation-id"))
	var metadata map[string]any
	require.NoError(t, json.Unmarshal([]byte(c.Request.Header.Get("x-codex-turn-metadata")), &metadata))
	require.Equal(t, "client-install", metadata["installation_id"])
	require.Equal(t, "none", metadata["sandbox"])
	require.Equal(t, "workspace-write", metadata["sandbox_mode"])
}

func TestBuildUpstreamRequestOpenAIPassthrough_OffModeNamespacesSession(t *testing.T) {
	svc := &OpenAIGatewayService{}
	account := newTestOAuthAccount(2002, map[string]any{
		"openai_oauth_passthrough": true,
		"codex_fingerprint_mode":   "off",
	})

	c := newFingerprintStageTestContext(t)
	c.Request.Header.Set("session_id", "real-client-session")
	c.Request.Header.Set("originator", "codex_cli_rs")

	ids := resolveCodexFingerprintIDsFromRequest(account, c.Request.Header)
	require.NotNil(t, ids)
	stageCodexFingerprintIDs(c, ids)

	body := []byte(`{"model":"gpt-5.6-sol","input":[],"stream":true}`)
	req, err := svc.buildUpstreamRequestOpenAIPassthrough(context.Background(), c, account, body, "test-token")
	require.NoError(t, err)

	wantSession := resolveNamespacedCodexSessionID(testCodexFingerprintSeed, "real-client-session")
	assert.Equal(t, wantSession, req.Header.Get("session-id"))
	assert.Equal(t, wantSession, req.Header.Get("session_id"))
	assert.Empty(t, req.Header.Get("x-codex-window-id"))
}

func TestResolveCodexFingerprintIDsForOutboundPoolBindingGate(t *testing.T) {
	for _, tt := range []struct {
		name           string
		personaEnabled string
		wantSlot       bool
	}{
		{name: "mode and persona off", personaEnabled: "false", wantSlot: false},
		{name: "persona enabled", personaEnabled: "true", wantSlot: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			repo := &codexPersonaFirstWriterRepo{}
			settingService := NewSettingService(&codexVersionSettingRepoStub{values: map[string]string{
				SettingKeyOpenAICodexDevicePoolSize:   "3",
				SettingKeyOpenAICodexUAPersonaEnabled: tt.personaEnabled,
			}}, &config.Config{})
			svc := &OpenAIGatewayService{accountRepo: repo, cfg: &config.Config{}, settingService: settingService}
			account := newTestOAuthAccount(2013, map[string]any{codexFingerprintModeExtraKey: "off"})
			c := newFingerprintStageTestContext(t)
			c.Request = c.Request.WithContext(WithSub2APIUserID(c.Request.Context(), 99))
			c.Request.Header.Set("session-id", "real-client-session")

			ids, err := svc.resolveCodexFingerprintIDsForOutbound(c, account, c.Request.Header, false)

			require.NoError(t, err)
			require.NotNil(t, ids)
			require.Equal(t, codexFingerprintOff, ids.mode)
			if tt.wantSlot {
				require.Positive(t, ids.deviceSlot)
				require.Positive(t, repo.casN)
			} else {
				require.Zero(t, ids.deviceSlot)
				require.Zero(t, repo.casN, "off mode without persona must not create persisted pool slots")
			}
		})
	}
}

func TestApplyCodexFingerprintClientMetadataRaw_NonObjectBodyUntouched(t *testing.T) {
	account := newTestOAuthAccount(4244, map[string]any{codexFingerprintModeExtraKey: "session"})
	ids := resolveCodexFingerprintIDs(account, "client-sess-nonobj", codexFingerprintSession)
	require.NotNil(t, ids)

	for _, body := range []string{`[1,2,3]`, `"plain string"`, `not json at all`} {
		out, changed, err := applyCodexFingerprintClientMetadataRaw([]byte(body), ids)
		require.NoError(t, err)
		assert.False(t, changed, "非 JSON 对象 body 不应被改写: %s", body)
		assert.Equal(t, []byte(body), out)
	}
}
