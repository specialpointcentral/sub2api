package service

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/model"
	"github.com/stretchr/testify/require"
)

type quotaProbeScheduleTestCache struct {
	ConcurrencyCache
	mu               sync.Mutex
	claimed          bool
	err              error
	calls            int
	delay            time.Duration
	claimContextErr  error
	claimHasDeadline bool
}

type openAIQuotaUsageStub struct {
	calls atomic.Int32
}

type quotaProbeContextKey struct{}

type quotaProbeContextSettingRepo struct {
	SettingRepository
	values   map[string]string
	observed any
}

func (r *quotaProbeContextSettingRepo) GetMultiple(ctx context.Context, keys []string) (map[string]string, error) {
	r.observed = ctx.Value(quotaProbeContextKey{})
	result := make(map[string]string, len(keys))
	for _, key := range keys {
		result[key] = r.values[key]
	}
	return result, nil
}

func (s *openAIQuotaUsageStub) QueryUsage(_ context.Context, _ int64) (*OpenAIQuotaUsage, error) {
	s.calls.Add(1)
	return &OpenAIQuotaUsage{}, nil
}

func (c *quotaProbeScheduleTestCache) TryClaimOpenAIQuotaProbe(ctx context.Context, _ int64, _ time.Duration, _ string, _ time.Time) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	c.claimContextErr = ctx.Err()
	_, c.claimHasDeadline = ctx.Deadline()
	if c.delay > 0 {
		time.Sleep(c.delay)
	}
	if c.err != nil {
		return false, c.err
	}
	if c.claimed {
		return false, nil
	}
	c.claimed = true
	return true, nil
}

