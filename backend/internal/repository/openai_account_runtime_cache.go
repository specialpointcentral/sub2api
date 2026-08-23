package repository

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

const (
	openAIAccountRuntimeBlockPrefix = "openai_runtime_block:account:"
	openAIOAuth429StormKey          = "openai_429_storm:oauth"
)

var openAIAccountRuntimeBlockSetScript = redis.NewScript(`
	local key = KEYS[1]
	local requested_until = tonumber(ARGV[1])
	local requested_ttl = tonumber(ARGV[2])
	local current_until = tonumber(redis.call('GET', key) or '0')
	if current_until >= requested_until then
		return current_until
	end
	redis.call('SET', key, requested_until, 'PX', requested_ttl)
	return requested_until
`)

var openAIOAuth429StormIncrementScript = redis.NewScript(`
	local key = KEYS[1]
	local ttl = tonumber(ARGV[1])
	local count = redis.call('INCR', key)
	if count == 1 then
		redis.call('PEXPIRE', key, ttl)
	end
	return count
`)

type openAIAccountRuntimeCache struct {
	rdb *redis.Client
}

func NewOpenAIAccountRuntimeCache(rdb *redis.Client) service.OpenAIAccountRuntimeCache {
	return &openAIAccountRuntimeCache{rdb: rdb}
}

func (c *openAIAccountRuntimeCache) SetOpenAIAccountRuntimeBlock(ctx context.Context, accountID int64, until time.Time) (time.Time, error) {
	ttl := time.Until(until)
	if ttl < time.Millisecond {
		ttl = time.Millisecond
	}
	result, err := openAIAccountRuntimeBlockSetScript.Run(
		ctx,
		c.rdb,
		[]string{fmt.Sprintf("%s%d", openAIAccountRuntimeBlockPrefix, accountID)},
		until.UnixMilli(),
		ttl.Milliseconds(),
	).Int64()
	if err != nil {
		return time.Time{}, fmt.Errorf("set openai account runtime block: %w", err)
	}
	return time.UnixMilli(result), nil
}

func (c *openAIAccountRuntimeCache) GetOpenAIAccountRuntimeBlock(ctx context.Context, accountID int64) (time.Time, error) {
	result, err := c.rdb.Get(ctx, fmt.Sprintf("%s%d", openAIAccountRuntimeBlockPrefix, accountID)).Int64()
	if err == redis.Nil {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("get openai account runtime block: %w", err)
	}
	return time.UnixMilli(result), nil
}

func (c *openAIAccountRuntimeCache) GetOpenAIAccountRuntimeBlocks(ctx context.Context, accountIDs []int64) (map[int64]time.Time, error) {
	result := make(map[int64]time.Time, len(accountIDs))
	if len(accountIDs) == 0 {
		return result, nil
	}
	keys := make([]string, len(accountIDs))
	for i, accountID := range accountIDs {
		keys[i] = fmt.Sprintf("%s%d", openAIAccountRuntimeBlockPrefix, accountID)
	}
	values, err := c.rdb.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, fmt.Errorf("batch get openai account runtime blocks: %w", err)
	}
	for i, value := range values {
		if value == nil {
			continue
		}
		unixMillis, err := strconv.ParseInt(fmt.Sprint(value), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parse openai account runtime block for account %d: %w", accountIDs[i], err)
		}
		result[accountIDs[i]] = time.UnixMilli(unixMillis)
	}
	return result, nil
}

func (c *openAIAccountRuntimeCache) ClearOpenAIAccountRuntimeBlock(ctx context.Context, accountID int64) error {
	if err := c.rdb.Del(ctx, fmt.Sprintf("%s%d", openAIAccountRuntimeBlockPrefix, accountID)).Err(); err != nil {
		return fmt.Errorf("clear openai account runtime block: %w", err)
	}
	return nil
}

func (c *openAIAccountRuntimeCache) IncrementOpenAIOAuth429Storm(ctx context.Context, window time.Duration) (int64, error) {
	if window < time.Millisecond {
		window = time.Millisecond
	}
	count, err := openAIOAuth429StormIncrementScript.Run(ctx, c.rdb, []string{openAIOAuth429StormKey}, window.Milliseconds()).Int64()
	if err != nil {
		return 0, fmt.Errorf("increment openai oauth 429 storm: %w", err)
	}
	return count, nil
}

func (c *openAIAccountRuntimeCache) GetOpenAIOAuth429StormCount(ctx context.Context) (int64, error) {
	count, err := c.rdb.Get(ctx, openAIOAuth429StormKey).Int64()
	if err == redis.Nil {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("get openai oauth 429 storm: %w", err)
	}
	return count, nil
}
