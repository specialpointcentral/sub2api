package service

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func newNonCodexTestAPIKeyAccount(id int64, extra map[string]any) *Account {
	return &Account{ID: id, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Extra: extra}
}

func TestGetOpenAINonCodexTrafficPolicy(t *testing.T) {
	tests := []struct {
		name    string
		account *Account
		want    NonCodexTrafficPolicy
	}{
		{name: "nil account", want: NonCodexTrafficPolicyOff},
		{name: "non OpenAI account", account: &Account{Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Extra: map[string]any{nonCodexTrafficPolicyExtraKey: "pi"}}, want: NonCodexTrafficPolicyOff},
		{name: "missing defaults off", account: newTestOAuthAccount(1501, nil), want: NonCodexTrafficPolicyOff},
		{name: "null defaults off", account: newTestOAuthAccount(1502, map[string]any{nonCodexTrafficPolicyExtraKey: nil}), want: NonCodexTrafficPolicyOff},
		{name: "legacy global value defaults off", account: newTestOAuthAccount(1503, map[string]any{nonCodexTrafficPolicyExtraKey: "pi-normalize"}), want: NonCodexTrafficPolicyOff},
		{name: "invalid defaults off", account: newTestOAuthAccount(1504, map[string]any{nonCodexTrafficPolicyExtraKey: "block"}), want: NonCodexTrafficPolicyOff},
		{name: "OAuth pi", account: newTestOAuthAccount(1505, map[string]any{nonCodexTrafficPolicyExtraKey: "pi"}), want: NonCodexTrafficPolicyPi},
		{name: "API key pi", account: newNonCodexTestAPIKeyAccount(1506, map[string]any{nonCodexTrafficPolicyExtraKey: "pi"}), want: NonCodexTrafficPolicyPi},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, tt.account.GetOpenAINonCodexTrafficPolicy())
		})
	}
}

func TestResolveOpenAINonCodexTrafficActionMatrix(t *testing.T) {
	gin.SetMode(gin.TestMode)

	newContext := func(official bool) *gin.Context {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
		if official {
			c.Request.Header.Set("User-Agent", "codex-tui/0.149.0 (Ubuntu 22.4.0; x86_64) xterm")
			c.Request.Header.Set("originator", "codex_cli_rs")
		} else {
			c.Request.Header.Set("User-Agent", "opencode/1.0")
			c.Request.Header.Set("originator", "opencode")
		}
		return c
	}

	tests := []struct {
		name      string
		official  bool
		account   *Account
		transport OpenAIUpstreamTransport
		want      openAINonCodexTrafficAction
	}{
		{name: "official client stays passthrough", official: true, account: newTestOAuthAccount(1510, map[string]any{nonCodexTrafficPolicyExtraKey: "pi"}), transport: OpenAIUpstreamTransportHTTPSSE, want: openAINonCodexTrafficActionPassthrough},
		{name: "OAuth defaults off", account: newTestOAuthAccount(1511, nil), transport: OpenAIUpstreamTransportHTTPSSE, want: openAINonCodexTrafficActionPassthrough},
		{name: "OAuth HTTP pi", account: newTestOAuthAccount(1512, map[string]any{nonCodexTrafficPolicyExtraKey: "pi"}), transport: OpenAIUpstreamTransportHTTPSSE, want: openAINonCodexTrafficActionPiNormalize},
		{name: "OAuth WS pi degrades", account: newTestOAuthAccount(1513, map[string]any{nonCodexTrafficPolicyExtraKey: "pi"}), transport: OpenAIUpstreamTransportResponsesWebsocketV2Ingress, want: openAINonCodexTrafficActionPassthrough},
		{name: "API key defaults off", account: newNonCodexTestAPIKeyAccount(1514, nil), transport: OpenAIUpstreamTransportHTTPSSE, want: openAINonCodexTrafficActionPassthrough},
		{name: "API key HTTP pi", account: newNonCodexTestAPIKeyAccount(1515, map[string]any{nonCodexTrafficPolicyExtraKey: "pi"}), transport: OpenAIUpstreamTransportHTTPSSE, want: openAINonCodexTrafficActionPiNormalize},
		{name: "API key WS pi degrades", account: newNonCodexTestAPIKeyAccount(1516, map[string]any{nonCodexTrafficPolicyExtraKey: "pi"}), transport: OpenAIUpstreamTransportResponsesWebsocketV2, want: openAINonCodexTrafficActionPassthrough},
		{name: "non OpenAI unaffected", account: &Account{Platform: PlatformAnthropic, Type: AccountTypeOAuth, Extra: map[string]any{nonCodexTrafficPolicyExtraKey: "pi"}}, transport: OpenAIUpstreamTransportHTTPSSE, want: openAINonCodexTrafficActionPassthrough},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, resolveOpenAINonCodexTrafficAction(newContext(tt.official), tt.account, tt.transport))
		})
	}
}

func TestNonCodexTrafficPolicyDoesNotChangeCodexCLIOnlyAdmission(t *testing.T) {
	gin.SetMode(gin.TestMode)
	detector := NewOpenAICodexClientRestrictionDetector(nil)
	account := newTestOAuthAccount(1520, map[string]any{
		"codex_cli_only":              true,
		nonCodexTrafficPolicyExtraKey: "pi",
	})
	policy := CodexRestrictionPolicy{
		Whitelist: []openai.AllowedClientEntry{{
			Originator:            "opencode",
			UAContains:            []string{"opencode/"},
			SkipEngineFingerprint: true,
		}},
	}

	allowed := detector.Detect(newCodexDetectorTestContext("opencode/1.0", "opencode"), account, policy, nil)
	require.True(t, allowed.Enabled)
	require.True(t, allowed.Matched)
	require.Equal(t, CodexClientRestrictionReasonMatchedWhitelistClient, allowed.Reason)

	denied := detector.Detect(newCodexDetectorTestContext("curl/8.0", "curl"), account, policy, nil)
	require.True(t, denied.Enabled)
	require.False(t, denied.Matched)
	require.Equal(t, CodexClientRestrictionReasonNotMatchedUA, denied.Reason)
}
