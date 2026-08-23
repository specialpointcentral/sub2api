package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestApplyOpenAINonCodexPiBodyProjection(t *testing.T) {
	input := []byte(`{
		"model":"gpt-5.6-sol",
		"store":true,
		"stream":false,
		"include":["file_search_call.results"],
		"prompt_cache_key":"client-cache",
		"instructions":"keep these instructions",
		"client_metadata":{"session_id":"client-session","x-codex-installation-id":"install","other":"remove-entire-object"},
		"input":[{"role":"user","content":"hello"}],
		"custom_field":{"keep":true}
	}`)
	projection := openAINonCodexPiProjection{SessionID: "018f47d2-14f0-7a6d-8b42-19c60df7aead"}

	got, err := applyOpenAINonCodexPiBodyProjection(input, projection)
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(got, "client_metadata").Exists(), "client_metadata must be removed as a whole")
	require.False(t, gjson.GetBytes(got, "store").Bool())
	require.True(t, gjson.GetBytes(got, "stream").Bool())
	require.Equal(t, `["file_search_call.results","reasoning.encrypted_content"]`, gjson.GetBytes(got, "include").Raw)
	require.Equal(t, projection.SessionID, gjson.GetBytes(got, "prompt_cache_key").String())
	require.Equal(t, "keep these instructions", gjson.GetBytes(got, "instructions").String())
	require.True(t, gjson.GetBytes(got, "custom_field.keep").Bool(), "unrelated body fields remain intact")

	deduplicated, err := applyOpenAINonCodexPiBodyProjection(
		[]byte(`{"include":["reasoning.encrypted_content","file_search_call.results","reasoning.encrypted_content"]}`),
		projection,
	)
	require.NoError(t, err)
	require.Equal(t, `["reasoning.encrypted_content","file_search_call.results"]`, gjson.GetBytes(deduplicated, "include").Raw)
}

func TestTruncateOpenAIPromptCacheKeyUsesUnicodeCodePoints(t *testing.T) {
	input := strings.Repeat("界", 65)
	require.Equal(t, strings.Repeat("界", 64), truncateOpenAIPromptCacheKey(input))
}

func TestApplyOpenAINonCodexPiHeadersProjection(t *testing.T) {
	headers := http.Header{
		"X-Codex-Installation-Id": {"install"},
		"X-Codex-Turn-State":      {"opaque"},
		"X-Codex-Unknown":         {"remove-prefix"},
		"Conversation_id":         {"conversation"},
		"Thread-Id":               {"thread"},
		"Session_id":              {"legacy-session"},
		"Openai-Beta":             {"assistants=v2"},
		"Version":                 {"0.149.0"},
		"Chatgpt-Account-Id":      {"org-123"},
		"Accept-Language":         {"en-US"},
	}
	projection := openAINonCodexPiProjection{
		SessionID:  "018f47d2-14f0-7a6d-8b42-19c60df7aead",
		Originator: "pi-custom",
		UserAgent:  "pi-custom (linux 6.8.0; x64)",
	}

	applyOpenAINonCodexPiHeadersProjection(headers, projection)

	for _, name := range []string{
		"x-codex-installation-id", "x-codex-turn-state", "x-codex-unknown",
		"conversation_id", "thread-id", "version",
	} {
		require.Empty(t, headers.Get(name), name+" must be stripped")
	}
	require.Equal(t, projection.SessionID, headers.Get("session-id"))
	require.Equal(t, projection.SessionID, headers.Get("session_id"), "an already-whitelisted underscore form must not conflict")
	require.Equal(t, projection.SessionID, headers.Get("x-client-request-id"))
	require.Equal(t, "responses=experimental", headers.Get("OpenAI-Beta"))
	require.Equal(t, projection.Originator, headers.Get("originator"))
	require.Equal(t, projection.UserAgent, headers.Get("user-agent"))
	require.Equal(t, "org-123", headers.Get("chatgpt-account-id"))
	require.Equal(t, "en-US", headers.Get("accept-language"))
}

func TestResolveOpenAINonCodexPiProjectionUsesStableNamespacedUUIDv7(t *testing.T) {
	accountA := newTestOAuthAccount(1001, map[string]any{codexFingerprintSeedExtraKey: testCodexFingerprintSeed})
	accountB := newNonCodexTestAPIKeyAccount(1002, map[string]any{codexFingerprintSeedExtraKey: "22222222-2222-4222-8222-222222222222"})

	first, err := resolveOpenAINonCodexPiProjection(accountA, "sticky-session-hash", 0, false)
	require.NoError(t, err)
	second, err := resolveOpenAINonCodexPiProjection(accountA, "sticky-session-hash", 0, false)
	require.NoError(t, err)
	otherSession, err := resolveOpenAINonCodexPiProjection(accountA, "other-sticky-session", 0, false)
	require.NoError(t, err)
	otherAccount, err := resolveOpenAINonCodexPiProjection(accountB, "sticky-session-hash", 0, false)
	require.NoError(t, err)

	require.Equal(t, first, second)
	require.NotEqual(t, first.SessionID, otherSession.SessionID)
	require.NotEqual(t, first.SessionID, otherAccount.SessionID)
	parsed, err := uuid.Parse(first.SessionID)
	require.NoError(t, err)
	require.Equal(t, uuid.Version(7), parsed.Version())
	require.NotContains(t, first.UserAgent, "{platform}")
	require.NotContains(t, first.UserAgent, "{release}")
	require.NotContains(t, first.UserAgent, "{arch}")
	require.Equal(t, "pi", first.Originator)
}