func TestAccountUsageServiceQuotaClaimSurvivesCallerCancellationWithDeadline(t *testing.T) {
	distributed := &quotaProbeScheduleTestCache{}
	svc := &AccountUsageService{
		cache:              NewUsageCache(),
		concurrencyService: NewConcurrencyService(distributed),
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	claimed := svc.shouldProbeOpenAICodexSnapshotWithContext(ctx, 103, time.Now(), time.Time{}, false)

	require.True(t, claimed)
	require.NoError(t, distributed.claimContextErr)
	require.True(t, distributed.claimHasDeadline)
}

func TestJitteredOpenAIQuotaProbeIntervalBounds(t *testing.T) {
	base := 10 * time.Minute
	requireEqualDuration := func(want, got time.Duration) {
		t.Helper()
		if got != want {
			t.Fatalf("interval = %s, want %s", got, want)
		}
	}
	requireEqualDuration(7*time.Minute+30*time.Second, jitteredOpenAIQuotaProbeInterval(base, 0.25, 0))
	requireEqualDuration(10*time.Minute, jitteredOpenAIQuotaProbeInterval(base, 0.25, 0.5))
	requireEqualDuration(12*time.Minute+30*time.Second, jitteredOpenAIQuotaProbeInterval(base, 0.25, 1))
}

func TestAccountUsageServiceShouldProbeOpenAICodexSnapshotUsesAtomicJitteredSchedule(t *testing.T) {
	settingSvc := NewSettingService(&codexVersionSettingRepoStub{values: map[string]string{
		SettingKeyOpenAIQuotaProbeIntervalMinutes: "10",
		SettingKeyOpenAIQuotaProbeJitterRatio:     "0.25",
	}}, &config.Config{})
	svc := &AccountUsageService{
		cache:                 NewUsageCache(),
		settingService:        settingSvc,
		openAIProbeJitterUnit: func() float64 { return 0 },
	}
	now := time.Date(2026, 8, 23, 0, 0, 0, 0, time.UTC)

	if !svc.shouldProbeOpenAICodexSnapshotWithContext(context.Background(), 77, now, time.Time{}, false) {
		t.Fatal("first automatic probe should claim eligibility")
	}
	if svc.shouldProbeOpenAICodexSnapshotWithContext(context.Background(), 77, now.Add(7*time.Minute+29*time.Second), time.Time{}, false) {
		t.Fatal("probe should remain gated before the -25% boundary")
	}
	if !svc.shouldProbeOpenAICodexSnapshotWithContext(context.Background(), 77, now.Add(7*time.Minute+30*time.Second), time.Time{}, false) {
		t.Fatal("probe should become eligible at the -25% boundary")
	}
	if !svc.shouldProbeOpenAICodexSnapshotWithContext(context.Background(), 77, now.Add(time.Second), time.Time{}, true) {
		t.Fatal("force=true must bypass the automatic schedule")
	}
}

func TestAccountUsageServiceShouldProbeOpenAICodexSnapshotPreservesCallerContext(t *testing.T) {
	repo := &quotaProbeContextSettingRepo{values: map[string]string{
		SettingKeyOpenAIQuotaProbeIntervalMinutes: "10",
		SettingKeyOpenAIQuotaProbeJitterRatio:     "0.25",
	}}
	svc := &AccountUsageService{
		cache:          NewUsageCache(),
		settingService: NewSettingService(repo, &config.Config{}),
	}
	ctx := context.WithValue(context.Background(), quotaProbeContextKey{}, "quota-probe-request")

	if !svc.shouldProbeOpenAICodexSnapshotWithContext(ctx, 78, time.Now(), time.Time{}, false) {
		t.Fatal("first automatic probe should claim eligibility")
	}
	if got, want := repo.observed, any("quota-probe-request"); got != want {
		t.Fatalf("settings context value = %v, want %v", got, want)
	}
}

func TestGetOpenAIUsageScheduleDuePreservesCallerContext(t *testing.T) {
	repo := &quotaProbeContextSettingRepo{values: map[string]string{
		SettingKeyOpenAIQuotaProbeIntervalMinutes: "10",
		SettingKeyOpenAIQuotaProbeJitterRatio:     "0.25",
	}}
	now := time.Now().UTC()
	account := &Account{
		ID:       79,
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Extra: map[string]any{
			"codex_5h_used_percent":  10.0,
			"codex_5h_reset_at":      now.Add(time.Hour).Format(time.RFC3339),
			"codex_7d_used_percent":  20.0,
			"codex_7d_reset_at":      now.Add(24 * time.Hour).Format(time.RFC3339),
			"codex_usage_updated_at": now.Format(time.RFC3339),
		},
	}
	svc := &AccountUsageService{
		cache:          NewUsageCache(),
		settingService: NewSettingService(repo, &config.Config{}),
	}
	svc.cache.openAIProbeCache.Store(account.ID, &openAIProbeScheduleState{
		nextProbeAt:  now.Add(-time.Minute),
		baseInterval: 10 * time.Minute,
		jitterRatio:  0.25,
		initialized:  true,
	})
	ctx := context.WithValue(context.Background(), quotaProbeContextKey{}, "usage-request")

	if _, err := svc.getOpenAIUsage(ctx, account, false); err != nil {
		t.Fatalf("getOpenAIUsage() error = %v", err)
	}
	if got, want := repo.observed, any("usage-request"); got != want {
		t.Fatalf("settings context value = %v, want %v", got, want)
	}
}

func TestAccountUsageServiceShouldProbeOpenAICodexSnapshotAllowsOneConcurrentClaim(t *testing.T) {
	svc := &AccountUsageService{cache: NewUsageCache()}
	now := time.Date(2026, 8, 23, 0, 0, 0, 0, time.UTC)
	var claimed atomic.Int32
	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if svc.shouldProbeOpenAICodexSnapshotWithContext(context.Background(), 88, now, time.Time{}, false) {
				claimed.Add(1)
			}
		}()
	}
	wg.Wait()
	if got := claimed.Load(); got != 1 {
		t.Fatalf("concurrent claims = %d, want 1", got)
	}
}

func TestAccountUsageServiceShouldProbeOpenAICodexSnapshotReclaimsChangedSchedule(t *testing.T) {
	svc := &AccountUsageService{cache: NewUsageCache()}
	now := time.Date(2026, 8, 23, 0, 0, 0, 0, time.UTC)
	svc.cache.openAIProbeCache.Store(int64(99), &openAIProbeScheduleState{
		nextProbeAt:  now.Add(24 * time.Hour),
		baseInterval: time.Hour,
		jitterRatio:  0.25,
		initialized:  true,
	})

	if !svc.shouldProbeOpenAICodexSnapshotWithContext(context.Background(), 99, now, time.Time{}, false) {
		t.Fatal("changed interval must not remain blocked by an old far-future schedule")
	}
}

