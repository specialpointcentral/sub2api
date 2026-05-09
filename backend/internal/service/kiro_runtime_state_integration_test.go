//go:build integration

package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/kirocooldown"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"
)

const kiroCooldownRedisImageTag = "redis:8.4-alpine"

// isKiroCooldownTransientStoreError reports whether err is a transport-class failure
// (network error, EOF, connection reset, deadline). Only these may be retried:
// domain errors such as an active cooldown verdict are logic results, not flakes.
func isKiroCooldownTransientStoreError(err error) bool {
	if err == nil {
		return false
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "connection reset by peer") || strings.Contains(msg, "broken pipe")
}

// reserveKiroCooldownWithReadback retries ReserveRequest only after a successful
// readback proves the previous non-idempotent script did not land. If the write
// outcome is ambiguous (both operation and readback fail), it returns the error
// instead of moving the slot a second time.
func reserveKiroCooldownWithReadback(ctx context.Context, rdb *redis.Client, store *kirocooldown.Store, token string, priorSlot *int64) (time.Duration, int64, error) {
	key := kirocooldown.RedisKey(token)
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		wait, reserveErr := store.ReserveRequest(ctx, token)
		if reserveErr == nil {
			slot, readErr := rdb.HGet(ctx, key, "last_request_ms").Int64()
			return wait, slot, readErr
		}
		if !isKiroCooldownTransientStoreError(reserveErr) {
			return 0, 0, reserveErr
		}
		lastErr = reserveErr

		slot, readErr := rdb.HGet(ctx, key, "last_request_ms").Int64()
		switch {
		case readErr == nil && (priorSlot == nil || slot > *priorSlot):
			// The script landed and only its response was lost. Accept the durable
			// slot without reapplying the reservation.
			return 0, slot, nil
		case errors.Is(readErr, redis.Nil):
			// No reservation exists, so replaying is proven safe.
		case readErr == nil && priorSlot != nil && slot == *priorSlot:
			// The previous slot is unchanged, so replaying is proven safe.
		case readErr != nil:
			return 0, 0, errors.Join(reserveErr, fmt.Errorf("reservation readback failed: %w", readErr))
		default:
			return 0, 0, fmt.Errorf("reservation readback moved backward: previous=%d current=%d: %w", *priorSlot, slot, reserveErr)
		}
		if attempt < 2 {
			time.Sleep(250 * time.Millisecond)
		}
	}
	return 0, 0, fmt.Errorf("reserve request retries exhausted: %w", lastErr)
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

	wait, slotA, err := reserveKiroCooldownWithReadback(ctx, rdb, storeA, "token-rate", nil)
	require.NoError(t, err)
	require.Zero(t, wait)

	wait, slotB, err := reserveKiroCooldownWithReadback(ctx, rdb, storeB, "token-rate", &slotA)
	require.NoError(t, err)
	require.LessOrEqual(t, wait, kirocooldown.MaxRequestInterval)
	// Unconditional sharing invariant: B's next slot trails A's by at least the
	// minimum request spacing (server-side slot = A's slot + random 1-2s interval,
	// or the current server time after a stalled-gap — both >= MinRequestInterval).
	// This does not depend on wall-clock luck between the two script executions.
	require.GreaterOrEqual(t, slotB-slotA, kirocooldown.MinRequestInterval.Milliseconds(),
		"instance B must observe instance A's reserved slot and queue behind it")
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

	// Writes are only retried while the server proves they never landed; a lost
	// response may still have applied server-side, and blind retries would double
	// Mark429's fail count or make ReserveRequest report a phantom wait.
	oldKey := kirocooldown.RedisKey("token-old")
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		_, err = store.Mark429(ctx, "token-old")
		if err == nil || !isKiroCooldownTransientStoreError(err) {
			break
		}
		exists, existsErr := rdb.HExists(ctx, oldKey, "fail_count").Result()
		if existsErr == nil && exists {
			err = nil
			break
		}
		if existsErr != nil {
			err = errors.Join(err, fmt.Errorf("mark 429 readback failed: %w", existsErr))
			break
		}
		if attempt < 2 {
			time.Sleep(250 * time.Millisecond)
		}
	}
	require.NoError(t, err)
	// The cooldown on the old token must actually be in effect for this test to
	// mean anything.
	cooldownUntil, err := rdb.HGet(ctx, oldKey, "cooldown_until_ms").Int64()
	require.NoError(t, err)
	require.Greater(t, cooldownUntil, int64(0))

	newKey := kirocooldown.RedisKey("token-new")
	wait, _, err := reserveKiroCooldownWithReadback(ctx, rdb, store, "token-new", nil)
	require.NoError(t, err)
	require.Zero(t, wait)
	// The new token must not inherit any cooldown state from the old one.
	failCount, err := rdb.HGet(ctx, newKey, "fail_count").Int64()
	require.True(t, err == redis.Nil || (err == nil && failCount == 0),
		"token-new must not carry fail state (err=%v, fail_count=%d)", err, failCount)
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
