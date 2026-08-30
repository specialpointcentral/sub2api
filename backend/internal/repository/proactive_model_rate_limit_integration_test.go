//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type staticModelRateLimitRuleStore struct{ rules []service.ModelRateLimitRule }

func (s staticModelRateLimitRuleStore) ListModelRateLimitRules(context.Context, *int64) ([]service.ModelRateLimitRule, error) {
	return append([]service.ModelRateLimitRule(nil), s.rules...), nil
}

func (s staticModelRateLimitRuleStore) ReplaceModelRateLimitRules(_ context.Context, _ *int64, rules []service.ModelRateLimitRule) ([]service.ModelRateLimitRule, error) {
	return append([]service.ModelRateLimitRule(nil), rules...), nil
}

func TestProactiveModelRateLimitRedisRollingRPMSharedAndIsolated(t *testing.T) {
	rdb := testRedis(t)
	first := NewProactiveModelRateLimitCache(rdb, 15*time.Minute)
	second := NewProactiveModelRateLimitCache(rdb, 15*time.Minute)
	ctx := context.Background()
	base := int64(1_800_000_000_000)

	for i, cache := range []*proactiveModelRateLimitCache{first, second} {
		result, err := cache.admitAtRule(ctx, 7, "gpt-5.6-luna", 0, 2, "rpm-"+string(rune('a'+i)), base+int64(i), 0)
		require.NoError(t, err)
		require.True(t, result.Allowed)
	}
	denied, err := first.admitAtRule(ctx, 7, "gpt-5.6-luna", 0, 2, "rpm-c", base+1000, 0)
	require.NoError(t, err)
	require.False(t, denied.Allowed)
	require.Equal(t, service.ModelRateLimitDimensionRPM, denied.Dimension)
	require.Equal(t, 59, denied.RetryAfterSeconds)

	otherUser, err := second.admitAtRule(ctx, 8, "gpt-5.6-luna", 0, 2, "other-user", base+1000, 0)
	require.NoError(t, err)
	require.True(t, otherUser.Allowed)
	otherModel, err := second.admitAtRule(ctx, 7, "gpt-5.6-terra", 0, 2, "other-model", base+1000, 0)
	require.NoError(t, err)
	require.True(t, otherModel.Allowed)

	afterWindow, err := second.admitAtRule(ctx, 7, "gpt-5.6-luna", 0, 2, "rpm-d", base+60_001, 0)
	require.NoError(t, err)
	require.True(t, afterWindow.Allowed)
}

func TestProactiveModelRateLimitRedisConcurrencyAtomicReleaseAndIsolation(t *testing.T) {
	rdb := testRedis(t)
	first := NewProactiveModelRateLimitCache(rdb, 15*time.Minute)
	second := NewProactiveModelRateLimitCache(rdb, 15*time.Minute)
	ctx := context.Background()

	one, err := first.AdmitModelRateLimit(ctx, 42, "claude-opus-4-1", 1, 0, "concurrency-a", 1)
	require.NoError(t, err)
	require.True(t, one.Allowed)
	two, err := second.AdmitModelRateLimit(ctx, 42, "claude-opus-4-1", 1, 0, "concurrency-b", 1)
	require.NoError(t, err)
	require.False(t, two.Allowed)
	require.Equal(t, service.ModelRateLimitDimensionConcurrency, two.Dimension)
	require.Equal(t, 1, two.RetryAfterSeconds)

	independent, err := second.AdmitModelRateLimit(ctx, 42, "claude-sonnet-4-1", 1, 0, "concurrency-c", 1)
	require.NoError(t, err)
	require.True(t, independent.Allowed)
	require.NoError(t, second.ReleaseModelRateLimit(ctx, 42, "claude-sonnet-4-1", "concurrency-c"))

	require.NoError(t, first.ReleaseModelRateLimit(ctx, 42, "claude-opus-4-1", "concurrency-a"))
	limiter := service.NewProactiveModelRateLimitService(
		staticModelRateLimitRuleStore{rules: []service.ModelRateLimitRule{{ID: 1, ModelPattern: "claude-opus-4-1", NormalizedPattern: "claude-opus-4-1", ConcurrencyLimit: 1}}},
		first,
		first,
		nil,
	)
	cancelCtx, cancel := context.WithCancel(context.Background())
	admission, err := limiter.Admit(cancelCtx, 42, "claude-opus-4-1")
	require.NoError(t, err)
	require.True(t, admission.Allowed)
	require.NotNil(t, admission.Release)
	stopRelease := context.AfterFunc(cancelCtx, admission.Release)
	cancel()
	defer stopRelease()
	require.Eventually(t, func() bool {
		counts, countErr := second.GetModelRateLimitUsageBatch(ctx, 42, []string{"claude-opus-4-1"})
		return countErr == nil && counts["claude-opus-4-1"].Concurrency == 0
	}, 10*time.Second, 10*time.Millisecond)

	restored, err := second.AdmitModelRateLimit(ctx, 42, "claude-opus-4-1", 1, 0, "concurrency-b", 1)
	require.NoError(t, err)
	require.True(t, restored.Allowed)
	require.NoError(t, second.ReleaseModelRateLimit(ctx, 42, "claude-opus-4-1", "concurrency-b"))
}