func TestAccountUsageServiceQuotaScheduleForceBypassesDistributedClaim(t *testing.T) {
	cache := &quotaProbeScheduleTestCache{}
	svc := &AccountUsageService{
		cache:              NewUsageCache(),
		concurrencyService: NewConcurrencyService(cache),
	}
	now := time.Date(2026, 8, 23, 0, 0, 0, 0, time.UTC)

	if !svc.shouldProbeOpenAICodexSnapshotWithContext(context.Background(), 101, now, time.Time{}, true) {
		t.Fatal("force=true must bypass the automatic schedule")
	}
	if cache.calls != 0 {
		t.Fatalf("distributed claim calls after force = %d, want 0", cache.calls)
	}
	if !svc.shouldProbeOpenAICodexSnapshotWithContext(context.Background(), 101, now, time.Time{}, false) {
		t.Fatal("automatic claim must remain available after a forced probe")
	}
}

func TestAccountUsageServiceQuotaScheduleFallsBackLocallyOnRedisError(t *testing.T) {
	const redisDelay = 25 * time.Millisecond
	distributed := &quotaProbeScheduleTestCache{err: errors.New("redis unavailable"), delay: redisDelay}
	svc := &AccountUsageService{
		cache:                 NewUsageCache(),
		concurrencyService:    NewConcurrencyService(distributed),
		openAIProbeJitterUnit: func() float64 { return 0.5 },
	}
	now := time.Date(2026, 8, 23, 0, 0, 0, 0, time.UTC)
	wallStart := time.Now()

	if !svc.shouldProbeOpenAICodexSnapshotWithContext(context.Background(), 102, now, time.Time{}, false) {
		t.Fatal("first probe must fail open through the local schedule")
	}
	if svc.shouldProbeOpenAICodexSnapshotWithContext(context.Background(), 102, now.Add(time.Second), time.Time{}, false) {
		t.Fatal("local fallback must still suppress a duplicate probe")
	}
	if distributed.calls != 1 {
		t.Fatalf("Redis calls during degraded backoff = %d, want 1", distributed.calls)
	}
	value, _ := svc.cache.openAIProbeCache.Load(int64(102))
	state, ok := value.(*openAIProbeScheduleState)
	require.True(t, ok)
	minimumRetryAt := wallStart.Add(redisDelay + openAIQuotaProbeRedisRetryBackoff - 5*time.Millisecond)
	if state.distributedRetryAt.Before(minimumRetryAt) {
		t.Fatalf("degraded retry deadline = %s, want at least %s based on Redis failure time", state.distributedRetryAt, minimumRetryAt)
	}
}

func TestGetOpenAIUsageProbesWhenConfiguredSchedulePredatesLegacyStaleness(t *testing.T) {
	settingSvc := NewSettingService(&codexVersionSettingRepoStub{values: map[string]string{
		SettingKeyOpenAIQuotaProbeIntervalMinutes: "1",
		SettingKeyOpenAIQuotaProbeJitterRatio:     "0",
	}}, &config.Config{})
	now := time.Now().UTC()
	parentID := int64(7001)
	account := &Account{
		ID:              7002,
		Platform:        PlatformOpenAI,
		Type:            AccountTypeOAuth,
		ParentAccountID: &parentID,
		QuotaDimension:  QuotaDimensionSpark,
		Extra: map[string]any{
			"codex_5h_used_percent":  10.0,
			"codex_5h_reset_at":      now.Add(time.Hour).Format(time.RFC3339),
			"codex_7d_used_percent":  20.0,
			"codex_7d_reset_at":      now.Add(24 * time.Hour).Format(time.RFC3339),
			"codex_usage_updated_at": now.Add(-2 * time.Minute).Format(time.RFC3339),
		},
	}
	quota := &openAIQuotaUsageStub{}
	svc := &AccountUsageService{
		cache:                 NewUsageCache(),
		settingService:        settingSvc,
		openAIProbeJitterUnit: func() float64 { return 0.5 },
		openAIQuotaService:    quota,
	}

	if _, err := svc.getOpenAIUsage(context.Background(), account, false); err != nil {
		t.Fatalf("getOpenAIUsage() error = %v", err)
	}
	if got := quota.calls.Load(); got != 1 {
		t.Fatalf("quota probe calls = %d, want 1 for a two-minute-old snapshot on a one-minute schedule", got)
	}
	value, ok := svc.cache.openAIProbeCache.Load(account.ID)
	if !ok {
		t.Fatal("configured automatic schedule was not initialized")
	}
	state, ok := value.(*openAIProbeScheduleState)
	if !ok || state == nil || !state.initialized {
		t.Fatalf("schedule state = %#v, want initialized", value)
	}
}

