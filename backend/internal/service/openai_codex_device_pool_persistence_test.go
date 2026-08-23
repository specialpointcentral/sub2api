package service

import (
	"context"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type codexDevicePoolConflictRepo struct {
	AccountRepository
	mu          sync.Mutex
	current     any
	calls       int
	finalWinner any
}

func (r *codexDevicePoolConflictRepo) CompareAndSwapAccountExtraValue(_ context.Context, _ int64, _ string, _, _ any) (any, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	if r.finalWinner != nil && r.calls == codexDevicePoolCASMaxAttempts {
		r.current = r.finalWinner
	}
	if r.current == nil {
		r.current = codexDevicePoolState{Version: 1, NextSlot: 1, Slots: []codexDeviceSlot{}}
	}
	return r.current, false, nil
}

func TestEnsureCodexDevicePoolSlotFinalConflictRecomputesFromWinner(t *testing.T) {
	winnerSlot := codexDeviceSlot{ID: 1, Platform: codexUAPersonaWindows, Sandbox: "none", CreatedFor: "99"}
	repo := &codexDevicePoolConflictRepo{finalWinner: codexDevicePoolState{
		Version: 1, NextSlot: 2, Slots: []codexDeviceSlot{winnerSlot},
	}}
	svc := &OpenAIGatewayService{accountRepo: repo}
	account := newTestOAuthAccount(41, nil)

	got, err := svc.ensureCodexDevicePoolSlot(
		context.Background(), account,
		codexUAPersonaSelection{Platform: codexUAPersonaWindows, Sandbox: "none"},
		99, "", codexDevicePlatformQuotas{Windows: 1},
	)

	require.NoError(t, err)
	require.Equal(t, winnerSlot, got)
	require.Equal(t, codexDevicePoolCASMaxAttempts, repo.calls)
	metrics := svc.SnapshotCodexDevicePoolMetrics()
	require.Equal(t, int64(codexDevicePoolCASMaxAttempts), metrics.ConflictTotal)
	require.Equal(t, int64(codexDevicePoolCASMaxAttempts-1), metrics.RetryTotal)
	require.Zero(t, metrics.ExhaustedTotal)
}

func TestEnsureCodexDevicePoolSlotRetryBackoffHonorsContext(t *testing.T) {
	repo := &codexDevicePoolConflictRepo{}
	svc := &OpenAIGatewayService{accountRepo: repo}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := svc.ensureCodexDevicePoolSlot(
		ctx, newTestOAuthAccount(42, nil),
		codexUAPersonaSelection{Platform: codexUAPersonaMac, Sandbox: "seatbelt"},
		7, "", codexDevicePlatformQuotas{Mac: 1},
	)

	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 1, repo.calls)
	metrics := svc.SnapshotCodexDevicePoolMetrics()
	require.Equal(t, int64(1), metrics.ConflictTotal)
	require.Equal(t, int64(1), metrics.RetryTotal)
	require.Zero(t, metrics.ExhaustedTotal)
}

func TestEnsureCodexDevicePoolSlotRecordsExhaustionAfterFinalWinnerStillNeedsWrite(t *testing.T) {
	repo := &codexDevicePoolConflictRepo{}
	svc := &OpenAIGatewayService{accountRepo: repo}

	_, err := svc.ensureCodexDevicePoolSlot(
		context.Background(), newTestOAuthAccount(45, nil),
		codexUAPersonaSelection{Platform: codexUAPersonaMac, Sandbox: "seatbelt"},
		8, "", codexDevicePlatformQuotas{Mac: 1},
	)

	require.ErrorContains(t, err, "did not converge")
	require.Equal(t, codexDevicePoolCASMaxAttempts, repo.calls)
	metrics := svc.SnapshotCodexDevicePoolMetrics()
	require.Equal(t, int64(codexDevicePoolCASMaxAttempts), metrics.ConflictTotal)
	require.Equal(t, int64(codexDevicePoolCASMaxAttempts-1), metrics.RetryTotal)
	require.Equal(t, int64(1), metrics.ExhaustedTotal)
}

