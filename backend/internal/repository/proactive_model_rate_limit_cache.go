package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

const (
	modelRateLimitConfigTTL     = 30 * time.Second
	modelRateLimitConfigChannel = "pmrl:config:invalidate"
)

var proactiveModelRateLimitAdmitScript = redis.NewScript(`
	redis.replicate_commands()
	local concurrencyKey = KEYS[1]
	local rpmKey = KEYS[2]
	local concurrencyLimit = tonumber(ARGV[1])
	local rpmLimit = tonumber(ARGV[2])
	local slotTTL = tonumber(ARGV[3])
	local requestID = ARGV[4]
	local now = tonumber(ARGV[5])
	if now == nil or now <= 0 then
		local redisTime = redis.call('TIME')
		now = tonumber(redisTime[1]) * 1000 + math.floor(tonumber(redisTime[2]) / 1000)
	end
	redis.call('ZREMRANGEBYSCORE', concurrencyKey, '-inf', now - slotTTL)
	redis.call('ZREMRANGEBYSCORE', rpmKey, '-inf', now - 60000)
	local concurrencyUsed = redis.call('ZCARD', concurrencyKey)
	local rpmUsed = redis.call('ZCARD', rpmKey)
	local minute = math.floor(now / 60000)
	for _, field in ipairs(redis.call('HKEYS', KEYS[3])) do
		local separator = string.find(field, ':', 1, true)
		local fieldMinute = separator and tonumber(string.sub(field, 1, separator - 1)) or nil
		if fieldMinute ~= nil and fieldMinute < minute - 9 then redis.call('HDEL', KEYS[3], field) end
	end
	if concurrencyLimit > 0 and concurrencyUsed >= concurrencyLimit then
		redis.call('HINCRBY', KEYS[3], tostring(minute) .. ':rejected:concurrency', 1)
		redis.call('EXPIRE', KEYS[3], 600)
		return {0, 1, concurrencyUsed, 1}
	end
	if rpmLimit > 0 and rpmUsed >= rpmLimit then
		local oldest = redis.call('ZRANGE', rpmKey, 0, 0, 'WITHSCORES')
		local retryAfter = 1
		if #oldest >= 2 then
			retryAfter = math.max(1, math.ceil((tonumber(oldest[2]) + 60000 - now) / 1000))
		end
		redis.call('HINCRBY', KEYS[3], tostring(minute) .. ':rejected:rpm', 1)
		redis.call('EXPIRE', KEYS[3], 600)
		return {0, 2, rpmUsed, retryAfter}
	end
	if concurrencyLimit > 0 then
		redis.call('ZADD', concurrencyKey, now, requestID)
		redis.call('PEXPIRE', concurrencyKey, math.max(slotTTL, 120000))
	end
	if rpmLimit > 0 then
		redis.call('ZADD', rpmKey, now, requestID)
		redis.call('PEXPIRE', rpmKey, 120000)
	end
	redis.call('HINCRBY', KEYS[3], tostring(minute) .. ':admitted', 1)
	if concurrencyLimit > 0 then redis.call('HINCRBY', KEYS[3], tostring(minute) .. ':admitted:concurrency', 1) end
	if rpmLimit > 0 then redis.call('HINCRBY', KEYS[3], tostring(minute) .. ':admitted:rpm', 1) end
	redis.call('EXPIRE', KEYS[3], 600)
	return {1, 0, 0, 0}
`)

var proactiveModelRateLimitRefreshScript = redis.NewScript(`
	redis.replicate_commands()
	if redis.call('ZSCORE', KEYS[1], ARGV[2]) == false then return 0 end
	local redisTime = redis.call('TIME')
	local now = tonumber(redisTime[1]) * 1000 + math.floor(tonumber(redisTime[2]) / 1000)
	redis.call('ZADD', KEYS[1], now, ARGV[2])
	redis.call('PEXPIRE', KEYS[1], math.max(tonumber(ARGV[1]), 120000))
	return 1
`)

type proactiveModelRateLimitCache struct {
	rdb       *redis.Client
	slotTTLMS int64
}

func NewProactiveModelRateLimitCache(rdb *redis.Client, slotTTL time.Duration) *proactiveModelRateLimitCache {
	if slotTTL <= 0 {
		slotTTL = 15 * time.Minute
	}
	return &proactiveModelRateLimitCache{rdb: rdb, slotTTLMS: slotTTL.Milliseconds()}
}