func TestGetOpenAIUsageColdScheduleDoesNotRepeatFreshProbe(t *testing.T) {
	settingSvc := NewSettingService(&codexVersionSettingRepoStub{values: map[string]string{
		SettingKeyOpenAIQuotaProbeIntervalMinutes: "10",
		SettingKeyOpenAIQuotaProbeJitterRatio:     "0.25",
	}}, &config.Config{})
	now := time.Now().UTC()
	parentID := int64(7101)
	account := &Account{
		ID:              7102,
		Platform:        PlatformOpenAI,
		Type:            AccountTypeOAuth,
		ParentAccountID: &parentID,
		QuotaDimension:  QuotaDimensionSpark,
		Extra: map[string]any{
			"codex_5h_used_percent":  10.0,
			"codex_5h_reset_at":      now.Add(time.Hour).Format(time.RFC3339),
			"codex_7d_used_percent":  20.0,
			"codex_7d_reset_at":      now.Add(24 * time.Hour).Format(time.RFC3339),
			"codex_usage_updated_at": now.Add(-5 * time.Second).Format(time.RFC3339),
		},
	}
	quota := &openAIQuotaUsageStub{}
	svc := &AccountUsageService{
		cache:                 NewUsageCache(),
		settingService:        settingSvc,
		openAIProbeJitterUnit: func() float64 { return 0.5 },
		openAIQuotaService:    quota,
	}

	if _, err := svc.getOpenAIUsage(context.Background(), account, false); err != nil {
		t.Fatalf("getOpenAIUsage() error = %v", err)
	}
	if got := quota.calls.Load(); got != 0 {
		t.Fatalf("quota probe calls = %d, want 0 for a freshly persisted snapshot", got)
	}
}

func TestGetOpenAIUsageColdSchedulesJitterInitialDeadlinesAcrossAccounts(t *testing.T) {
	settingSvc := NewSettingService(&codexVersionSettingRepoStub{values: map[string]string{
		SettingKeyOpenAIQuotaProbeIntervalMinutes: "10",
		SettingKeyOpenAIQuotaProbeJitterRatio:     "0.25",
	}}, &config.Config{})
	updatedAt := time.Now().UTC().Truncate(time.Second)
	parentID := int64(7200)
	newAccount := func(id int64) *Account {
		return &Account{
			ID: id, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
			ParentAccountID: &parentID, QuotaDimension: QuotaDimensionSpark,
			Extra: map[string]any{
				"codex_5h_used_percent":  10.0,
				"codex_5h_reset_at":      updatedAt.Add(time.Hour).Format(time.RFC3339),
				"codex_7d_used_percent":  20.0,
				"codex_7d_reset_at":      updatedAt.Add(24 * time.Hour).Format(time.RFC3339),
				"codex_usage_updated_at": updatedAt.Format(time.RFC3339),
			},
		}
	}
	units := []float64{0, 1}
	unitIndex := 0
	svc := &AccountUsageService{
		cache:          NewUsageCache(),
		settingService: settingSvc,
		openAIProbeJitterUnit: func() float64 {
			unit := units[unitIndex]
			unitIndex++
			return unit
		},
		openAIQuotaService: &openAIQuotaUsageStub{},
	}

	for _, account := range []*Account{newAccount(7201), newAccount(7202)} {
		if _, err := svc.getOpenAIUsage(context.Background(), account, false); err != nil {
			t.Fatalf("getOpenAIUsage(%d) error = %v", account.ID, err)
		}
	}
	valueA, _ := svc.cache.openAIProbeCache.Load(int64(7201))
	valueB, _ := svc.cache.openAIProbeCache.Load(int64(7202))
	stateA, ok := valueA.(*openAIProbeScheduleState)
	require.True(t, ok)
	stateB, ok := valueB.(*openAIProbeScheduleState)
	require.True(t, ok)
	deadlineA := stateA.nextProbeAt
	deadlineB := stateB.nextProbeAt
	if got, want := deadlineB.Sub(deadlineA), 5*time.Minute; got != want {
		t.Fatalf("cold deadline spread = %s, want %s", got, want)
	}
}