func TestResolveOpenAINonCodexPiProjectionUsesFrozenAccountPersona(t *testing.T) {
	account := newTestOAuthAccount(1003, map[string]any{
		codexFingerprintSeedExtraKey: testCodexFingerprintSeed,
		codexUAPersonaExtraKey: map[string]any{
			"platform": "mac",
			"sandbox":  "seatbelt",
		},
	})
	require.Equal(t, codexUAPersonaUbuntu, weightedCodexUAPersonaSelection(testCodexFingerprintSeed).Platform,
		"the fixture must distinguish the frozen persona from the weighted fallback")

	projection, err := resolveOpenAINonCodexPiProjection(account, "sticky-session-hash", 0, false)

	require.NoError(t, err)
	require.Equal(t, "pi (darwin 24.6.0; arm64)", projection.UserAgent)
}

func TestResolveOpenAINonCodexPiProjectionFallsBackToWeightedPersona(t *testing.T) {
	account := newTestOAuthAccount(1004, map[string]any{codexFingerprintSeedExtraKey: testCodexFingerprintSeed})
	want := renderOpenAINonCodexPiUserAgent(weightedCodexUAPersonaSelection(testCodexFingerprintSeed).Platform)

	projection, err := resolveOpenAINonCodexPiProjection(account, "sticky-session-hash", 0, false)

	require.NoError(t, err)
	require.Equal(t, "pi (linux 6.8.0; x64)", want, "the weighted fixture must remain hand-checkable")
	require.Equal(t, want, projection.UserAgent)
}

func TestResolveOpenAINonCodexPiProjectionKeepsFrozenPersonaUntilPoolHasSlot(t *testing.T) {
	account := newTestOAuthAccount(1005, map[string]any{
		codexFingerprintSeedExtraKey: testCodexFingerprintSeed,
		codexUAPersonaExtraKey: map[string]any{
			"platform": "mac",
			"sandbox":  "seatbelt",
		},
		codexDevicePoolExtraKey: codexDevicePoolState{Version: 1, NextSlot: 1, Slots: []codexDeviceSlot{}},
	})

	projection, err := resolveOpenAINonCodexPiProjection(account, "sticky-session-hash", 99, true)

	require.NoError(t, err)
	require.Equal(t, "pi (darwin 24.6.0; arm64)", projection.UserAgent)
}

func TestPrepareOpenAINonCodexPiProjectionIsolatesDownstreamAPIKeys(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &OpenAIGatewayService{}
	account := newTestOAuthAccount(1050, map[string]any{
		nonCodexTrafficPolicyExtraKey: "pi",
		codexFingerprintSeedExtraKey:  testCodexFingerprintSeed,
	})
	body := []byte(`{"model":"gpt-5.6-sol","prompt_cache_key":"shared-client-session","input":"hello"}`)

	resolve := func(apiKeyID int64) string {
		t.Helper()
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
		c.Request.Header.Set("User-Agent", "opencode/1.0")
		c.Set("api_key", &APIKey{ID: apiKeyID})
		require.NoError(t, svc.prepareOpenAINonCodexPiProjection(c, account, body, OpenAIUpstreamTransportHTTPSSE))
		projection := stagedOpenAINonCodexPiProjection(c)
		require.NotNil(t, projection)
		return projection.SessionID
	}

	require.Equal(t, resolve(501), resolve(501), "same downstream tenant and sticky session must be stable")
	require.NotEqual(t, resolve(501), resolve(502), "different downstream API keys must not share upstream pi identity")
}

func TestPrepareOpenAINonCodexPiProjectionUsesActiveDeviceSlotPersona(t *testing.T) {
	gin.SetMode(gin.TestMode)
	settingService := NewSettingService(&codexVersionSettingRepoStub{values: map[string]string{
		SettingKeyOpenAICodexDevicePoolSize:   "3",
		SettingKeyOpenAICodexUAPersonaEnabled: "true",
	}}, &config.Config{})
	svc := &OpenAIGatewayService{settingService: settingService}
	slots := []codexDeviceSlot{
		{ID: 1, Platform: codexUAPersonaWindows, Sandbox: "none", CreatedFor: "2"},
		{ID: 2, Platform: codexUAPersonaUbuntu, Sandbox: "seccomp", CreatedFor: "1"},
	}
	account := newTestOAuthAccount(1053, map[string]any{
		nonCodexTrafficPolicyExtraKey: "pi",
		codexFingerprintSeedExtraKey:  testCodexFingerprintSeed,
		codexUAPersonaExtraKey: map[string]any{
			"platform": "mac",
			"sandbox":  "seatbelt",
		},
		codexDevicePoolExtraKey: codexDevicePoolState{
			Version:  1,
			NextSlot: 3,
			Slots:    slots,
		},
	})

	resolve := func(userID int64) string {
		t.Helper()
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.6-sol","input":"hello"}`))
		c.Request = c.Request.WithContext(WithSub2APIUserID(c.Request.Context(), userID))
		c.Request.Header.Set("User-Agent", "opencode/1.0")
		c.Set("api_key", &APIKey{ID: 503})
		require.NoError(t, svc.prepareOpenAINonCodexPiProjection(c, account, []byte(`{"model":"gpt-5.6-sol","input":"hello"}`), OpenAIUpstreamTransportHTTPSSE))
		projection := stagedOpenAINonCodexPiProjection(c)
		require.NotNil(t, projection)
		return projection.UserAgent
	}

	require.Equal(t, "pi (linux 6.8.0; x64)", resolve(1), "user 1 rendezvous-selects Ubuntu slot 2")
	require.Equal(t, "pi (win32 10.0.26100; x64)", resolve(2), "user 2 rendezvous-selects Windows slot 1")
}

