package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestLiveLeaseReplacesRegularSlotsAndCountsTowardLimits(t *testing.T) {
	redisServer := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	regular := NewConcurrencyCache(client, 15, 900)
	live, ok := regular.(service.LiveConcurrencyCache)
	require.True(t, ok)
	ctx := context.Background()

	accountAcquired, err := regular.AcquireAccountSlot(ctx, 10, 1, "regular-account")
	require.NoError(t, err)
	require.True(t, accountAcquired)
	userAcquired, err := regular.AcquireUserSlot(ctx, 20, 1, "regular-user")
	require.NoError(t, err)
	require.True(t, userAcquired)

	acquired, err := live.AcquireLiveLease(ctx, 10, 1, 20, 1, 30, "live-lease", true)
	require.NoError(t, err)
	require.True(t, acquired)
	require.NoError(t, regular.ReleaseAccountSlot(ctx, 10, "regular-account"))
	require.NoError(t, regular.ReleaseUserSlot(ctx, 20, "regular-user"))

	accountCount, err := regular.GetAccountConcurrency(ctx, 10)
	require.NoError(t, err)
	require.Equal(t, 1, accountCount)
	userCount, err := regular.GetUserConcurrency(ctx, 20)
	require.NoError(t, err)
	require.Equal(t, 1, userCount)
	accountAcquired, err = regular.AcquireAccountSlot(ctx, 10, 1, "ordinary-blocked")
	require.NoError(t, err)
	require.False(t, accountAcquired)

	refreshed, err := live.RefreshLiveLease(ctx, 10, 20, 30, "live-lease")
	require.NoError(t, err)
	require.True(t, refreshed)
	require.NoError(t, live.ReleaseLiveLease(ctx, 10, 20, 30, "live-lease"))
	accountAcquired, err = regular.AcquireAccountSlot(ctx, 10, 1, "ordinary-allowed")
	require.NoError(t, err)
	require.True(t, accountAcquired)
}

func TestLiveLeaseExpiresWithoutRefresh(t *testing.T) {
	redisServer := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	regular := NewConcurrencyCache(client, 15, 900)
	live, ok := regular.(service.LiveConcurrencyCache)
	require.True(t, ok)
	ctx := context.Background()

	acquired, err := live.AcquireLiveLease(ctx, 10, 1, 20, 1, 30, "expired-live", false)
	require.NoError(t, err)
	require.True(t, acquired)

	redisServer.FastForward(61 * time.Second)
	acquired, err = regular.AcquireAccountSlot(ctx, 10, 1, "ordinary-after-expiry")
	require.NoError(t, err)
	require.True(t, acquired)
	refreshed, err := live.RefreshLiveLease(ctx, 10, 20, 30, "expired-live")
	require.NoError(t, err)
	require.False(t, refreshed)
}

func TestOpenAIRequestPacingRejectsOverLimitReservationWithoutAdvancingQueue(t *testing.T) {
	redisServer := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	regular := NewConcurrencyCache(client, 15, 900)
	pacing, ok := regular.(service.OpenAIRequestPacingCache)
	require.True(t, ok)
	ctx := context.Background()
	deadline := time.Now().Add(30 * time.Second)

	const interval = 50 * time.Millisecond
	wait, err := pacing.ReserveOpenAIOAuthStart(ctx, 77, interval, deadline, "owner-1")
	require.NoError(t, err)
	require.Zero(t, wait)
	wait, err = pacing.ReserveOpenAIOAuthStart(ctx, 77, interval, deadline, "owner-2")
	require.NoError(t, err)
	require.InDelta(t, interval.Milliseconds(), wait.Milliseconds(), 5)
	firstWait := wait
	wait, err = pacing.ReserveOpenAIOAuthStart(ctx, 77, interval, deadline, "owner-3")
	require.NoError(t, err)
	require.InDelta(t, firstWait.Milliseconds(), wait.Milliseconds(), 5, "waiting callers must not advance the shared gate")

	time.Sleep(75 * time.Millisecond)
	wait, err = pacing.ReserveOpenAIOAuthStart(ctx, 77, interval, deadline, "owner-4")
	require.NoError(t, err)
	require.Zero(t, wait, "the next eligible caller should atomically advance the gate")

	rolledBack, err := pacing.RollbackOpenAIOAuthStart(ctx, 77, "owner-4")
	require.NoError(t, err)
	require.True(t, rolledBack)
	wait, err = pacing.ReserveOpenAIOAuthStart(ctx, 77, interval, deadline, "owner-5")
	require.NoError(t, err)
	require.Zero(t, wait, "rolling back the current owner must reopen the gate")
	rolledBack, err = pacing.RollbackOpenAIOAuthStart(ctx, 77, "owner-4")
	require.NoError(t, err)
	require.False(t, rolledBack, "a stale owner must not delete a newer admission")

	rolledBack, err = pacing.RollbackOpenAIOAuthStart(ctx, 88, "canceled-before-admit")
	require.NoError(t, err)
	require.False(t, rolledBack)
	wait, err = pacing.ReserveOpenAIOAuthStart(ctx, 88, interval, deadline, "canceled-before-admit")
	require.NoError(t, err)
	require.Negative(t, wait, "a cancellation tombstone must reject a delayed admission")
	wait, err = pacing.ReserveOpenAIOAuthStart(ctx, 88, interval, deadline, "replacement-owner")
	require.NoError(t, err)
	require.Zero(t, wait, "canceling one owner must not block a replacement request")
}