type accountUsageCodexProbeRepo struct {
	stubOpenAIAccountRepo
	updateExtraCh chan map[string]any
	rateLimitCh   chan time.Time
}

func (r *accountUsageCodexProbeRepo) UpdateExtra(_ context.Context, _ int64, updates map[string]any) error {
	if r.updateExtraCh != nil {
		copied := make(map[string]any, len(updates))
		for k, v := range updates {
			copied[k] = v
		}
		r.updateExtraCh <- copied
	}
	return nil
}

func (r *accountUsageCodexProbeRepo) SetRateLimited(_ context.Context, _ int64, resetAt time.Time) error {
	if r.rateLimitCh != nil {
		r.rateLimitCh <- resetAt
	}
	return nil
}

func TestShouldRefreshOpenAICodexSnapshot(t *testing.T) {
	t.Parallel()

	rateLimitedUntil := time.Now().Add(5 * time.Minute)
	now := time.Now()
	usage := &UsageInfo{
		FiveHour: &UsageProgress{Utilization: 0},
		SevenDay: &UsageProgress{Utilization: 0},
	}

	if !shouldRefreshOpenAICodexSnapshot(&Account{RateLimitResetAt: &rateLimitedUntil}, usage, now) {
		t.Fatal("expected rate-limited account to force codex snapshot refresh")
	}

	if shouldRefreshOpenAICodexSnapshot(&Account{}, usage, now) {
		t.Fatal("expected complete non-rate-limited usage to skip codex snapshot refresh")
	}

	if !shouldRefreshOpenAICodexSnapshot(&Account{}, &UsageInfo{FiveHour: nil, SevenDay: &UsageProgress{}}, now) {
		t.Fatal("expected missing 5h snapshot to require refresh")
	}

	staleAt := now.Add(-(openAIProbeCacheTTL + time.Minute)).Format(time.RFC3339)
	if !shouldRefreshOpenAICodexSnapshot(&Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Extra: map[string]any{
			"openai_oauth_responses_websockets_v2_enabled": true,
			"codex_usage_updated_at":                       staleAt,
		},
	}, usage, now) {
		t.Fatal("expected stale ws snapshot to trigger refresh")
	}
}

func TestAccountUsageServiceOpenAICodexProbeClientOptionsUsesAccountTLSProfile(t *testing.T) {
	const profileID int64 = 91
	account := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Extra:    map[string]any{"tls_fingerprint_profile_id": profileID},
	}
	svc := &AccountUsageService{
		tlsFPProfileService: &TLSFingerprintProfileService{localCache: map[int64]*model.TLSFingerprintProfile{
			profileID: {ID: profileID, Name: "usage probe override", CipherSuites: []uint16{0x1301}},
		}},
	}

	opts := svc.openAICodexProbeClientOptions(account, "http://proxy.local:8080")

	if opts.ProxyURL != "http://proxy.local:8080" {
		t.Fatalf("ProxyURL = %q, want configured proxy", opts.ProxyURL)
	}
	if opts.TLSProfile == nil {
		t.Fatal("expected usage probe client options to carry an OpenAI TLS profile")
	}
	if opts.TLSProfile.Name != "usage probe override" {
		t.Fatalf("TLS profile name = %q, want account override", opts.TLSProfile.Name)
	}
}

