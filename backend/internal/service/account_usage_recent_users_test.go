//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type recentAccountUsersUsageRepo struct {
	UsageLogRepository
	users []RecentAccountUser
}

func (r *recentAccountUsersUsageRepo) GetRecentAccountUsers(ctx context.Context, accountID int64, minutes int) ([]RecentAccountUser, error) {
	return r.users, nil
}

func (r *recentAccountUsersUsageRepo) GetAccountUsersByTimeRange(ctx context.Context, accountID int64, startTime, endTime time.Time) ([]RecentAccountUser, error) {
	return r.users, nil
}

type recentAccountUsersUserRepo struct {
	UserRepository
	users map[int64]*User
}

func (r *recentAccountUsersUserRepo) GetByID(ctx context.Context, id int64) (*User, error) {
	return r.users[id], nil
}

func TestAccountUsageServiceGetRecentAccountUsersMergesRealtimeConcurrency(t *testing.T) {
	now := time.Now()
	usageRepo := &recentAccountUsersUsageRepo{
		users: []RecentAccountUser{
			{UserID: 1001, Email: "active-in-log@example.com", Requests: 3, LastUsedAt: now.Add(-time.Minute)},
			{UserID: 1003, Email: "historical@example.com", Requests: 1, LastUsedAt: now.Add(-2 * time.Minute)},
		},
	}
	concurrency := NewConcurrencyService(&stubConcurrencyCacheForTest{
		activeUserConcurrency: map[int64]int{1001: 2, 1002: 1},
	})
	userRepo := &recentAccountUsersUserRepo{
		users: map[int64]*User{
			1002: {ID: 1002, Email: "redis-only@example.com"},
		},
	}
	svc := &AccountUsageService{usageLogRepo: usageRepo}
	svc.SetConcurrencyService(concurrency)
	svc.SetUserRepository(userRepo)

	users, err := svc.GetRecentAccountUsers(context.Background(), 42, 5)
	require.NoError(t, err)
	require.Len(t, users, 3)

	require.Equal(t, int64(1001), users[0].UserID)
	require.Equal(t, int64(2), users[0].CurrentRequests)
	require.Equal(t, int64(1002), users[1].UserID)
	require.Equal(t, "redis-only@example.com", users[1].Email)
	require.Equal(t, int64(1), users[1].CurrentRequests)
	require.Equal(t, int64(1003), users[2].UserID)
	require.Zero(t, users[2].CurrentRequests)
}
