//go:build unit

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestOpenAIAccountRuntimeCacheSharesAndExtendsBlocks(t *testing.T) {
	server := miniredis.RunT(t)
	clientA := redis.NewClient(&redis.Options{Addr: server.Addr()})
	clientB := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() {
		_ = clientA.Close()
		_ = clientB.Close()
	})
	cacheA := NewOpenAIAccountRuntimeCache(clientA)
	cacheB := NewOpenAIAccountRuntimeCache(clientB)
	ctx := context.Background()
	shortUntil := time.Now().Add(time.Minute).Truncate(time.Millisecond)
	longUntil := shortUntil.Add(time.Minute)

	stored, err := cacheA.SetOpenAIAccountRuntimeBlock(ctx, 91, longUntil)
	require.NoError(t, err)
	require.WithinDuration(t, longUntil, stored, time.Millisecond)
	stored, err = cacheB.SetOpenAIAccountRuntimeBlock(ctx, 91, shortUntil)
	require.NoError(t, err)
	require.WithinDuration(t, longUntil, stored, time.Millisecond, "a shorter cross-instance block must not replace the longer deadline")

	visible, err := cacheB.GetOpenAIAccountRuntimeBlock(ctx, 91)
	require.NoError(t, err)
	require.WithinDuration(t, longUntil, visible, time.Millisecond)
	require.NoError(t, cacheA.ClearOpenAIAccountRuntimeBlock(ctx, 91))
	visible, err = cacheB.GetOpenAIAccountRuntimeBlock(ctx, 91)
	require.NoError(t, err)
	require.True(t, visible.IsZero())
}

func TestOpenAIAccountRuntimeCacheSharesStormWindow(t *testing.T) {
	server := miniredis.RunT(t)
	clientA := redis.NewClient(&redis.Options{Addr: server.Addr()})
	clientB := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() {
		_ = clientA.Close()
		_ = clientB.Close()
	})
	cacheA := NewOpenAIAccountRuntimeCache(clientA)
	cacheB := NewOpenAIAccountRuntimeCache(clientB)
	ctx := context.Background()

	count, err := cacheA.IncrementOpenAIOAuth429Storm(ctx, 10*time.Second)
	require.NoError(t, err)
	require.Equal(t, int64(1), count)
	count, err = cacheB.IncrementOpenAIOAuth429Storm(ctx, 10*time.Second)
	require.NoError(t, err)
	require.Equal(t, int64(2), count)
	count, err = cacheA.GetOpenAIOAuth429StormCount(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(2), count)

	server.FastForward(11 * time.Second)
	count, err = cacheB.GetOpenAIOAuth429StormCount(ctx)
	require.NoError(t, err)
	require.Zero(t, count)
}