func (c *proactiveModelRateLimitCache) AdmitModelRateLimit(ctx context.Context, userID int64, model string, concurrencyLimit, rpmLimit int, requestID string, ruleID int64) (service.ModelRateLimitCacheAdmission, error) {
	return c.admitAtRule(ctx, userID, model, concurrencyLimit, rpmLimit, requestID, 0, ruleID)
}

func (c *proactiveModelRateLimitCache) admitAtRule(ctx context.Context, userID int64, model string, concurrencyLimit, rpmLimit int, requestID string, nowMS, ruleID int64) (service.ModelRateLimitCacheAdmission, error) {
	keys := append(modelRateLimitCounterKeys(userID, model), modelRateLimitStatsKey(userID, model, ruleID))
	values, err := proactiveModelRateLimitAdmitScript.Run(ctx, c.rdb, keys, concurrencyLimit, rpmLimit, c.slotTTLMS, requestID, nowMS).Int64Slice()
	if err != nil {
		return service.ModelRateLimitCacheAdmission{}, err
	}
	if len(values) != 4 {
		return service.ModelRateLimitCacheAdmission{}, fmt.Errorf("unexpected model rate limit admission response: %v", values)
	}
	result := service.ModelRateLimitCacheAdmission{Allowed: values[0] == 1, Used: int(values[2]), RetryAfterSeconds: int(values[3])}
	switch values[1] {
	case 1:
		result.Dimension = service.ModelRateLimitDimensionConcurrency
	case 2:
		result.Dimension = service.ModelRateLimitDimensionRPM
	}
	return result, nil
}

func (c *proactiveModelRateLimitCache) ReleaseModelRateLimit(ctx context.Context, userID int64, model, requestID string) error {
	keys := modelRateLimitCounterKeys(userID, model)
	return c.rdb.ZRem(ctx, keys[0], requestID).Err()
}

func (c *proactiveModelRateLimitCache) RefreshModelRateLimitConcurrency(ctx context.Context, userID int64, model, requestID string) (bool, error) {
	keys := modelRateLimitCounterKeys(userID, model)
	result, err := proactiveModelRateLimitRefreshScript.Run(ctx, c.rdb, keys[:1], c.slotTTLMS, requestID).Int()
	return result == 1, err
}

func (c *proactiveModelRateLimitCache) GetModelRateLimitUsageBatch(ctx context.Context, userID int64, models []string) (map[string]service.ModelRateLimitUsageCounts, error) {
	if len(models) == 0 {
		return map[string]service.ModelRateLimitUsageCounts{}, nil
	}
	now, err := c.rdb.Time(ctx).Result()
	if err != nil {
		return nil, err
	}
	nowMS := now.UnixMilli()
	type usageCommands struct {
		concurrency *redis.IntCmd
		rpm         *redis.IntCmd
		oldestRPM   *redis.ZSliceCmd
	}
	pipe := c.rdb.Pipeline()
	commands := make(map[string]usageCommands, len(models))
	for _, model := range models {
		keys := modelRateLimitCounterKeys(userID, model)
		pipe.ZRemRangeByScore(ctx, keys[0], "-inf", strconv.FormatInt(nowMS-c.slotTTLMS, 10))
		pipe.ZRemRangeByScore(ctx, keys[1], "-inf", strconv.FormatInt(nowMS-60_000, 10))
		commands[model] = usageCommands{
			concurrency: pipe.ZCard(ctx, keys[0]),
			rpm:         pipe.ZCard(ctx, keys[1]),
			oldestRPM:   pipe.ZRangeWithScores(ctx, keys[1], 0, 0),
		}
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return nil, err
	}
	result := make(map[string]service.ModelRateLimitUsageCounts, len(models))
	for model, modelCommands := range commands {
		retryAfterSeconds := 0
		oldestRPM := modelCommands.oldestRPM.Val()
		if len(oldestRPM) > 0 {
			remainingMS := int64(oldestRPM[0].Score) + 60_000 - nowMS
			if remainingMS > 0 {
				retryAfterSeconds = int((remainingMS + 999) / 1_000)
			}
		}
		result[model] = service.ModelRateLimitUsageCounts{
			Concurrency:          int(modelCommands.concurrency.Val()),
			RPM:                  int(modelCommands.rpm.Val()),
			RPMRetryAfterSeconds: retryAfterSeconds,
		}
	}
	return result, nil
}

