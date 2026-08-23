package service

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math/rand/v2"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"go.uber.org/zap"
)

const openAIRequestPacingMaxWait = 30 * time.Second

var ErrOpenAIRequestPacingTimeout = errors.New("openai account request pacing wait exceeded 30 seconds")

var openAIRequestPacingFallbackTokenCounter atomic.Uint64

func newOpenAIRequestPacingOwnerToken() string {
	var raw [16]byte
	if _, err := cryptorand.Read(raw[:]); err != nil {
		return fmt.Sprintf("%d-%d", time.Now().UnixNano(), openAIRequestPacingFallbackTokenCounter.Add(1))
	}
	return hex.EncodeToString(raw[:])
}

func sampleOpenAIRequestPacingInterval(minimum, maximum time.Duration) time.Duration {
	if maximum <= minimum {
		return minimum
	}
	return minimum + time.Duration(rand.Int64N(int64(maximum-minimum)+1))
}

func sleepOpenAIRequestPacing(ctx context.Context, wait time.Duration) error {
	if wait <= 0 {
		return nil
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func isOpenAIRequestPacingAccount(account *Account) bool {
	return account != nil && (account.IsOpenAIOAuth() || account.IsOpenAIApiKey())
}

func (s *OpenAIGatewayService) waitForOpenAIAccountStart(ctx context.Context, account *Account) error {
	if s == nil || s.settingService == nil || !isOpenAIRequestPacingAccount(account) {
		return nil
	}
	// Callers pace at the immediate upstream-start boundary. The scheduler's
	// account slot is therefore already held, and persistent WebSocket paths may
	// also hold a pool lease. Keeping those admissions prevents a paced request
	// from losing its selected account or being reordered by a second admission;
	// pacing is disabled by default and any enabled wait is bounded to 30 seconds.
	return s.waitForOpenAIAccountStartWithSettings(ctx, account, s.settingService.GetOpenAITrafficShapingSettings(ctx))
}

func (s *OpenAIGatewayService) waitForOpenAIAccountStartWithSettings(ctx context.Context, account *Account, settings OpenAITrafficShapingSettings) error {
	if s == nil || !isOpenAIRequestPacingAccount(account) || !settings.RequestPacingEnabled || s.concurrencyService == nil {
		return nil
	}
	minimum := time.Duration(settings.RequestPacingMinIntervalMS) * time.Millisecond
	maximum := time.Duration(settings.RequestPacingMaxIntervalMS) * time.Millisecond
	if minimum < 0 || maximum < minimum || maximum > time.Duration(MaxOpenAIRequestPacingIntervalMS)*time.Millisecond {
		minimum = time.Duration(DefaultOpenAIRequestPacingMinIntervalMS) * time.Millisecond
		maximum = time.Duration(DefaultOpenAIRequestPacingMaxIntervalMS) * time.Millisecond
	}
	sample := sampleOpenAIRequestPacingInterval
	if s.openAIRequestPacingSample != nil {
		sample = s.openAIRequestPacingSample
	}
	sleep := sleepOpenAIRequestPacing
	if s.openAIRequestPacingSleep != nil {
		sleep = s.openAIRequestPacingSleep
	}
	now := time.Now
	if s.openAIRequestPacingNow != nil {
		now = s.openAIRequestPacingNow
	}
	deadline := now().Add(openAIRequestPacingMaxWait)
	ownerToken := newOpenAIRequestPacingOwnerToken()
	pacingCtx, cancel := context.WithTimeout(ctx, openAIRequestPacingMaxWait)
	defer cancel()
	rollbackAdmission := func() {
		rollbackCtx, rollbackCancel := context.WithTimeout(context.Background(), time.Second)
		defer rollbackCancel()
		if _, err := s.concurrencyService.RollbackOpenAIOAuthStart(rollbackCtx, account.ID, ownerToken); err != nil {
			logger.L().Warn("openai_request_pacing_rollback_failed",
				zap.Int64("account_id", account.ID),
				zap.Error(err),
			)
		}
	}
	pacingContextError := func(err error) error {
		if parentErr := ctx.Err(); parentErr != nil {
			return parentErr
		}
		if errors.Is(err, context.DeadlineExceeded) || pacingCtx.Err() != nil || !now().Before(deadline) {
			return ErrOpenAIRequestPacingTimeout
		}
		return err
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := pacingCtx.Err(); err != nil || !now().Before(deadline) {
			return pacingContextError(err)
		}
		interval := sample(minimum, maximum)
		wait, err := s.concurrencyService.ReserveOpenAIOAuthStart(pacingCtx, account.ID, interval, deadline, ownerToken)
		if err != nil {
			if ctx.Err() != nil || pacingCtx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				rollbackAdmission()
				return pacingContextError(err)
			}
			logger.L().Warn("openai_request_pacing_reserve_failed_open",
				zap.Int64("account_id", account.ID),
				zap.Error(err),
			)
			return nil
		}
		if pacingCtx.Err() != nil || !now().Before(deadline) {
			rollbackAdmission()
			return pacingContextError(pacingCtx.Err())
		}
		if wait < 0 {
			return ErrOpenAIRequestPacingTimeout
		}
		if wait <= 0 {
			return nil
		}
		if wait > deadline.Sub(now()) {
			return ErrOpenAIRequestPacingTimeout
		}
		if err := sleep(pacingCtx, wait); err != nil {
			return pacingContextError(err)
		}
	}
}