func TestPrepareOpenAINonCodexPiProjectionDoesNotActivatePoolForAPIKeyOffMode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	settingService := NewSettingService(&codexVersionSettingRepoStub{values: map[string]string{
		SettingKeyOpenAICodexDevicePoolSize:   "3",
		SettingKeyOpenAICodexUAPersonaEnabled: "true",
	}}, &config.Config{})
	svc := &OpenAIGatewayService{settingService: settingService}
	account := newNonCodexTestAPIKeyAccount(1054, map[string]any{
		nonCodexTrafficPolicyExtraKey: "pi",
		codexFingerprintModeExtraKey:  "off",
		codexFingerprintSeedExtraKey:  testCodexFingerprintSeed,
		codexUAPersonaExtraKey: map[string]any{
			"platform": "mac",
			"sandbox":  "seatbelt",
		},
		codexDevicePoolExtraKey: codexDevicePoolState{
			Version:  1,
			NextSlot: 2,
			Slots: []codexDeviceSlot{{
				ID:         1,
				Platform:   codexUAPersonaWindows,
				Sandbox:    "none",
				CreatedFor: "99",
			}},
		},
	})
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.6-sol","input":"hello"}`))
	c.Request = c.Request.WithContext(WithSub2APIUserID(c.Request.Context(), 99))
	c.Request.Header.Set("User-Agent", "opencode/1.0")
	c.Set("api_key", &APIKey{ID: 504})

	require.NoError(t, svc.prepareOpenAINonCodexPiProjection(c, account, []byte(`{"model":"gpt-5.6-sol","input":"hello"}`), OpenAIUpstreamTransportHTTPSSE))
	projection := stagedOpenAINonCodexPiProjection(c)
	require.NotNil(t, projection)
	require.Equal(t, "pi (darwin 24.6.0; arm64)", projection.UserAgent)
}

func TestPrepareOpenAINonCodexPiProjectionSupportsOpenAIAPIKeyAccount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"gpt-5.6-sol","input":"hello"}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("User-Agent", "opencode/1.0")
	account := newNonCodexTestAPIKeyAccount(1051, map[string]any{
		nonCodexTrafficPolicyExtraKey: "pi",
		codexFingerprintSeedExtraKey:  testCodexFingerprintSeed,
	})

	require.NoError(t, (&OpenAIGatewayService{}).prepareOpenAINonCodexPiProjection(c, account, body, OpenAIUpstreamTransportHTTPSSE))
	require.NotNil(t, stagedOpenAINonCodexPiProjection(c))
}

func TestPrepareOpenAINonCodexPiProjectionPersistsSeedForAPIKeyOffMode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"gpt-5.6-sol","input":"hello"}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("User-Agent", "opencode/1.0")
	repo := &codexPersonaFirstWriterRepo{seed: testCodexFingerprintSeed}
	svc := &OpenAIGatewayService{accountRepo: repo}
	account := newNonCodexTestAPIKeyAccount(1052, map[string]any{
		codexFingerprintModeExtraKey:  "off",
		nonCodexTrafficPolicyExtraKey: "pi",
	})

	require.NoError(t, svc.prepareOpenAINonCodexPiProjection(c, account, body, OpenAIUpstreamTransportHTTPSSE))
	require.Equal(t, 1, repo.ensureSeedN, "pi normalization requires a stable account namespace even when fingerprint convergence is off")
	projection := stagedOpenAINonCodexPiProjection(c)
	require.NotNil(t, projection)
	require.NotEmpty(t, projection.SessionID)
	require.NotContains(t, account.Extra, codexFingerprintSeedExtraKey, "request-time persistence must not mutate the scheduler snapshot")
}