type codexDevicePoolConcurrentRepo struct {
	AccountRepository
	wantInitial int
	readyOnce   sync.Once
	ready       chan struct{}
	arrivalMu   sync.Mutex
	arrivals    int
	mu          sync.Mutex
	current     any
}

func newCodexDevicePoolConcurrentRepo(wantInitial int) *codexDevicePoolConcurrentRepo {
	return &codexDevicePoolConcurrentRepo{wantInitial: wantInitial, ready: make(chan struct{})}
}

func (r *codexDevicePoolConcurrentRepo) CompareAndSwapAccountExtraValue(_ context.Context, _ int64, _ string, expected, replacement any) (any, bool, error) {
	if expected == nil {
		r.arrivalMu.Lock()
		r.arrivals++
		if r.arrivals == r.wantInitial {
			r.readyOnce.Do(func() { close(r.ready) })
		}
		r.arrivalMu.Unlock()
		<-r.ready
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if reflect.DeepEqual(r.current, expected) {
		r.current = replacement
		return replacement, true, nil
	}
	return r.current, false, nil
}

func TestEnsureCodexDevicePoolSlotConcurrentColdStartConverges(t *testing.T) {
	const workers = 12
	repo := newCodexDevicePoolConcurrentRepo(workers)
	svc := &OpenAIGatewayService{accountRepo: repo}
	account := newTestOAuthAccount(43, nil)
	observed := codexUAPersonaSelection{Platform: codexUAPersonaWindows, Sandbox: "none"}
	quotas := codexDevicePlatformQuotas{Windows: 8}

	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for userID := int64(1); userID <= workers; userID++ {
		wg.Add(1)
		go func(userID int64) {
			defer wg.Done()
			_, err := svc.ensureCodexDevicePoolSlot(context.Background(), account, observed, userID, "", quotas)
			errs <- err
		}(userID)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}

	repo.mu.Lock()
	persisted, ok := repo.current.(codexDevicePoolState)
	repo.mu.Unlock()
	require.True(t, ok)
	require.Len(t, persisted.Slots, quotas.Windows)
	canonical, valid := canonicalCodexDevicePoolState(persisted)
	require.True(t, valid)
	require.Equal(t, persisted, canonical)
	seen := make(map[int]struct{}, len(persisted.Slots))
	for _, slot := range persisted.Slots {
		_, duplicate := seen[slot.ID]
		require.False(t, duplicate)
		seen[slot.ID] = struct{}{}
	}
	require.Greater(t, svc.SnapshotCodexDevicePoolMetrics().ConflictTotal, int64(0))
}

func TestEnsureCodexDevicePoolSlotMalformedStateWarnsOncePerInterval(t *testing.T) {
	logSink, restore := captureStructuredLog(t)
	defer restore()

	svc := &OpenAIGatewayService{
		accountRepo:                     &codexPersonaFirstWriterRepo{},
		codexDevicePoolMalformedLogRate: newAccountWriteThrottle(time.Hour),
	}
	account := newTestOAuthAccount(44, map[string]any{codexDevicePoolExtraKey: "not-an-object"})
	for range 2 {
		_, err := svc.ensureCodexDevicePoolSlot(
			context.Background(), account,
			codexUAPersonaSelection{Platform: codexUAPersonaUbuntu, Sandbox: "seccomp"},
			1, "", codexDevicePlatformQuotas{Linux: 1},
		)
		require.ErrorContains(t, err, "malformed codex device pool state")
	}

	require.Equal(t, int64(2), svc.SnapshotCodexDevicePoolMetrics().MalformedStateTotal)
	logSink.mu.Lock()
	defer logSink.mu.Unlock()
	warnings := 0
	for _, event := range logSink.events {
		if event != nil && event.Message == "openai_codex_device_pool_state_malformed" {
			warnings++
			require.Equal(t, "warn", event.Level)
			require.Equal(t, int64(44), event.Fields["account_id"])
		}
	}
	require.Equal(t, 1, warnings)
}
