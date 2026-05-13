//go:build unit

package dto

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAccountFromServiceShallow_QuotaExceeded(t *testing.T) {
	now := time.Now()
	account := &service.Account{
		Status:      service.StatusActive,
		Schedulable: true,
		Type:        service.AccountTypeAPIKey,
		Extra: map[string]any{
			"quota_daily_limit": 10.0,
			"quota_daily_used":  10.0,
			"quota_daily_start": now.Add(-1 * time.Hour).Format(time.RFC3339),
		},
	}

	out := AccountFromServiceShallow(account)
	require.NotNil(t, out)
	require.True(t, out.QuotaExceeded)
	require.NotNil(t, out.QuotaDailyLimit)
	require.NotNil(t, out.QuotaDailyUsed)
	require.Equal(t, 10.0, *out.QuotaDailyLimit)
	require.Equal(t, 10.0, *out.QuotaDailyUsed)
}

func TestAccountFromServiceShallow_QuotaExceededFalseIsExplicit(t *testing.T) {
	now := time.Now()
	account := &service.Account{
		Status:      service.StatusActive,
		Schedulable: true,
		Type:        service.AccountTypeAPIKey,
		Extra: map[string]any{
			"quota_daily_limit": 10.0,
			"quota_daily_used":  10.0,
			"quota_daily_start": now.Add(-25 * time.Hour).Format(time.RFC3339),
		},
	}

	out := AccountFromServiceShallow(account)
	require.NotNil(t, out)
	require.False(t, out.QuotaExceeded)
	require.NotNil(t, out.QuotaDailyUsed)
	require.Equal(t, 10.0, *out.QuotaDailyUsed)

	payload, err := json.Marshal(out)
	require.NoError(t, err)
	require.Contains(t, string(payload), `"quota_exceeded":false`)
}