func TestIsOpenAINonCodexTrafficRequestUsesStrictOfficialClassification(t *testing.T) {
	gin.SetMode(gin.TestMode)
	account := newTestOAuthAccount(1101, nil)
	account.Extra["codex_cli_only_app_server"] = true

	newContext := func(userAgent, originator string) *gin.Context {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
		c.Request.Header.Set("User-Agent", userAgent)
		c.Request.Header.Set("originator", originator)
		return c
	}

	require.False(t, isOpenAINonCodexTrafficRequest(newContext("codex-tui/0.149.0 (Ubuntu 22.4.0; x86_64) xterm", "other"), account))
	require.False(t, isOpenAINonCodexTrafficRequest(newContext("curl/8.0", "codex-tui"), account))
	require.True(t, isOpenAINonCodexTrafficRequest(newContext("opencode/1.0", "opencode"), account), "AppServer/whitelist-style clients remain non-official")
	require.True(t, isOpenAINonCodexTrafficRequest(newContext("opencode/1.0", "opencode"), &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}))
}

func TestShouldApplyOpenAINonCodexPiNormalizationDefaultsOffAndSkipsWS(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Request.Header.Set("User-Agent", "opencode/1.0")
	offAccount := newTestOAuthAccount(1201, nil)
	piAccount := newTestOAuthAccount(1202, map[string]any{nonCodexTrafficPolicyExtraKey: "pi"})

	require.False(t, shouldApplyOpenAINonCodexPiNormalization(c, offAccount, OpenAIUpstreamTransportHTTPSSE))
	require.True(t, shouldApplyOpenAINonCodexPiNormalization(c, piAccount, OpenAIUpstreamTransportHTTPSSE))
	require.False(t, shouldApplyOpenAINonCodexPiNormalization(c, piAccount, OpenAIUpstreamTransportResponsesWebsocketV2))
}

func TestOpenAINonCodexPiProjectionMatchesHTTPAndPassthroughBuilders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	projection := openAINonCodexPiProjection{
		SessionID:  "018f47d2-14f0-7a6d-8b42-19c60df7aead",
		Originator: "pi",
		UserAgent:  "pi (linux 6.8.0; x64)",
	}
	body, err := applyOpenAINonCodexPiBodyProjection([]byte(`{"model":"gpt-5.6-sol","store":true,"stream":false,"instructions":"keep","client_metadata":{"session_id":"leak"}}`), projection)
	require.NoError(t, err)
	account := newTestOAuthAccount(1301, nil)
	account.Credentials = map[string]any{"chatgpt_account_id": "org-projected"}
	svc := &OpenAIGatewayService{cfg: &config.Config{}}

	build := func(passthrough bool) *http.Request {
		t.Helper()
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
		c.Request.Header.Set("User-Agent", "opencode/1.0")
		c.Request.Header.Set("originator", "opencode")
		c.Request.Header.Set("conversation_id", "strip-conversation")
		c.Request.Header.Set("x-codex-installation-id", "strip-installation")
		stageOpenAINonCodexPiProjection(c, &projection)
		var req *http.Request
		var buildErr error
		if passthrough {
			req, buildErr = svc.buildUpstreamRequestOpenAIPassthrough(context.Background(), c, account, body, "token")
		} else {
			req, buildErr = svc.buildUpstreamRequest(context.Background(), c, account, body, "token", true, projection.SessionID, false)
		}
		require.NoError(t, buildErr)
		return req
	}

	httpReq := build(false)
	passthroughReq := build(true)
	for _, req := range []*http.Request{httpReq, passthroughReq} {
		require.Equal(t, projection.SessionID, req.Header.Get("session-id"))
		require.Equal(t, projection.SessionID, req.Header.Get("x-client-request-id"))
		require.Equal(t, "responses=experimental", req.Header.Get("OpenAI-Beta"))
		require.Equal(t, projection.Originator, req.Header.Get("originator"))
		require.Equal(t, projection.UserAgent, req.Header.Get("user-agent"))
		require.Equal(t, "org-projected", req.Header.Get("chatgpt-account-id"))
		require.Empty(t, req.Header.Get("conversation_id"))
		require.Empty(t, req.Header.Get("x-codex-installation-id"))
		wireBody, readErr := io.ReadAll(req.Body)
		require.NoError(t, readErr)
		require.JSONEq(t, string(body), string(wireBody))
	}
}