// TestShouldRefreshOpenAICodexSnapshot_SparkShadowIgnoresWSv2 外审第9轮 P1:spark 影子用量走
// QueryUsage(/wham/usage,与 WSv2 无关),staleness 不得被 WSv2 门控,否则首刷后窗口永久冻结。
func TestShouldRefreshOpenAICodexSnapshot_SparkShadowIgnoresWSv2(t *testing.T) {
	t.Parallel()

	now := time.Now()
	usage := &UsageInfo{
		FiveHour: &UsageProgress{Utilization: 0},
		SevenDay: &UsageProgress{Utilization: 0},
	}
	staleAt := now.Add(-(openAIProbeCacheTTL + time.Minute)).Format(time.RFC3339)
	freshAt := now.Add(-time.Minute).Format(time.RFC3339)
	parentID := int64(7001)

	// 影子无 WSv2,但首刷后窗口已存在;过期 codex_usage_updated_at 必须触发再刷新。
	shadowStale := &Account{
		Platform:        PlatformOpenAI,
		Type:            AccountTypeOAuth,
		ParentAccountID: &parentID,
		QuotaDimension:  QuotaDimensionSpark,
		Extra:           map[string]any{"codex_usage_updated_at": staleAt},
	}
	if !shouldRefreshOpenAICodexSnapshot(shadowStale, usage, now) {
		t.Fatal("expected stale spark shadow (no WSv2) to trigger refresh")
	}

	// 影子时间戳仍新鲜→不刷(TTL 生效)。
	shadowFresh := &Account{
		Platform:        PlatformOpenAI,
		Type:            AccountTypeOAuth,
		ParentAccountID: &parentID,
		QuotaDimension:  QuotaDimensionSpark,
		Extra:           map[string]any{"codex_usage_updated_at": freshAt},
	}
	if shouldRefreshOpenAICodexSnapshot(shadowFresh, usage, now) {
		t.Fatal("expected fresh spark shadow to skip refresh (TTL not elapsed)")
	}

	// 反向对照:普通账号无 WSv2 + 过期时间戳→仍不刷(WSv2 门控普通账号的 probe 刷新)。
	normalNoWS := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Extra:    map[string]any{"codex_usage_updated_at": staleAt},
	}
	if shouldRefreshOpenAICodexSnapshot(normalNoWS, usage, now) {
		t.Fatal("expected non-WSv2 normal account to skip codex probe refresh")
	}
}

func TestExtractOpenAICodexProbeUpdatesAccepts429WithCodexHeaders(t *testing.T) {
	t.Parallel()

	headers := make(http.Header)
	headers.Set("x-codex-primary-used-percent", "100")
	headers.Set("x-codex-primary-reset-after-seconds", "604800")
	headers.Set("x-codex-primary-window-minutes", "10080")
	headers.Set("x-codex-secondary-used-percent", "100")
	headers.Set("x-codex-secondary-reset-after-seconds", "18000")
	headers.Set("x-codex-secondary-window-minutes", "300")

	updates, err := extractOpenAICodexProbeUpdates(&http.Response{StatusCode: http.StatusTooManyRequests, Header: headers})
	if err != nil {
		t.Fatalf("extractOpenAICodexProbeUpdates() error = %v", err)
	}
	if len(updates) == 0 {
		t.Fatal("expected codex probe updates from 429 headers")
	}
	if got := updates["codex_5h_used_percent"]; got != 100.0 {
		t.Fatalf("codex_5h_used_percent = %v, want 100", got)
	}
	if got := updates["codex_7d_used_percent"]; got != 100.0 {
		t.Fatalf("codex_7d_used_percent = %v, want 100", got)
	}
}

