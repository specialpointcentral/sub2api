package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type requestPacingTestCache struct {
	ConcurrencyCache
	waits      map[int64]time.Duration
	err        error
	accountIDs []int64
	intervals  []time.Duration
	deadlines  []time.Time
	onReserve  func()
	rollbacks  int
}

func (c *requestPacingTestCache) ReserveOpenAIOAuthStart(ctx context.Context, accountID int64, interval time.Duration, _ time.Time, _ string) (time.Duration, error) {
	c.accountIDs = append(c.accountIDs, accountID)
	c.intervals = append(c.intervals, interval)
	if deadline, ok := ctx.Deadline(); ok {
		c.deadlines = append(c.deadlines, deadline)
	}
	if c.onReserve != nil {
		c.onReserve()
	}
	wait := c.waits[accountID]
	if wait > 0 && wait <= openAIRequestPacingMaxWait {
		delete(c.waits, accountID)
	}
	return wait, c.err
}

func (c *requestPacingTestCache) RollbackOpenAIOAuthStart(_ context.Context, _ int64, _ string) (bool, error) {
	c.rollbacks++
	return true, nil
}

func TestWaitForOpenAIAccountStartWithSettings(t *testing.T) {
	account := &Account{ID: 11, Platform: PlatformOpenAI, Type: AccountTypeOAuth}

	t.Run("disabled keeps oauth and api key legacy behavior", func(t *testing.T) {
		cache := &requestPacingTestCache{}
		svc := &OpenAIGatewayService{concurrencyService: NewConcurrencyService(cache)}
		err := svc.waitForOpenAIAccountStartWithSettings(context.Background(), account, OpenAITrafficShapingSettings{})
		require.NoError(t, err)
		err = svc.waitForOpenAIAccountStartWithSettings(context.Background(), &Account{ID: 12, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, OpenAITrafficShapingSettings{})
		require.NoError(t, err)
		require.Empty(t, cache.accountIDs)
	})

	t.Run("reserves account scoped start and waits", func(t *testing.T) {
		cache := &requestPacingTestCache{waits: map[int64]time.Duration{11: 400 * time.Millisecond}}
		var slept time.Duration
		svc := &OpenAIGatewayService{
			concurrencyService: cacheService(cache),
			openAIRequestPacingSample: func(minimum, maximum time.Duration) time.Duration {
				require.Equal(t, 250*time.Millisecond, minimum)
				require.Equal(t, 750*time.Millisecond, maximum)
				return maximum
			},
			openAIRequestPacingSleep: func(_ context.Context, wait time.Duration) error {
				slept = wait
				return nil
			},
		}
		err := svc.waitForOpenAIAccountStartWithSettings(context.Background(), account, OpenAITrafficShapingSettings{
			RequestPacingEnabled: true, RequestPacingMinIntervalMS: 250, RequestPacingMaxIntervalMS: 750,
		})
		require.NoError(t, err)
		require.Equal(t, []int64{11, 11}, cache.accountIDs)
		require.Equal(t, []time.Duration{750 * time.Millisecond, 750 * time.Millisecond}, cache.intervals)
		require.Equal(t, 400*time.Millisecond, slept)
	})

	t.Run("different accounts remain isolated", func(t *testing.T) {
		cache := &requestPacingTestCache{waits: map[int64]time.Duration{11: time.Millisecond, 22: 2 * time.Millisecond}}
		svc := &OpenAIGatewayService{
			concurrencyService:        cacheService(cache),
			openAIRequestPacingSample: func(_, _ time.Duration) time.Duration { return 500 * time.Millisecond },
			openAIRequestPacingSleep:  func(_ context.Context, _ time.Duration) error { return nil },
		}
		settings := OpenAITrafficShapingSettings{RequestPacingEnabled: true, RequestPacingMinIntervalMS: 250, RequestPacingMaxIntervalMS: 750}
		require.NoError(t, svc.waitForOpenAIAccountStartWithSettings(context.Background(), account, settings))
		require.NoError(t, svc.waitForOpenAIAccountStartWithSettings(context.Background(), &Account{ID: 22, Platform: PlatformOpenAI, Type: AccountTypeOAuth}, settings))
		require.Equal(t, []int64{11, 11, 22, 22}, cache.accountIDs)
	})

	t.Run("redis errors fail open", func(t *testing.T) {
		cache := &requestPacingTestCache{err: errors.New("redis unavailable")}
		svc := &OpenAIGatewayService{concurrencyService: cacheService(cache)}
		err := svc.waitForOpenAIAccountStartWithSettings(context.Background(), account, OpenAITrafficShapingSettings{
			RequestPacingEnabled: true, RequestPacingMinIntervalMS: 250, RequestPacingMaxIntervalMS: 750,
		})
		require.NoError(t, err)
	})

	t.Run("openai api key account reserves pacing gate", func(t *testing.T) {
		cache := &requestPacingTestCache{}
		svc := &OpenAIGatewayService{concurrencyService: cacheService(cache)}
		settings := OpenAITrafficShapingSettings{RequestPacingEnabled: true, RequestPacingMinIntervalMS: 250, RequestPacingMaxIntervalMS: 750}
		require.NoError(t, svc.waitForOpenAIAccountStartWithSettings(context.Background(), &Account{ID: 12, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, settings))
		require.Equal(t, []int64{12}, cache.accountIDs)
	})

	t.Run("non openai accounts remain untouched", func(t *testing.T) {
		cache := &requestPacingTestCache{}
		svc := &OpenAIGatewayService{concurrencyService: cacheService(cache)}
		settings := OpenAITrafficShapingSettings{RequestPacingEnabled: true, RequestPacingMinIntervalMS: 250, RequestPacingMaxIntervalMS: 750}
		require.NoError(t, svc.waitForOpenAIAccountStartWithSettings(context.Background(), &Account{ID: 13, Platform: PlatformGrok, Type: AccountTypeOAuth}, settings))
		require.Empty(t, cache.accountIDs)
	})

	t.Run("wait is context cancellable", func(t *testing.T) {
		cache := &requestPacingTestCache{waits: map[int64]time.Duration{11: time.Second}}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		svc := &OpenAIGatewayService{concurrencyService: cacheService(cache)}
		err := svc.waitForOpenAIAccountStartWithSettings(ctx, account, OpenAITrafficShapingSettings{
			RequestPacingEnabled: true, RequestPacingMinIntervalMS: 250, RequestPacingMaxIntervalMS: 750,
		})
		require.ErrorIs(t, err, context.Canceled)
	})

	t.Run("wait above hard limit is rejected", func(t *testing.T) {
		cache := &requestPacingTestCache{waits: map[int64]time.Duration{11: 31 * time.Second}}
		svc := &OpenAIGatewayService{concurrencyService: cacheService(cache)}
		err := svc.waitForOpenAIAccountStartWithSettings(context.Background(), account, OpenAITrafficShapingSettings{
			RequestPacingEnabled: true, RequestPacingMinIntervalMS: 250, RequestPacingMaxIntervalMS: 750,
		})
		require.ErrorIs(t, err, ErrOpenAIRequestPacingTimeout)
	})

	t.Run("hard deadline covers scheduling overrun before another redis claim", func(t *testing.T) {
		cache := &requestPacingTestCache{waits: map[int64]time.Duration{11: 29 * time.Second}}
		now := time.Date(2026, 8, 23, 0, 0, 0, 0, time.UTC)
		svc := &OpenAIGatewayService{
			concurrencyService: cacheService(cache),
			openAIRequestPacingNow: func() time.Time {
				return now
			},
			openAIRequestPacingSleep: func(_ context.Context, _ time.Duration) error {
				now = now.Add(31 * time.Second)
				return nil
			},
		}
		err := svc.waitForOpenAIAccountStartWithSettings(context.Background(), account, OpenAITrafficShapingSettings{
			RequestPacingEnabled: true, RequestPacingMinIntervalMS: 250, RequestPacingMaxIntervalMS: 750,
		})
		require.ErrorIs(t, err, ErrOpenAIRequestPacingTimeout)
		require.Len(t, cache.accountIDs, 1, "deadline overrun must not make another Redis claim")
		require.Len(t, cache.deadlines, 1, "Redis claim must inherit the pacing deadline")
	})

	t.Run("admission returned after the hard deadline is rolled back", func(t *testing.T) {
		now := time.Date(2026, 8, 23, 0, 0, 0, 0, time.UTC)
		cache := &requestPacingTestCache{
			onReserve: func() { now = now.Add(31 * time.Second) },
		}
		svc := &OpenAIGatewayService{
			concurrencyService:     cacheService(cache),
			openAIRequestPacingNow: func() time.Time { return now },
		}
		err := svc.waitForOpenAIAccountStartWithSettings(context.Background(), account, OpenAITrafficShapingSettings{
			RequestPacingEnabled: true, RequestPacingMinIntervalMS: 250, RequestPacingMaxIntervalMS: 750,
		})
		require.ErrorIs(t, err, ErrOpenAIRequestPacingTimeout)
		require.Equal(t, 1, cache.rollbacks, "a late admission must release only its own gate")
	})
}

func cacheService(cache ConcurrencyCache) *ConcurrencyService {
	return NewConcurrencyService(cache)
}