func TestOpenAINonCodexPiProjectionIsFinalForBothHTTPForwardingPaths(t *testing.T) {
	gin.SetMode(gin.TestMode)

	for _, passthrough := range []bool{false, true} {
		name := "transformed"
		if passthrough {
			name = "passthrough"
		}
		t.Run(name, func(t *testing.T) {
			body := []byte(`{
				"model":"gpt-5.6-sol",
				"store":true,
				"stream":false,
				"include":["file_search_call.results"],
				"prompt_cache_key":"client-session",
				"instructions":"preserve exactly",
				"client_metadata":{"session_id":"client-session","x-codex-installation-id":"leak"},
				"input":[{"type":"message","role":"user","content":"hello"}]
			}`)
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
			c.Request.Header.Set("Content-Type", "application/json")
			c.Request.Header.Set("User-Agent", "opencode/1.0")
			c.Request.Header.Set("originator", "opencode")
			c.Request.Header.Set("conversation_id", "strip-conversation")
			c.Request.Header.Set("thread-id", "strip-thread")
			c.Request.Header.Set("x-codex-installation-id", "strip-installation")

			upstream := &httpUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusBadRequest,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"error":{"type":"invalid_request_error","message":"captured"}}`)),
			}}
			svc := &OpenAIGatewayService{
				cfg:          &config.Config{},
				httpUpstream: upstream,
			}
			accountExtra := map[string]any{
				nonCodexTrafficPolicyExtraKey: "pi",
				codexFingerprintSeedExtraKey:  testCodexFingerprintSeed,
			}
			if passthrough {
				accountExtra["openai_passthrough"] = true
			}
			account := newTestOAuthAccount(1401, accountExtra)
			account.Name = name
			account.Concurrency = 1
			account.Credentials = map[string]any{
				"access_token":       "oauth-token",
				"chatgpt_account_id": "org-projected",
			}

			_, err := svc.Forward(context.Background(), c, account, body)
			require.Error(t, err)
			require.NotNil(t, upstream.lastReq)

			projectedSession := gjson.GetBytes(upstream.lastBody, "prompt_cache_key").String()
			parsed, parseErr := uuid.Parse(projectedSession)
			require.NoError(t, parseErr)
			require.Equal(t, uuid.Version(7), parsed.Version())
			require.False(t, gjson.GetBytes(upstream.lastBody, "client_metadata").Exists())
			require.False(t, gjson.GetBytes(upstream.lastBody, "store").Bool())
			require.True(t, gjson.GetBytes(upstream.lastBody, "stream").Bool())
			require.Equal(t, `["file_search_call.results","reasoning.encrypted_content"]`, gjson.GetBytes(upstream.lastBody, "include").Raw)
			require.Equal(t, "preserve exactly", gjson.GetBytes(upstream.lastBody, "instructions").String())
			require.Equal(t, projectedSession, upstream.lastReq.Header.Get("session-id"))
			require.Equal(t, projectedSession, upstream.lastReq.Header.Get("x-client-request-id"))
			require.Equal(t, "responses=experimental", upstream.lastReq.Header.Get("OpenAI-Beta"))
			require.Equal(t, "pi", upstream.lastReq.Header.Get("originator"))
			require.Contains(t, upstream.lastReq.Header.Get("user-agent"), "pi (")
			require.Equal(t, "org-projected", upstream.lastReq.Header.Get("chatgpt-account-id"))
			for _, header := range []string{"conversation_id", "thread-id", "x-codex-installation-id", "x-codex-beta-features", "version"} {
				require.Empty(t, upstream.lastReq.Header.Get(header), header+" must be absent after final projection")
			}
		})
	}
}

func TestOpenAINonCodexPiProjectionCoversChatCompletionsHTTPEntrypoint(t *testing.T) {
	runOpenAINonCodexPiCompatEntrypointWireTest(
		t,
		"/v1/chat/completions",
		[]byte(`{"model":"gpt-5.6-sol","input":"hello","store":true,"stream":false,"include":["file_search_call.results"],"client_metadata":{"client_value":"remove"}}`),
		`["file_search_call.results","reasoning.encrypted_content"]`,
		func(svc *OpenAIGatewayService, c *gin.Context, account *Account, body []byte) (*OpenAIForwardResult, error) {
			return svc.ForwardAsChatCompletions(context.Background(), c, account, body, "", "")
		},
	)
}

func TestOpenAINonCodexPiProjectionCoversMessagesHTTPEntrypoint(t *testing.T) {
	runOpenAINonCodexPiCompatEntrypointWireTest(
		t,
		"/v1/messages",
		[]byte(`{"model":"gpt-5.6-sol","max_tokens":16,"messages":[{"role":"user","content":"hello"}],"stream":false}`),
		`["reasoning.encrypted_content"]`,
		func(svc *OpenAIGatewayService, c *gin.Context, account *Account, body []byte) (*OpenAIForwardResult, error) {
			return svc.ForwardAsAnthropic(context.Background(), c, account, body, "", "")
		},
	)
}

func TestOpenAINonCodexPiNormalizationWebSocketFramePassthrough(t *testing.T) {
	cfg := newOpenAIWSIngressFingerprintConfig()
	captureConn := &openAIWSCaptureConn{events: [][]byte{
		[]byte(`{"type":"response.completed","response":{"id":"resp_pi_ws_passthrough","model":"gpt-5.6-sol","usage":{"input_tokens":1,"output_tokens":1}}}`),
	}}
	captureDialer := &openAIWSCaptureDialer{conn: captureConn}
	pool := newOpenAIWSConnPool(cfg)
	pool.setClientDialerForTest(captureDialer)
	svc := &OpenAIGatewayService{
		cfg:              cfg,
		httpUpstream:     &httpUpstreamRecorder{},
		cache:            &stubGatewayCache{},
		openaiWSResolver: NewOpenAIWSProtocolResolver(cfg),
		toolCorrector:    NewCodexToolCorrector(),
		openaiWSPool:     pool,
		settingService:   NewSettingService(&gatewayTTLSettingRepo{}, cfg),
	}
	account := newTestOAuthAccount(1456, map[string]any{
		"responses_websockets_v2_enabled": true,
		codexFingerprintModeExtraKey:      "off",
		nonCodexTrafficPolicyExtraKey:     "pi",
	})
	account.Credentials = map[string]any{"access_token": "oauth-token", "chatgpt_account_id": "chatgpt-account"}
	account.Status, account.Schedulable, account.Concurrency = StatusActive, true, 1
	clientFrame := `{
		"type":"response.create",
		"model":"gpt-5.6-sol",
		"store":true,
		"stream":false,
		"include":["file_search_call.results"],
		"prompt_cache_key":"client-ws-cache",
		"client_metadata":{"client_value":"preserve"},
		"input":"hello"
	}`

	err := runOpenAIWSIngressFingerprintTest(
		t,
		svc,
		account,
		http.Header{"User-Agent": []string{"opencode/1.0"}, "originator": []string{"opencode"}},
		clientFrame,
		true,
	)
	require.NoError(t, err)
	require.Equal(t, 1, captureDialer.DialCount())
	require.Len(t, captureConn.writes, 1)
	wireFrame := requestToJSONString(captureConn.writes[0])
	require.True(t, gjson.Get(wireFrame, "store").Bool())
	require.False(t, gjson.Get(wireFrame, "stream").Bool())
	require.Equal(t, `["file_search_call.results"]`, gjson.Get(wireFrame, "include").Raw)
	require.Equal(t, "client-ws-cache", gjson.Get(wireFrame, "prompt_cache_key").String())
	require.Equal(t, "preserve", gjson.Get(wireFrame, "client_metadata.client_value").String())
}

func runOpenAINonCodexPiCompatEntrypointWireTest(
	t *testing.T,
	path string,
	body []byte,
	wantInclude string,
	forward func(*OpenAIGatewayService, *gin.Context, *Account, []byte) (*OpenAIForwardResult, error),
) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("User-Agent", "opencode/1.0")
	c.Request.Header.Set("originator", "opencode")
	c.Request.Header.Set("x-codex-installation-id", "must-not-leak")
	c.Set("api_key", &APIKey{ID: 701})

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusBadRequest,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"error":{"type":"invalid_request_error","message":"captured"}}`)),
	}}
	svc := &OpenAIGatewayService{
		cfg:          &config.Config{},
		httpUpstream: upstream,
	}
	account := newTestOAuthAccount(1455, map[string]any{
		nonCodexTrafficPolicyExtraKey: "pi",
		codexFingerprintSeedExtraKey:  testCodexFingerprintSeed,
	})
	account.Name = "pi-compat-entrypoint"
	account.Concurrency = 1
	account.Credentials = map[string]any{
		"access_token":       "oauth-token",
		"chatgpt_account_id": "org-projected",
	}

	result, err := forward(svc, c, account, body)
	require.Error(t, err)
	require.Nil(t, result)
	require.NotNil(t, upstream.lastReq)

	projectedSession := gjson.GetBytes(upstream.lastBody, "prompt_cache_key").String()
	parsed, parseErr := uuid.Parse(projectedSession)
	require.NoError(t, parseErr)
	require.Equal(t, uuid.Version(7), parsed.Version())
	require.False(t, gjson.GetBytes(upstream.lastBody, "client_metadata").Exists())
	require.False(t, gjson.GetBytes(upstream.lastBody, "store").Bool())
	require.True(t, gjson.GetBytes(upstream.lastBody, "stream").Bool())
	require.Equal(t, wantInclude, gjson.GetBytes(upstream.lastBody, "include").Raw)
	require.Equal(t, projectedSession, upstream.lastReq.Header.Get("session-id"))
	require.Equal(t, projectedSession, upstream.lastReq.Header.Get("x-client-request-id"))
	require.Equal(t, "responses=experimental", upstream.lastReq.Header.Get("OpenAI-Beta"))
	require.Equal(t, "pi", upstream.lastReq.Header.Get("originator"))
	require.Contains(t, upstream.lastReq.Header.Get("user-agent"), "pi (")
	require.Empty(t, upstream.lastReq.Header.Get("x-codex-installation-id"))
}