func TestAccountUsageService_PersistOpenAICodexProbeSnapshotOnlyUpdatesExtra(t *testing.T) {
	t.Parallel()

	repo := &accountUsageCodexProbeRepo{
		updateExtraCh: make(chan map[string]any, 1),
		rateLimitCh:   make(chan time.Time, 1),
	}
	svc := &AccountUsageService{accountRepo: repo}
	svc.persistOpenAICodexProbeSnapshot(321, map[string]any{
		"codex_7d_used_percent": 100.0,
		"codex_7d_reset_at":     time.Now().Add(2 * time.Hour).UTC().Truncate(time.Second).Format(time.RFC3339),
	})

	select {
	case updates := <-repo.updateExtraCh:
		if got := updates["codex_7d_used_percent"]; got != 100.0 {
			t.Fatalf("codex_7d_used_percent = %v, want 100", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("等待 codex 探测快照写入 extra 超时")
	}

	select {
	case got := <-repo.rateLimitCh:
		t.Fatalf("不应将探测快照写入运行时限流状态: %v", got)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestAccountUsageService_GetOpenAIUsage_DoesNotPromoteCodexExtraToRateLimit(t *testing.T) {
	t.Parallel()

	resetAt := time.Now().Add(6 * 24 * time.Hour).UTC().Truncate(time.Second)
	repo := &accountUsageCodexProbeRepo{
		rateLimitCh: make(chan time.Time, 1),
	}
	svc := &AccountUsageService{accountRepo: repo}
	account := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Extra: map[string]any{
			"codex_5h_used_percent": 1.0,
			"codex_5h_reset_at":     time.Now().Add(2 * time.Hour).UTC().Truncate(time.Second).Format(time.RFC3339),
			"codex_7d_used_percent": 100.0,
			"codex_7d_reset_at":     resetAt.Format(time.RFC3339),
		},
	}

	usage, err := svc.getOpenAIUsage(context.Background(), account, false)
	if err != nil {
		t.Fatalf("getOpenAIUsage() error = %v", err)
	}
	if usage.SevenDay == nil || usage.SevenDay.Utilization != 100.0 {
		t.Fatalf("预期 7 天用量仍然可见，实际为 %#v", usage.SevenDay)
	}
	if account.RateLimitResetAt != nil {
		t.Fatalf("不应让已耗尽的 codex extra 改写运行时限流状态: %v", account.RateLimitResetAt)
	}
	select {
	case got := <-repo.rateLimitCh:
		t.Fatalf("不应将已耗尽的 codex extra 持久化为运行时限流状态: %v", got)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestBuildCodexUsageProgressFromExtra_ZerosExpiredWindow(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 3, 16, 12, 0, 0, 0, time.UTC)

	t.Run("expired 5h window zeroes utilization", func(t *testing.T) {
		extra := map[string]any{
			"codex_5h_used_percent": 42.0,
			"codex_5h_reset_at":     "2026-03-16T10:00:00Z", // 2h ago
		}
		progress := buildCodexUsageProgressFromExtra(extra, "5h", now)
		if progress == nil {
			t.Fatal("expected non-nil progress")
		}
		if progress.Utilization != 0 {
			t.Fatalf("expected Utilization=0 for expired window, got %v", progress.Utilization)
		}
		if progress.RemainingSeconds != 0 {
			t.Fatalf("expected RemainingSeconds=0, got %v", progress.RemainingSeconds)
		}
	})

	t.Run("active 5h window keeps utilization", func(t *testing.T) {
		resetAt := now.Add(2 * time.Hour).Format(time.RFC3339)
		extra := map[string]any{
			"codex_5h_used_percent": 42.0,
			"codex_5h_reset_at":     resetAt,
		}
		progress := buildCodexUsageProgressFromExtra(extra, "5h", now)
		if progress == nil {
			t.Fatal("expected non-nil progress")
		}
		if progress.Utilization != 42.0 {
			t.Fatalf("expected Utilization=42, got %v", progress.Utilization)
		}
	})

	t.Run("expired 7d window zeroes utilization", func(t *testing.T) {
		extra := map[string]any{
			"codex_7d_used_percent": 88.0,
			"codex_7d_reset_at":     "2026-03-15T00:00:00Z", // yesterday
		}
		progress := buildCodexUsageProgressFromExtra(extra, "7d", now)
		if progress == nil {
			t.Fatal("expected non-nil progress")
		}
		if progress.Utilization != 0 {
			t.Fatalf("expected Utilization=0 for expired 7d window, got %v", progress.Utilization)
		}
	})
}
