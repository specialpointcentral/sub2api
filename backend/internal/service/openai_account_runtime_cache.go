package service

import (
	"context"
	"time"
)

// OpenAIAccountRuntimeCache shares short-lived scheduler blocks and the OAuth
// 429 storm window across gateway replicas. Implementations are optional;
// OpenAIGatewayService keeps the process-local state as a failover path.
type OpenAIAccountRuntimeCache interface {
	SetOpenAIAccountRuntimeBlock(ctx context.Context, accountID int64, until time.Time) (time.Time, error)
	GetOpenAIAccountRuntimeBlock(ctx context.Context, accountID int64) (time.Time, error)
	ClearOpenAIAccountRuntimeBlock(ctx context.Context, accountID int64) error
	IncrementOpenAIOAuth429Storm(ctx context.Context, window time.Duration) (int64, error)
	GetOpenAIOAuth429StormCount(ctx context.Context) (int64, error)
}

// OpenAIAccountRuntimeBatchCache is the optional scheduling-hot-path extension.
// Implementations return only account IDs with an active shared deadline.
type OpenAIAccountRuntimeBatchCache interface {
	GetOpenAIAccountRuntimeBlocks(ctx context.Context, accountIDs []int64) (map[int64]time.Time, error)
}

// SetOpenAIAccountRuntimeCache installs the optional distributed runtime-state
// store. A nil cache preserves the historical in-memory behavior.
func (s *OpenAIGatewayService) SetOpenAIAccountRuntimeCache(cache OpenAIAccountRuntimeCache) {
	if s != nil {
		s.openAIAccountRuntimeCache = cache
		s.openaiAccountRuntimeBlockReadCache.Clear()
	}
}

func openAIAccountRuntimeCacheContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), openAIAccountStateUpdateTimeout)
}

func openAIAccountRuntimeReadContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 250*time.Millisecond)
}