func TestOpenAINonCodexPiProjectionPreservesNonStreamingClientResponseContract(t *testing.T) {
	gin.SetMode(gin.TestMode)

	for _, passthrough := range []bool{false, true} {
		name := "transformed"
		if passthrough {
			name = "passthrough"
		}
		t.Run(name, func(t *testing.T) {
			body := []byte(`{"model":"gpt-5.6-sol","stream":false,"prompt_cache_key":"client-session","instructions":"keep","input":"hello"}`)
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
			c.Request.Header.Set("User-Agent", "opencode/1.0")
			c.Set("api_key", &APIKey{ID: 601})
			upstream := &httpUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body: io.NopCloser(strings.NewReader(
					"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_pi\",\"object\":\"response\",\"model\":\"gpt-5.6-sol\",\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"hello\"}]}],\"usage\":{\"input_tokens\":2,\"output_tokens\":1,\"total_tokens\":3}}}\n\n" +
						"data: [DONE]\n\n",
				)),
			}}
			svc := &OpenAIGatewayService{
				cfg:          &config.Config{},
				httpUpstream: upstream,
			}
			extra := map[string]any{
				nonCodexTrafficPolicyExtraKey: "pi",
				codexFingerprintSeedExtraKey:  testCodexFingerprintSeed,
			}
			if passthrough {
				extra["openai_passthrough"] = true
			}
			account := newTestOAuthAccount(1450, extra)
			account.Name = name
			account.Concurrency = 1
			account.Credentials = map[string]any{"access_token": "oauth-token", "chatgpt_account_id": "org-projected"}

			result, err := svc.Forward(context.Background(), c, account, body)

			require.NoError(t, err)
			require.NotNil(t, result)
			require.False(t, result.Stream)
			require.Contains(t, rec.Header().Get("Content-Type"), "application/json")
			require.Equal(t, "resp_pi", gjson.Get(rec.Body.String(), "id").String())
			require.NotContains(t, rec.Body.String(), "data:")
			require.True(t, gjson.GetBytes(upstream.lastBody, "stream").Bool(), "upstream remains pi-compatible SSE")
		})
	}
}

