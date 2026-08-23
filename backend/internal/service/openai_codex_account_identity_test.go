package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type codexAccountIdentityRepoStub struct {
	AccountRepository
	account *Account
}

func (s *codexAccountIdentityRepoStub) GetByID(_ context.Context, _ int64) (*Account, error) {
	return s.account, nil
}

func TestCodexRequestBodyIdentityNamespaceIsStablePerOAuthAccount(t *testing.T) {
	body := []byte(`{"model":"gpt-5.6-codex","prompt_cache_key":"client-session","client_metadata":{"x-codex-installation-id":"client-installation","session_id":"client-session","thread_id":"client-thread","x-codex-window-id":"client-window","x-codex-turn-metadata":"{\"installation_id\":\"client-installation\",\"session_id\":\"client-session\",\"thread_id\":\"client-thread\",\"turn_id\":\"client-turn\",\"window_id\":\"client-window\"}"}}`)
	account11 := &Account{ID: 11, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"chatgpt_account_id": "chatgpt-account-11"}}
	account19 := &Account{ID: 19, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"chatgpt_account_id": "chatgpt-account-19"}}

	first, changed, err := applyCodexAccountIdentityClientMetadataRaw(body, account11, 77)
	require.NoError(t, err)
	require.True(t, changed)
	firstAgain, changed, err := applyCodexAccountIdentityClientMetadataRaw(body, account11, 77)
	require.NoError(t, err)
	require.True(t, changed)
	second, changed, err := applyCodexAccountIdentityClientMetadataRaw(body, account19, 77)
	require.NoError(t, err)
	require.True(t, changed)
	require.JSONEq(t, string(first), string(firstAgain))

	paths := []string{
		"prompt_cache_key",
		"client_metadata.x-codex-installation-id",
		"client_metadata.session_id",
		"client_metadata.thread_id",
		"client_metadata.x-codex-window-id",
	}
	for _, path := range paths {
		require.NotEqual(t, gjson.GetBytes(body, path).String(), gjson.GetBytes(first, path).String(), path)
		require.NotEqual(t, gjson.GetBytes(first, path).String(), gjson.GetBytes(second, path).String(), path)
	}
	require.Equal(t, gjson.GetBytes(first, "prompt_cache_key").String(), gjson.GetBytes(first, "client_metadata.session_id").String())

	var embeddedFirst map[string]any
	var embeddedSecond map[string]any
	require.NoError(t, json.Unmarshal([]byte(gjson.GetBytes(first, "client_metadata.x-codex-turn-metadata").String()), &embeddedFirst))
	require.NoError(t, json.Unmarshal([]byte(gjson.GetBytes(second, "client_metadata.x-codex-turn-metadata").String()), &embeddedSecond))
	for _, field := range []string{"installation_id", "session_id", "thread_id", "turn_id", "window_id"} {
		require.NotEqual(t, embeddedFirst[field], embeddedSecond[field], field)
	}
}

func TestCodexAccountIdentityNamespaceUsesStableCredentialSource(t *testing.T) {
	firstRow := &Account{ID: 8, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"chatgpt_account_id": "shared-upstream-account"}}
	secondRow := &Account{ID: 19, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"chatgpt_account_id": "shared-upstream-account"}}
	require.Equal(t, codexAccountIdentityNamespace(firstRow), codexAccountIdentityNamespace(secondRow))

	firstUser := &Account{ID: 20, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"chatgpt_account_id": "team-account", "chatgpt_user_id": "user-1"}}
	sameUser := &Account{ID: 21, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"chatgpt_account_id": "team-account", "chatgpt_user_id": "user-1"}}
	secondUser := &Account{ID: 22, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"chatgpt_account_id": "team-account", "chatgpt_user_id": "user-2"}}
	require.Equal(t, codexAccountIdentityNamespace(firstUser), codexAccountIdentityNamespace(sameUser))
	require.NotEqual(t, codexAccountIdentityNamespace(firstUser), codexAccountIdentityNamespace(secondUser))

	seed := "11111111-1111-4111-8111-111111111111"
	seeded := &Account{ID: 11, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{codexFingerprintSeedExtraKey: seed}}
	require.Equal(t, "seed:"+seed, codexAccountIdentityNamespace(seeded))

	// Local row IDs repeat across independent deployments, so they are not a
	// safe fallback for upstream identity.
	require.Empty(t, codexAccountIdentityNamespace(&Account{ID: 11, Platform: PlatformOpenAI, Type: AccountTypeOAuth}))

	setupTokenA := &Account{ID: 30, Platform: PlatformOpenAI, Type: AccountTypeSetupToken, Credentials: map[string]any{"access_token": "setup-token-a"}}
	setupTokenADuplicate := &Account{ID: 31, Platform: PlatformOpenAI, Type: AccountTypeSetupToken, Credentials: map[string]any{"access_token": "setup-token-a"}}
	setupTokenB := &Account{ID: 32, Platform: PlatformOpenAI, Type: AccountTypeSetupToken, Credentials: map[string]any{"access_token": "setup-token-b"}}
	setupNamespace := codexAccountIdentityNamespace(setupTokenA)
	require.NotEmpty(t, setupNamespace)
	require.NotContains(t, setupNamespace, "setup-token-a")
	require.Equal(t, setupNamespace, codexAccountIdentityNamespace(setupTokenADuplicate))
	require.NotEqual(t, setupNamespace, codexAccountIdentityNamespace(setupTokenB))
}

func TestCodexAccountIdentitySourceResolvesShadowAndOverwritesFailoverContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	parentID := int64(11)
	parent := &Account{ID: parentID, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{
		"chatgpt_account_id": "team-account",
		"chatgpt_user_id":    "user-1",
	}}
	shadow := &Account{ID: 111, ParentAccountID: &parentID, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	service := &OpenAIGatewayService{accountRepo: &codexAccountIdentityRepoStub{account: parent}}

	resolved, err := service.prepareCodexAccountIdentitySource(context.Background(), c, shadow)
	require.NoError(t, err)
	require.Same(t, parent, resolved)
	require.Same(t, parent, codexAccountIdentitySource(c, shadow))

	req, err := service.buildUpstreamRequest(
		context.Background(), c, shadow,
		[]byte(`{"model":"gpt-5.6-codex","stream":true,"prompt_cache_key":"client-session"}`),
		"token", true, "client-session", true,
	)
	require.NoError(t, err)
	require.Equal(t, isolateOpenAISessionID(0, "client-session"), req.Header.Get("session_id"))

	next := &Account{ID: 19, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{
		"chatgpt_account_id": "other-account",
		"chatgpt_user_id":    "user-2",
	}}
	resolved, err = service.prepareCodexAccountIdentitySource(context.Background(), c, next)
	require.NoError(t, err)
	require.Same(t, next, resolved)
	require.Same(t, next, codexAccountIdentitySource(c, shadow))
}
