//go:build unit

package admin

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type kiroRefreshOAuthStub struct {
	account *service.Account
	calls   int
}

func (s *kiroRefreshOAuthStub) RefreshAccountToken(_ context.Context, account *service.Account) (*service.KiroTokenInfo, error) {
	s.calls++
	s.account = account
	return &service.KiroTokenInfo{
		AccessToken:  "new-access",
		RefreshToken: "new-refresh",
		ExpiresAt:    "1800000000",
		AuthMethod:   "social",
		Provider:     "BuilderId",
	}, nil
}

func (s *kiroRefreshOAuthStub) BuildAccountCredentials(info *service.KiroTokenInfo) map[string]any {
	return map[string]any{
		"access_token":  info.AccessToken,
		"refresh_token": info.RefreshToken,
		"expires_at":    info.ExpiresAt,
		"auth_method":   info.AuthMethod,
		"provider":      info.Provider,
	}
}

func TestRefreshSingleAccountRoutesKiroThroughKiroOAuthService(t *testing.T) {
	adminSvc := &grokRefreshAdminService{stubAdminService: newStubAdminService()}
	kiroOAuth := &kiroRefreshOAuthStub{}
	handler := NewAccountHandler(adminSvc, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	handler.kiroOAuthService = kiroOAuth
	account := &service.Account{
		ID:       4228,
		Platform: service.PlatformAnthropic,
		Type:     service.AccountTypeKiro,
		Credentials: map[string]any{
			"access_token":  "old-access",
			"refresh_token": "old-refresh",
			"profile_arn":   "arn:aws:codewhisperer:us-east-1:123:profile/test",
		},
	}

	updated, warning, err := handler.refreshSingleAccount(context.Background(), account)

	require.NoError(t, err)
	require.Empty(t, warning)
	require.Equal(t, 1, kiroOAuth.calls)
	require.Same(t, account, kiroOAuth.account)
	require.Equal(t, "new-access", adminSvc.updatedCredentials["access_token"])
	require.Equal(t, "new-refresh", adminSvc.updatedCredentials["refresh_token"])
	require.Equal(t, "arn:aws:codewhisperer:us-east-1:123:profile/test", adminSvc.updatedCredentials["profile_arn"])
	require.Equal(t, adminSvc.updatedCredentials, updated.Credentials)
}