func runOpenAINonCodexPiNonStreamingSSETest(t *testing.T, passthrough bool, sse string) (*httptest.ResponseRecorder, *OpenAIForwardResult, error) {
	t.Helper()
	body := []byte(`{"model":"gpt-5.6-sol","stream":false,"prompt_cache_key":"client-session","instructions":"keep","input":"hello"}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("User-Agent", "opencode/1.0")
	c.Set("api_key", &APIKey{ID: 602})
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"rid-pi-buffered"}},
		Body:       io.NopCloser(strings.NewReader(sse)),
	}}
	svc := &OpenAIGatewayService{
		cfg:          &config.Config{},
		httpUpstream: upstream,
	}
	extra := map[string]any{
		nonCodexTrafficPolicyExtraKey: "pi",
		codexFingerprintSeedExtraKey:  testCodexFingerprintSeed,
	}
	if passthrough {
		extra["openai_passthrough"] = true
	}
	account := newTestOAuthAccount(1451, extra)
	account.Name = "pi-buffered"
	account.Concurrency = 1
	account.Credentials = map[string]any{"access_token": "oauth-token", "chatgpt_account_id": "org-projected"}

	result, err := svc.Forward(context.Background(), c, account, body)
	return rec, result, err
}

func TestOpenAINonCodexPiNonStreamingTerminalResponsesRemainJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)

	for _, passthrough := range []bool{false, true} {
		path := "transformed"
		if passthrough {
			path = "passthrough"
		}
		for _, terminal := range []string{"response.incomplete", "response.cancelled"} {
			t.Run(path+"/"+terminal, func(t *testing.T) {
				sse := "data: {\"type\":" + fmt.Sprintf("%q", terminal) + ",\"response\":{\"id\":\"resp_terminal\",\"object\":\"response\",\"status\":" + fmt.Sprintf("%q", strings.TrimPrefix(terminal, "response.")) + ",\"output\":[],\"usage\":{\"input_tokens\":2,\"output_tokens\":1}}}\n\n"
				rec, result, err := runOpenAINonCodexPiNonStreamingSSETest(t, passthrough, sse)

				require.NoError(t, err)
				require.NotNil(t, result)
				require.False(t, result.Stream)
				require.Contains(t, rec.Header().Get("Content-Type"), "application/json")
				require.Equal(t, "resp_terminal", gjson.Get(rec.Body.String(), "id").String())
				require.Equal(t, strings.TrimPrefix(terminal, "response."), gjson.Get(rec.Body.String(), "status").String())
				require.NotContains(t, rec.Body.String(), "data:")
			})
		}
	}
}

func TestOpenAINonCodexPiNonStreamingSSEFailuresNeverLeakSSE(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name         string
		sse          string
		wantFailover bool
	}{
		{
			name:         "capacity failed",
			sse:          "data: {\"type\":\"response.failed\",\"response\":{\"error\":{\"type\":\"server_error\",\"code\":\"server_is_overloaded\",\"message\":\"overloaded\"},\"usage\":{\"input_tokens\":2,\"output_tokens\":0}}}\n\n",
			wantFailover: true,
		},
		{
			name:         "EOF before terminal",
			sse:          "data: {\"type\":\"response.in_progress\",\"response\":{\"id\":\"resp_partial\"}}\n\n",
			wantFailover: true,
		},
		{
			name: "non-retryable failed",
			sse:  "data: {\"type\":\"response.failed\",\"response\":{\"error\":{\"type\":\"invalid_request_error\",\"code\":\"content_policy\",\"message\":\"not allowed\"}}}\n\n",
		},
		{
			name: "error only",
			sse:  "data: {\"type\":\"error\",\"error\":{\"type\":\"invalid_request_error\",\"code\":\"invalid_request\",\"message\":\"invalid input\"}}\n\n",
		},
	}

	for _, passthrough := range []bool{false, true} {
		path := "transformed"
		if passthrough {
			path = "passthrough"
		}
		for _, tt := range tests {
			t.Run(path+"/"+tt.name, func(t *testing.T) {
				rec, result, err := runOpenAINonCodexPiNonStreamingSSETest(t, passthrough, tt.sse)

				require.Error(t, err)
				require.Nil(t, result)
				var failoverErr *UpstreamFailoverError
				if tt.wantFailover {
					require.ErrorAs(t, err, &failoverErr)
					require.Empty(t, rec.Body.String(), "retryable failures must not commit a downstream response")
				} else {
					require.NotErrorAs(t, err, &failoverErr)
					require.Contains(t, rec.Header().Get("Content-Type"), "application/json")
					require.NotEmpty(t, gjson.Get(rec.Body.String(), "error.message").String())
					require.NotContains(t, rec.Body.String(), "data:")
				}
			})
		}
	}
}

func TestPrepareOpenAINonCodexPiBufferedResponsePreservesCyberPolicyMark(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	stageOpenAINonCodexPiProjection(c, &openAINonCodexPiProjection{})
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: io.NopCloser(strings.NewReader(
			"data: {\"type\":\"response.failed\",\"response\":{\"error\":{\"code\":\"cyber_policy\",\"message\":\"blocked by network policy\"},\"usage\":{\"input_tokens\":1234,\"output_tokens\":7}}}\n\n",
		)),
	}
	svc := &OpenAIGatewayService{cfg: &config.Config{}}
	account := newTestOAuthAccount(1452, nil)

	err := svc.prepareOpenAINonCodexPiBufferedResponse(c, account, resp, false)

	require.Error(t, err)
	var failoverErr *UpstreamFailoverError
	require.NotErrorAs(t, err, &failoverErr)
	mark := GetOpsCyberPolicy(c)
	require.NotNil(t, mark)
	require.Equal(t, "cyber_policy", mark.Code)
	require.Equal(t, "blocked by network policy", mark.Message)
	require.Equal(t, 1234, mark.UpstreamInTok)
	require.Equal(t, 7, mark.UpstreamOutTok)
	require.Contains(t, rec.Header().Get("Content-Type"), "application/json")
	require.Contains(t, gjson.Get(rec.Body.String(), "error.message").String(), "blocked by network policy")
	require.NotContains(t, rec.Body.String(), "data:")
}

func TestPrepareOpenAINonCodexPiBufferedResponsePreservesErrorPassthroughRule(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	stageOpenAINonCodexPiProjection(c, &openAINonCodexPiProjection{})
	bindPassthroughRule(c, "openai", []string{"context_length_exceeded"}, http.StatusUnprocessableEntity)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(buildContextLengthFailedSSE())),
	}
	svc := &OpenAIGatewayService{cfg: &config.Config{}}
	account := newTestOAuthAccount(1453, nil)

	err := svc.prepareOpenAINonCodexPiBufferedResponse(c, account, resp, false)

	require.Error(t, err)
	require.Contains(t, err.Error(), "passthrough rule matched")
	var failoverErr *UpstreamFailoverError
	require.NotErrorAs(t, err, &failoverErr)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	require.Contains(t, rec.Header().Get("Content-Type"), "application/json")
	require.Equal(t, "upstream_error", gjson.Get(rec.Body.String(), "error.type").String())
	require.Contains(t, gjson.Get(rec.Body.String(), "error.message").String(), "context window")
	require.NotContains(t, rec.Body.String(), "data:")
}

func TestOpenAINonCodexPiNormalizationSkipsCompactForBothHTTPPaths(t *testing.T) {
	gin.SetMode(gin.TestMode)

	for _, passthrough := range []bool{false, true} {
		name := "transformed"
		if passthrough {
			name = "passthrough"
		}
		t.Run(name, func(t *testing.T) {
			body := []byte(`{"model":"gpt-5.6-sol","instructions":"compact","input":[{"role":"user","content":"hello"}]}`)
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses/compact", bytes.NewReader(body))
			c.Request.Header.Set("User-Agent", "opencode/1.0")
			upstream := &httpUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusBadRequest,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"captured"}}`)),
			}}
			svc := &OpenAIGatewayService{
				cfg:          &config.Config{},
				httpUpstream: upstream,
			}
			extra := map[string]any{nonCodexTrafficPolicyExtraKey: "pi"}
			if passthrough {
				extra["openai_passthrough"] = true
			}
			account := newTestOAuthAccount(1460, extra)
			account.Name = name
			account.Concurrency = 1
			account.Credentials = map[string]any{"access_token": "oauth-token", "chatgpt_account_id": "org-projected"}

			_, err := svc.Forward(context.Background(), c, account, body)

			require.Error(t, err)
			require.NotNil(t, upstream.lastReq)
			require.Equal(t, "/backend-api/codex/responses/compact", upstream.lastReq.URL.Path)
			require.False(t, gjson.GetBytes(upstream.lastBody, "store").Exists())
			require.False(t, gjson.GetBytes(upstream.lastBody, "stream").Exists())
			require.False(t, gjson.GetBytes(upstream.lastBody, "include").Exists())
			require.False(t, gjson.GetBytes(upstream.lastBody, "prompt_cache_key").Exists())
			require.NotEqual(t, "pi", upstream.lastReq.Header.Get("originator"))
			require.NotEmpty(t, upstream.lastReq.Header.Get("version"))
		})
	}
}
