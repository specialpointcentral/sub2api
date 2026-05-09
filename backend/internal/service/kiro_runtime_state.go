package service

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/kirocooldown"
)

var errKiroCooldownStoreUnavailable = errors.New("kiro cooldown store unavailable")

// logKiroCooldownBestEffortFailure logs a best-effort cooldown store failure.
// Cooldown bookkeeping must never convert an otherwise successful upstream
// exchange into a client-visible 502: the scheduler already fails open on store
// outages, so the request path logs and continues with degraded cooldown memory.
func logKiroCooldownBestEffortFailure(op string, err error) {
	slog.Warn("kiro_cooldown_best_effort_failed", "op", op, "error", err)
}

type KiroCooldownStore interface {
	ReserveRequest(ctx context.Context, tokenKey string) (time.Duration, error)
	MarkSuccess(ctx context.Context, tokenKey string) error
	Mark429(ctx context.Context, tokenKey string) (time.Duration, error)
	MarkSuspended(ctx context.Context, tokenKey string) (time.Duration, error)
	GetState(ctx context.Context, tokenKey string) (*kirocooldown.State, error)
	ClearEarliestTransientCooldown(ctx context.Context, tokenKeys []string) (bool, error)
}

func asKiroCooldownFailoverError(err error) *UpstreamFailoverError {
	if err == nil {
		return nil
	}
	var cooldownErr *kirocooldown.Error
	if !errors.As(err, &cooldownErr) {
		return nil
	}
	return &UpstreamFailoverError{
		StatusCode:   http.StatusTooManyRequests,
		ResponseBody: []byte(cooldownErr.Error()),
	}
}

func (s *GatewayService) checkAndWaitKiroCooldown(ctx context.Context, tokenKey string) error {
	if s == nil || s.kiroCooldownStore == nil {
		return errKiroCooldownStoreUnavailable
	}
	waitFor, err := s.kiroCooldownStore.ReserveRequest(ctx, tokenKey)
	if err != nil {
		return err
	}
	if waitFor <= 0 {
		return nil
	}
	timer := time.NewTimer(waitFor)
	select {
	case <-ctx.Done():
		if !timer.Stop() {
			<-timer.C
		}
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (s *GatewayService) markKiroSuccess(ctx context.Context, tokenKey string) error {
	if s == nil || s.kiroCooldownStore == nil {
		return errKiroCooldownStoreUnavailable
	}
	return s.kiroCooldownStore.MarkSuccess(ctx, tokenKey)
}

func (s *GatewayService) markKiro429(ctx context.Context, tokenKey string) (time.Duration, error) {
	if s == nil || s.kiroCooldownStore == nil {
		return 0, errKiroCooldownStoreUnavailable
	}
	return s.kiroCooldownStore.Mark429(ctx, tokenKey)
}

func (s *GatewayService) markKiroSuspended(ctx context.Context, tokenKey string) (time.Duration, error) {
	if s == nil || s.kiroCooldownStore == nil {
		return 0, errKiroCooldownStoreUnavailable
	}
	return s.kiroCooldownStore.MarkSuspended(ctx, tokenKey)
}

func (s *GatewayService) getKiroCooldownState(ctx context.Context, tokenKey string) (*kirocooldown.State, error) {
	if s == nil || s.kiroCooldownStore == nil {
		return nil, errKiroCooldownStoreUnavailable
	}
	return s.kiroCooldownStore.GetState(ctx, tokenKey)
}

func kiroRuntimeStateSnapshot(state *kirocooldown.State) (string, string, *time.Time) {
	if state == nil || !state.Active {
		return "", "", nil
	}
	resetAt := state.CooldownUntil
	switch state.Reason {
	case kirocooldown.CooldownReasonSuspended:
		return "suspended", state.Reason, &resetAt
	default:
		return "cooldown", state.Reason, &resetAt
	}
}