func TestOpenAIQuotaProbeScheduleClaimsAreSharedAcrossInstances(t *testing.T) {
	redisServer := miniredis.RunT(t)
	clientA := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	clientB := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	cacheA, ok := NewConcurrencyCache(clientA, 15, 900).(service.OpenAIQuotaProbeScheduleCache)
	require.True(t, ok)
	cacheB, ok := NewConcurrencyCache(clientB, 15, 900).(service.OpenAIQuotaProbeScheduleCache)
	require.True(t, ok)
	ctx := context.Background()
	const interval = 50 * time.Millisecond

	claimed, err := cacheA.TryClaimOpenAIQuotaProbe(ctx, 77, interval, "10:0.25", time.Time{})
	require.NoError(t, err)
	require.True(t, claimed)
	claimed, err = cacheB.TryClaimOpenAIQuotaProbe(ctx, 77, interval, "10:0.25", time.Time{})
	require.NoError(t, err)
	require.False(t, claimed, "a second instance must observe the shared deadline")

	claimed, err = cacheB.TryClaimOpenAIQuotaProbe(ctx, 78, interval, "10:0.25", time.Time{})
	require.NoError(t, err)
	require.True(t, claimed, "different accounts must remain isolated")
	claimed, err = cacheB.TryClaimOpenAIQuotaProbe(ctx, 77, interval, "20:0.25", time.Time{})
	require.NoError(t, err)
	require.True(t, claimed, "a changed schedule must replace the old deadline immediately")
	claimed, err = cacheA.TryClaimOpenAIQuotaProbe(ctx, 77, interval, "20:0.25", time.Time{})
	require.NoError(t, err)
	require.False(t, claimed)

	time.Sleep(75 * time.Millisecond)
	claimed, err = cacheA.TryClaimOpenAIQuotaProbe(ctx, 77, interval, "20:0.25", time.Time{})
	require.NoError(t, err)
	require.True(t, claimed, "an expired deadline must become claimable")

	initialNotBefore := time.Now().Add(interval)
	claimed, err = cacheA.TryClaimOpenAIQuotaProbe(ctx, 79, interval, "10:0.25", initialNotBefore)
	require.NoError(t, err)
	require.False(t, claimed, "a fresh persisted snapshot must initialize without probing")
	claimed, err = cacheB.TryClaimOpenAIQuotaProbe(ctx, 79, interval, "10:0.25", initialNotBefore)
	require.NoError(t, err)
	require.False(t, claimed, "other instances must observe the initialized cold deadline")
	time.Sleep(75 * time.Millisecond)
	claimed, err = cacheB.TryClaimOpenAIQuotaProbe(ctx, 79, interval, "10:0.25", initialNotBefore)
	require.NoError(t, err)
	require.True(t, claimed, "the initialized cold deadline must become claimable")
}
