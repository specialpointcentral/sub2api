//go:build integration

package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/kirocooldown"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"
)

const kiroCooldownRedisImageTag = "redis:8.4-alpine"

// kiroCooldownRetryTransient retries only transient store/transport failures on the
// fresh per-test Redis container (Docker contention on loaded CI runners trips the
// store's short Redis timeout). Assertion outcomes are never retried: a deterministic
// logic error fails every attempt and still fails the test.
func kiroCooldownRetryTransient[T any](t *testing.T, fn func() (T, error)) (T, error) {
	t.Helper()
	var result T
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		if result, err = fn(); err == nil {
			return result, nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	return result, err
}

func TestRedisKiroCooldownStoreSharesCooldownAcrossInstances(t *testing.T) {
	ctx := context.Background()
	rdb := startKiroCooldownRedis(t, ctx)
	storeA := kirocooldown.NewStore(rdb)
	storeB := kirocooldown.NewStore(rdb)

	cooldown, err := storeA.Mark429(ctx, "token-shared")
	require.NoError(t, err)
	require.Equal(t, time.Minute, cooldown)

	wait, err := storeB.ReserveRequest(ctx, "token-shared")
	require.Zero(t, wait)
	require.Error(t, err)
	require.Contains(t, err.Error(), kirocooldown.CooldownReason429)

	require.NoError(t, storeB.MarkSuccess(ctx, "token-shared"))

	wait, err = storeA.ReserveRequest(ctx, "token-shared")
	require.NoError(t, err)
	require.GreaterOrEqual(t, wait, 0*time.Second)
}

func TestRedisKiroCooldownStoreSharesReservationAcrossInstances(t *testing.T) {
	ctx := context.Background()
	rdb := startKiroCooldownRedis(t, ctx)
	storeA := kirocooldown.NewStore(rdb)
	storeB := kirocooldown.NewStore(rdb)

	callStart := time.Now()
	wait, err := kiroCooldownRetryTransient(t, func() (time.Duration, error) {
		return storeA.ReserveRequest(ctx, "token-rate")
	})
	require.NoError(t, err)
	require.Zero(t, wait)
	slotA, err := rdb.HGet(ctx, kirocooldown.RedisKey("token-rate"), "last_request_ms").Int64()
	require.NoError(t, err)

	wait, err = kiroCooldownRetryTransient(t, func() (time.Duration, error) {
		return storeB.ReserveRequest(ctx, "token-rate")
	})
	elapsed := time.Since(callStart)
	require.NoError(t, err)
	// Instance B must observe instance A's reserved slot and queue behind it.
	// The store picks a random 1-2s spacing per call, so a positive wait is only
	// guaranteed when the two script executions land within the minimum spacing;
	// on a stalled CI runner (server-side gap >= spacing) a zero wait is the
	// correct outcome, never a store bug. The slot ordering check below is the
	// unconditional sharing assertion.
	if elapsed < kirocooldown.MinRequestInterval {
		require.Greater(t, wait, 0*time.Millisecond)
	}
	require.LessOrEqual(t, wait, kirocooldown.MaxRequestInterval)
	slotB, err := rdb.HGet(ctx, kirocooldown.RedisKey("token-rate"), "last_request_ms").Int64()
	require.NoError(t, err)
	require.Greater(t, slotB, slotA, "instance B must observe and extend instance A's reserved slot")
}

func TestRedisKiroCooldownStoreSharesSuspendedStateAcrossInstances(t *testing.T) {
	ctx := context.Background()
	rdb := startKiroCooldownRedis(t, ctx)
	storeA := kirocooldown.NewStore(rdb)
	storeB := kirocooldown.NewStore(rdb)

	cooldown, err := storeA.MarkSuspended(ctx, "token-suspended")
	require.NoError(t, err)
	require.Equal(t, kirocooldown.LongCooldown, cooldown)

	wait, err := storeB.ReserveRequest(ctx, "token-suspended")
	require.Zero(t, wait)
	require.Error(t, err)
	require.Contains(t, err.Error(), kirocooldown.CooldownReasonSuspended)
}

func TestRedisKiroCooldownStoreSuspendedResetsFailCount(t *testing.T) {
	ctx := context.Background()
	rdb := startKiroCooldownRedis(t, ctx)
	store := kirocooldown.NewStore(rdb)

	_, err := store.Mark429(ctx, "token-reset")
	require.NoError(t, err)
	_, err = store.Mark429(ctx, "token-reset")
	require.NoError(t, err)

	cooldown, err := store.MarkSuspended(ctx, "token-reset")
	require.NoError(t, err)
	require.Equal(t, kirocooldown.LongCooldown, cooldown)

	cooldown, err = store.Mark429(ctx, "token-reset")
	require.NoError(t, err)
	require.Equal(t, time.Minute, cooldown)
}

func TestRedisKiroCooldownStoreReserveDifferentTokenIgnoresOldCooldown(t *testing.T) {
	ctx := context.Background()
	rdb := startKiroCooldownRedis(t, ctx)
	store := kirocooldown.NewStore(rdb)

	_, err := kiroCooldownRetryTransient(t, func() (time.Duration, error) {
		return store.Mark429(ctx, "token-old")
	})
	require.NoError(t, err)

	wait, err := kiroCooldownRetryTransient(t, func() (time.Duration, error) {
		return store.ReserveRequest(ctx, "token-new")
	})
	require.NoError(t, err)
	require.Zero(t, wait)
}

func TestRedisKiroCooldownStoreUsesExpectedTTLs(t *testing.T) {
	ctx := context.Background()
	rdb := startKiroCooldownRedis(t, ctx)
	store := kirocooldown.NewStore(rdb)

	_, err := store.ReserveRequest(ctx, "token-ttl-active")
	require.NoError(t, err)
	activeTTL, err := rdb.PTTL(ctx, kirocooldown.RedisKey("token-ttl-active")).Result()
	require.NoError(t, err)
	require.Greater(t, activeTTL, 0*time.Second)
	require.LessOrEqual(t, activeTTL, kirocooldown.ActiveTTL())

	_, err = store.MarkSuspended(ctx, "token-ttl-state")
	require.NoError(t, err)
	stateTTL, err := rdb.PTTL(ctx, kirocooldown.RedisKey("token-ttl-state")).Result()
	require.NoError(t, err)
	require.Greater(t, stateTTL, 24*time.Hour)
	require.LessOrEqual(t, stateTTL, kirocooldown.StateTTL())
}

func startKiroCooldownRedis(t *testing.T, ctx context.Context) *redis.Client {
	t.Helper()
	ensureKiroCooldownDockerAvailable(t)

	redisContainer, err := tcredis.Run(ctx, kiroCooldownRedisImageTag)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = redisContainer.Terminate(ctx)
	})

	host, err := redisContainer.Host(ctx)
	require.NoError(t, err)
	port, err := redisContainer.MappedPort(ctx, "6379/tcp")
	require.NoError(t, err)

	rdb := redis.NewClient(&redis.Options{
		Addr: fmt.Sprintf("%s:%d", host, port.Int()),
		DB:   0,
	})
	require.NoError(t, rdb.Ping(ctx).Err())
	t.Cleanup(func() {
		_ = rdb.Close()
	})
	return rdb
}

func ensureKiroCooldownDockerAvailable(t *testing.T) {
	t.Helper()
	if kiroCooldownDockerAvailable() {
		return
	}
	t.Skip("Docker 未启用，跳过依赖 testcontainers 的 Kiro cooldown 集成测试")
}

func kiroCooldownDockerAvailable() bool {
	if os.Getenv("DOCKER_HOST") != "" {
		return true
	}

	socketCandidates := []string{
		"/var/run/docker.sock",
		filepath.Join(os.Getenv("XDG_RUNTIME_DIR"), "docker.sock"),
		filepath.Join(kiroCooldownUserHomeDir(), ".docker", "run", "docker.sock"),
		filepath.Join(kiroCooldownUserHomeDir(), ".docker", "desktop", "docker.sock"),
		filepath.Join("/run/user", strconv.Itoa(os.Getuid()), "docker.sock"),
	}

	for _, socket := range socketCandidates {
		if socket == "" {
			continue
		}
		if _, err := os.Stat(socket); err == nil {
			return true
		}
	}
	return false
}

func kiroCooldownUserHomeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home
}