func modelRateLimitCounterKeys(userID int64, model string) []string {
	digest := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(model))))
	tag := fmt.Sprintf("{u:%d:m:%s}", userID, hex.EncodeToString(digest[:]))
	return []string{"pmrl:" + tag + ":concurrency", "pmrl:" + tag + ":rpm"}
}

func modelRateLimitStatsKey(userID int64, model string, ruleID int64) string {
	digest := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(model))))
	tag := fmt.Sprintf("{u:%d:m:%s}", userID, hex.EncodeToString(digest[:]))
	return fmt.Sprintf("pmrl:%s:stats:r:%d", tag, ruleID)
}

func (c *proactiveModelRateLimitCache) GetRecentModelRateLimitTotals(ctx context.Context, userID int64, identities []service.ModelRateLimitCounterIdentity) (service.ModelRateLimitRecentTotals, error) {
	now, err := c.rdb.Time(ctx).Result()
	if err != nil {
		return service.ModelRateLimitRecentTotals{}, err
	}
	pipe := c.rdb.Pipeline()
	commands := make([]*redis.MapStringStringCmd, 0, len(identities))
	for _, identity := range identities {
		commands = append(commands, pipe.HGetAll(ctx, modelRateLimitStatsKey(userID, identity.EffectiveModel, identity.RuleID)))
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return service.ModelRateLimitRecentTotals{}, err
	}
	minimumMinute := now.Unix()/60 - 4
	totals := service.ModelRateLimitRecentTotals{}
	for _, command := range commands {
		for field, raw := range command.Val() {
			parts := strings.Split(field, ":")
			minute, parseErr := strconv.ParseInt(parts[0], 10, 64)
			value, valueErr := strconv.Atoi(raw)
			if parseErr != nil || valueErr != nil || minute < minimumMinute || len(parts) < 2 {
				continue
			}
			if parts[1] == "admitted" && len(parts) == 2 {
				totals.Admitted += value
			} else if parts[1] == "rejected" {
				totals.Rejected += value
			}
		}
	}
	return totals, nil
}

func (c *proactiveModelRateLimitCache) LoadModelRateLimitRules(ctx context.Context, userID *int64) ([]service.ModelRateLimitRule, bool, error) {
	raw, err := c.rdb.Get(ctx, modelRateLimitConfigKey(userID)).Bytes()
	if err == redis.Nil {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var rules []service.ModelRateLimitRule
	if err := json.Unmarshal(raw, &rules); err != nil {
		return nil, false, err
	}
	return rules, true, nil
}

func (c *proactiveModelRateLimitCache) StoreModelRateLimitRules(ctx context.Context, userID *int64, rules []service.ModelRateLimitRule) error {
	raw, err := json.Marshal(rules)
	if err != nil {
		return err
	}
	return c.rdb.Set(ctx, modelRateLimitConfigKey(userID), raw, modelRateLimitConfigTTL).Err()
}

func (c *proactiveModelRateLimitCache) PublishModelRateLimitInvalidation(ctx context.Context, userID *int64) error {
	return c.rdb.Publish(ctx, modelRateLimitConfigChannel, modelRateLimitScopePayload(userID)).Err()
}

func (c *proactiveModelRateLimitCache) SubscribeModelRateLimitInvalidations(handler func(userID *int64)) {
	if c == nil || c.rdb == nil || handler == nil {
		return
	}
	pubsub := c.rdb.Subscribe(context.Background(), modelRateLimitConfigChannel)
	go func() {
		defer func() { _ = pubsub.Close() }()
		for message := range pubsub.Channel() {
			if userID, ok := parseModelRateLimitScopePayload(message.Payload); ok {
				handler(userID)
			}
		}
	}()
}

func modelRateLimitConfigKey(userID *int64) string {
	return "pmrl:config:" + modelRateLimitScopePayload(userID)
}

func modelRateLimitScopePayload(userID *int64) string {
	if userID == nil {
		return "global"
	}
	return "user:" + strconv.FormatInt(*userID, 10)
}

func parseModelRateLimitScopePayload(value string) (*int64, bool) {
	if value == "global" {
		return nil, true
	}
	raw, ok := strings.CutPrefix(value, "user:")
	if !ok {
		return nil, false
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	return &id, err == nil && id > 0
}
