package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	ModelRateLimitRPMWindowSeconds = 60
	modelRateLimitMaxRules         = 200
	modelRateLimitMaxPatternBytes  = 255
)

type ModelRateLimitSource string

const (
	ModelRateLimitSourceGlobal ModelRateLimitSource = "global"
	ModelRateLimitSourceUser   ModelRateLimitSource = "user"
)

type ModelRateLimitDimension string

const (
	ModelRateLimitDimensionConcurrency ModelRateLimitDimension = "concurrency"
	ModelRateLimitDimensionRPM         ModelRateLimitDimension = "rpm"
)

type ModelRateLimitRule struct {
	ID                int64     `json:"id"`
	UserID            *int64    `json:"user_id,omitempty"`
	ModelPattern      string    `json:"model_pattern"`
	NormalizedPattern string    `json:"normalized_pattern"`
	ConcurrencyLimit  int       `json:"concurrency_limit"`
	RPMLimit          int       `json:"rpm_limit"`
	TPMLimit          *int      `json:"tpm_limit"`
	CreatedAt         time.Time `json:"-"`
	UpdatedAt         time.Time `json:"-"`
}

type ModelRateLimitLimits struct {
	Concurrency int  `json:"concurrency"`
	RPM         int  `json:"rpm"`
	TPM         *int `json:"tpm"`
}

type ModelRateLimitWindows struct {
	RPMSeconds int  `json:"rpm_seconds"`
	TPMSeconds *int `json:"tpm_seconds"`
}

type ModelRateLimitRuleInput struct {
	ModelPattern string                 `json:"model_pattern"`
	Limits       ModelRateLimitLimits   `json:"limits"`
	Windows      *ModelRateLimitWindows `json:"windows,omitempty"`
}

type ModelRateLimitRuleView struct {
	ID           int64                 `json:"id"`
	ModelPattern string                `json:"model_pattern"`
	Limits       ModelRateLimitLimits  `json:"limits"`
	Windows      ModelRateLimitWindows `json:"windows"`
}

func (r ModelRateLimitRule) View() ModelRateLimitRuleView {
	return ModelRateLimitRuleView{
		ID:           r.ID,
		ModelPattern: r.ModelPattern,
		Limits: ModelRateLimitLimits{
			Concurrency: r.ConcurrencyLimit,
			RPM:         r.RPMLimit,
			TPM:         r.TPMLimit,
		},
		Windows: ModelRateLimitWindows{RPMSeconds: ModelRateLimitRPMWindowSeconds},
	}
}

type ResolvedModelRateLimit struct {
	Matched           bool
	RuleID            int64
	MatchedPattern    string
	Source            ModelRateLimitSource
	EffectiveModelKey string
	ConcurrencyLimit  int
	RPMLimit          int
}

type ModelRateLimitAdmission struct {
	Allowed           bool                    `json:"allowed"`
	Dimension         ModelRateLimitDimension `json:"dimension,omitempty"`
	Model             string                  `json:"model"`
	EffectiveModelKey string                  `json:"effective_model_key"`
	MatchedPattern    string                  `json:"matched_pattern"`
	Source            ModelRateLimitSource    `json:"source"`
	Used              int                     `json:"used"`
	Limit             int                     `json:"limit"`
	RetryAfterSeconds int                     `json:"retry_after_seconds"`
	RequestID         string                  `json:"-"`
	Release           func()                  `json:"-"`
}

type ModelRateLimitCacheAdmission struct {
	Allowed           bool
	Dimension         ModelRateLimitDimension
	Used              int
	RetryAfterSeconds int
}

type ModelRateLimitRuleStore interface {
	ListModelRateLimitRules(ctx context.Context, userID *int64) ([]ModelRateLimitRule, error)
	ReplaceModelRateLimitRules(ctx context.Context, userID *int64, rules []ModelRateLimitRule) ([]ModelRateLimitRule, error)
}

type ModelRateLimitConfigCache interface {
	LoadModelRateLimitRules(ctx context.Context, userID *int64) ([]ModelRateLimitRule, bool, error)
	StoreModelRateLimitRules(ctx context.Context, userID *int64, rules []ModelRateLimitRule) error
	PublishModelRateLimitInvalidation(ctx context.Context, userID *int64) error
}

type ModelRateLimitInvalidationSubscriber interface {
	SubscribeModelRateLimitInvalidations(handler func(userID *int64))
}

type ModelRateLimitCounterCache interface {
	AdmitModelRateLimit(ctx context.Context, userID int64, effectiveModel string, concurrencyLimit, rpmLimit int, requestID string, ruleID int64) (ModelRateLimitCacheAdmission, error)
	RefreshModelRateLimitConcurrency(ctx context.Context, userID int64, effectiveModel, requestID string) (bool, error)
	ReleaseModelRateLimit(ctx context.Context, userID int64, effectiveModel, requestID string) error
}

type ModelRateLimitCounterIdentity struct {
	EffectiveModel string
	RuleID         int64
}

type ModelRateLimitRecentTotals struct {
	Admitted int `json:"admitted"`
	Rejected int `json:"rejected"`
}

type ModelRateLimitRecentReader interface {
	GetRecentModelRateLimitTotals(ctx context.Context, userID int64, identities []ModelRateLimitCounterIdentity) (ModelRateLimitRecentTotals, error)
}

type ModelRateLimitUsageCounts struct {
	Concurrency          int
	RPM                  int
	RPMRetryAfterSeconds int
}

type ModelRateLimitUsageReader interface {
	GetModelRateLimitUsageBatch(ctx context.Context, userID int64, models []string) (map[string]ModelRateLimitUsageCounts, error)
}

type ModelRateLimitCandidateProvider interface {
	ModelRateLimitCandidates(ctx context.Context) ([]string, error)
}

type ModelRateLimitUserCandidateProvider interface {
	ModelRateLimitCandidatesForUser(ctx context.Context, userID int64) ([]string, error)
}

type ProactiveModelRateLimitService struct {
	store      ModelRateLimitRuleStore
	config     ModelRateLimitConfigCache
	counters   ModelRateLimitCounterCache
	candidates ModelRateLimitCandidateProvider

	mu                 sync.RWMutex
	local              map[string]cachedModelRateLimitRules
	cacheTTL           time.Duration
	candidateCache     cachedModelRateLimitCandidates
	userCandidateCache map[int64]cachedModelRateLimitCandidates
}

type cachedModelRateLimitRules struct {
	rules     []ModelRateLimitRule
	expiresAt time.Time
}

type cachedModelRateLimitCandidates struct {
	models    []string
	expiresAt time.Time
}

func NewProactiveModelRateLimitService(
	store ModelRateLimitRuleStore,
	config ModelRateLimitConfigCache,
	counters ModelRateLimitCounterCache,
	candidates ModelRateLimitCandidateProvider,
) *ProactiveModelRateLimitService {
	svc := &ProactiveModelRateLimitService{
		store: store, config: config, counters: counters, candidates: candidates,
		local: make(map[string]cachedModelRateLimitRules), userCandidateCache: make(map[int64]cachedModelRateLimitCandidates), cacheTTL: 30 * time.Second,
	}
	if subscriber, ok := config.(ModelRateLimitInvalidationSubscriber); ok {
		subscriber.SubscribeModelRateLimitInvalidations(svc.evict)
	}
	return svc
}

func (s *ProactiveModelRateLimitService) candidatesForUser(ctx context.Context, userID int64) ([]string, error) {
	provider, ok := s.candidates.(ModelRateLimitUserCandidateProvider)
	if !ok {
		return s.Candidates(ctx)
	}
	now := time.Now()
	s.mu.RLock()
	cached := s.userCandidateCache[userID]
	s.mu.RUnlock()
	if now.Before(cached.expiresAt) {
		return append([]string(nil), cached.models...), nil
	}
	models, err := provider.ModelRateLimitCandidatesForUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.userCandidateCache[userID] = cachedModelRateLimitCandidates{models: append([]string(nil), models...), expiresAt: now.Add(s.cacheTTL)}
	s.mu.Unlock()
	return models, nil
}

func ResolveModelRateLimit(requestedModel string, userRules, globalRules []ModelRateLimitRule) ResolvedModelRateLimit {
	literal := strings.ToLower(strings.TrimSpace(requestedModel))
	if literal == "" {
		return ResolvedModelRateLimit{}
	}
	canonical := normalizeKnownOpenAICodexModel(literal)
	if resolved, ok := resolveModelRateLimitScope(literal, canonical, userRules, ModelRateLimitSourceUser); ok {
		return resolved
	}
	if resolved, ok := resolveModelRateLimitScope(literal, canonical, globalRules, ModelRateLimitSourceGlobal); ok {
		return resolved
	}
	return ResolvedModelRateLimit{EffectiveModelKey: literal}
}

func resolveModelRateLimitScope(literal, canonical string, rules []ModelRateLimitRule, source ModelRateLimitSource) (ResolvedModelRateLimit, bool) {
	if rule := bestModelRateLimitRule(rules, literal); rule != nil {
		return resolvedModelRateLimit(*rule, source, literal), true
	}
	if canonical != "" && canonical != literal {
		if rule := bestModelRateLimitRule(rules, canonical); rule != nil {
			return resolvedModelRateLimit(*rule, source, canonical), true
		}
	}
	return ResolvedModelRateLimit{}, false
}

func resolvedModelRateLimit(rule ModelRateLimitRule, source ModelRateLimitSource, key string) ResolvedModelRateLimit {
	return ResolvedModelRateLimit{
		Matched: true, RuleID: rule.ID, MatchedPattern: rule.ModelPattern, Source: source,
		EffectiveModelKey: key, ConcurrencyLimit: rule.ConcurrencyLimit, RPMLimit: rule.RPMLimit,
	}
}

func bestModelRateLimitRule(rules []ModelRateLimitRule, model string) *ModelRateLimitRule {
	var matches []ModelRateLimitRule
	for _, rule := range rules {
		pattern := rule.NormalizedPattern
		if pattern == "" {
			pattern = strings.ToLower(strings.TrimSpace(rule.ModelPattern))
		}
		if pattern == model {
			copy := rule
			return &copy
		}
		if strings.Contains(pattern, "*") && matchStarPattern(pattern, model) {
			rule.NormalizedPattern = pattern
			matches = append(matches, rule)
		}
	}
	if len(matches) == 0 {
		return nil
	}
	sort.Slice(matches, func(i, j int) bool {
		a, b := matches[i].NormalizedPattern, matches[j].NormalizedPattern
		ap, bp := len(strings.SplitN(a, "*", 2)[0]), len(strings.SplitN(b, "*", 2)[0])
		if ap != bp {
			return ap > bp
		}
		aw, bw := strings.Count(a, "*"), strings.Count(b, "*")
		if aw != bw {
			return aw < bw
		}
		return a < b
	})
	return &matches[0]
}

func matchStarPattern(pattern, value string) bool {
	parts := strings.Split(pattern, "*")
	if len(parts) == 1 {
		return pattern == value
	}
	position := 0
	for i, part := range parts {
		if part == "" {
			continue
		}
		index := strings.Index(value[position:], part)
		if index < 0 || (i == 0 && index != 0) {
			return false
		}
		position += index + len(part)
	}
	return strings.HasSuffix(pattern, "*") || strings.HasSuffix(value, parts[len(parts)-1])
}

func (s *ProactiveModelRateLimitService) ListRules(ctx context.Context, userID *int64) ([]ModelRateLimitRule, error) {
	return s.loadRules(ctx, userID)
}

func (s *ProactiveModelRateLimitService) Candidates(ctx context.Context) ([]string, error) {
	if s == nil || s.candidates == nil {
		return nil, errors.New("model rate limit candidate provider is unavailable")
	}
	now := time.Now()
	s.mu.RLock()
	cached := s.candidateCache
	s.mu.RUnlock()
	if now.Before(cached.expiresAt) {
		return append([]string(nil), cached.models...), nil
	}
	models, err := s.candidates.ModelRateLimitCandidates(ctx)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.candidateCache = cachedModelRateLimitCandidates{models: append([]string(nil), models...), expiresAt: now.Add(s.cacheTTL)}
	s.mu.Unlock()
	return models, nil
}

func (s *ProactiveModelRateLimitService) ReplaceRules(ctx context.Context, userID *int64, inputs []ModelRateLimitRuleInput) ([]ModelRateLimitRule, error) {
	if s == nil || s.store == nil {
		return nil, errors.New("model rate limit rule store is unavailable")
	}
	rules, err := validateModelRateLimitRules(userID, inputs)
	if err != nil {
		return nil, err
	}
	replaced, err := s.store.ReplaceModelRateLimitRules(ctx, userID, rules)
	if err != nil {
		return nil, err
	}
	s.evict(userID)
	if s.config != nil {
		if err := s.config.StoreModelRateLimitRules(ctx, userID, replaced); err != nil {
			return nil, fmt.Errorf("cache model rate limit rules: %w", err)
		}
		if err := s.config.PublishModelRateLimitInvalidation(ctx, userID); err != nil {
			return nil, fmt.Errorf("publish model rate limit invalidation: %w", err)
		}
	}
	s.storeLocal(userID, replaced)
	return replaced, nil
}

func validateModelRateLimitRules(userID *int64, inputs []ModelRateLimitRuleInput) ([]ModelRateLimitRule, error) {
	if len(inputs) > modelRateLimitMaxRules {
		return nil, modelRateLimitValidationError("too_many_rules", "at most 200 model rate limit rules are allowed")
	}
	seen := make(map[string]struct{}, len(inputs))
	rules := make([]ModelRateLimitRule, 0, len(inputs))
	for i, input := range inputs {
		pattern := strings.TrimSpace(input.ModelPattern)
		normalized := strings.ToLower(pattern)
		if pattern == "" {
			return nil, modelRateLimitValidationError("empty_pattern", fmt.Sprintf("rule %d model_pattern is required", i+1))
		}
		if !utf8.ValidString(pattern) || len(pattern) > modelRateLimitMaxPatternBytes {
			return nil, modelRateLimitValidationError("invalid_pattern_length", fmt.Sprintf("rule %d model_pattern is too long", i+1))
		}
		if strings.ContainsAny(pattern, "?[]\\") {
			return nil, modelRateLimitValidationError("invalid_glob", fmt.Sprintf("rule %d only supports * as a wildcard", i+1))
		}
		if _, ok := seen[normalized]; ok {
			return nil, modelRateLimitValidationError("duplicate_pattern", fmt.Sprintf("rule %d duplicates model_pattern %q", i+1, pattern))
		}
		seen[normalized] = struct{}{}
		if input.Limits.Concurrency < 0 || input.Limits.RPM < 0 {
			return nil, modelRateLimitValidationError("negative_limit", fmt.Sprintf("rule %d limits must be non-negative integers", i+1))
		}
		if input.Limits.TPM != nil && *input.Limits.TPM != 0 {
			return nil, modelRateLimitValidationError("tpm_not_supported", "TPM enforcement is deferred to phase 2")
		}
		if input.Windows != nil {
			if input.Windows.RPMSeconds != 0 && input.Windows.RPMSeconds != ModelRateLimitRPMWindowSeconds {
				return nil, modelRateLimitValidationError("invalid_rpm_window", "RPM window must be 60 seconds")
			}
			if input.Windows.TPMSeconds != nil && *input.Windows.TPMSeconds != 0 {
				return nil, modelRateLimitValidationError("tpm_not_supported", "TPM enforcement is deferred to phase 2")
			}
		}
		rules = append(rules, ModelRateLimitRule{
			UserID: userID, ModelPattern: pattern, NormalizedPattern: normalized,
			ConcurrencyLimit: input.Limits.Concurrency, RPMLimit: input.Limits.RPM,
		})
	}
	return rules, nil
}

func modelRateLimitValidationError(reason, message string) error {
	return infraerrors.New(http.StatusBadRequest, reason, message)
}

func (s *ProactiveModelRateLimitService) Resolve(ctx context.Context, userID int64, model string) (ResolvedModelRateLimit, error) {
	userRules, err := s.loadRules(ctx, &userID)
	if err != nil {
		return ResolvedModelRateLimit{}, err
	}
	globalRules, err := s.loadRules(ctx, nil)
	if err != nil {
		return ResolvedModelRateLimit{}, err
	}
	return ResolveModelRateLimit(model, userRules, globalRules), nil
}

// HasEffectiveRules reports whether the current cached rule snapshots contain
// any finite per-model limit that can apply to the user. It deliberately avoids
// the rule-store fallback and never touches live counter state.
func (s *ProactiveModelRateLimitService) HasEffectiveRules(ctx context.Context, userID int64) bool {
	if s == nil || userID <= 0 {
		return false
	}
	userRules, _ := s.loadCachedRules(ctx, &userID)
	if hasFiniteModelRateLimitRule(userRules) {
		return true
	}
	globalRules, _ := s.loadCachedRules(ctx, nil)
	return hasFiniteModelRateLimitRule(globalRules)
}

func (s *ProactiveModelRateLimitService) loadCachedRules(ctx context.Context, userID *int64) ([]ModelRateLimitRule, bool) {
	key := modelRateLimitScopeKey(userID)
	now := time.Now()
	s.mu.RLock()
	local, ok := s.local[key]
	s.mu.RUnlock()
	if ok && now.Before(local.expiresAt) {
		return append([]ModelRateLimitRule(nil), local.rules...), true
	}
	if s.config == nil {
		return nil, false
	}
	rules, found, err := s.config.LoadModelRateLimitRules(ctx, userID)
	if err != nil || !found {
		return nil, false
	}
	s.storeLocal(userID, rules)
	return rules, true
}

func hasFiniteModelRateLimitRule(rules []ModelRateLimitRule) bool {
	for _, rule := range rules {
		if rule.ConcurrencyLimit > 0 || rule.RPMLimit > 0 {
			return true
		}
	}
	return false
}

func (s *ProactiveModelRateLimitService) Admit(ctx context.Context, userID int64, model string) (*ModelRateLimitAdmission, error) {
	resolved, err := s.Resolve(ctx, userID, model)
	if err != nil {
		return nil, err
	}
	if !resolved.Matched || (resolved.ConcurrencyLimit == 0 && resolved.RPMLimit == 0) {
		return &ModelRateLimitAdmission{Allowed: true, Model: strings.TrimSpace(model), EffectiveModelKey: resolved.EffectiveModelKey}, nil
	}
	if s.counters == nil {
		return nil, errors.New("model rate limit counter cache is unavailable")
	}
	requestID := generateRequestID()
	result, err := s.counters.AdmitModelRateLimit(ctx, userID, resolved.EffectiveModelKey, resolved.ConcurrencyLimit, resolved.RPMLimit, requestID, resolved.RuleID)
	if err != nil {
		return nil, err
	}
	limit := resolved.RPMLimit
	if result.Dimension == ModelRateLimitDimensionConcurrency {
		limit = resolved.ConcurrencyLimit
	}
	admission := &ModelRateLimitAdmission{
		Allowed: result.Allowed, Dimension: result.Dimension, Model: strings.TrimSpace(model),
		EffectiveModelKey: resolved.EffectiveModelKey, MatchedPattern: resolved.MatchedPattern,
		Source: resolved.Source, Used: result.Used, Limit: limit,
		RetryAfterSeconds: result.RetryAfterSeconds, RequestID: requestID,
	}
	if result.Allowed && resolved.ConcurrencyLimit > 0 {
		var once sync.Once
		admission.Release = func() {
			once.Do(func() {
				releaseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = s.counters.ReleaseModelRateLimit(releaseCtx, userID, resolved.EffectiveModelKey, requestID)
			})
		}
	}
	return admission, nil
}

func (s *ProactiveModelRateLimitService) RecentTotals(ctx context.Context, userID int64, models []string) (ModelRateLimitRecentTotals, error) {
	reader, ok := s.counters.(ModelRateLimitRecentReader)
	if !ok {
		return ModelRateLimitRecentTotals{}, errors.New("model rate limit state is unavailable")
	}
	userRules, err := s.loadRules(ctx, &userID)
	if err != nil {
		return ModelRateLimitRecentTotals{}, err
	}
	globalRules, err := s.loadRules(ctx, nil)
	if err != nil {
		return ModelRateLimitRecentTotals{}, err
	}
	identities := make([]ModelRateLimitCounterIdentity, 0, len(models))
	seen := make(map[string]struct{})
	for _, model := range models {
		resolved := ResolveModelRateLimit(model, userRules, globalRules)
		if !resolved.Matched {
			continue
		}
		key := fmt.Sprintf("%d:%s", resolved.RuleID, resolved.EffectiveModelKey)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		identities = append(identities, ModelRateLimitCounterIdentity{EffectiveModel: resolved.EffectiveModelKey, RuleID: resolved.RuleID})
	}
	return reader.GetRecentModelRateLimitTotals(ctx, userID, identities)
}

func (s *ProactiveModelRateLimitService) loadRules(ctx context.Context, userID *int64) ([]ModelRateLimitRule, error) {
	key := modelRateLimitScopeKey(userID)
	now := time.Now()
	s.mu.RLock()
	local, ok := s.local[key]
	s.mu.RUnlock()
	if ok && now.Before(local.expiresAt) {
		return append([]ModelRateLimitRule(nil), local.rules...), nil
	}
	if s.config != nil {
		if rules, found, err := s.config.LoadModelRateLimitRules(ctx, userID); err == nil && found {
			s.storeLocal(userID, rules)
			return rules, nil
		}
	}
	if s.store == nil {
		return nil, errors.New("model rate limit rule store is unavailable")
	}
	rules, err := s.store.ListModelRateLimitRules(ctx, userID)
	if err != nil {
		return nil, err
	}
	if s.config != nil {
		_ = s.config.StoreModelRateLimitRules(ctx, userID, rules)
	}
	s.storeLocal(userID, rules)
	return rules, nil
}

func (s *ProactiveModelRateLimitService) storeLocal(userID *int64, rules []ModelRateLimitRule) {
	s.mu.Lock()
	s.local[modelRateLimitScopeKey(userID)] = cachedModelRateLimitRules{rules: append([]ModelRateLimitRule(nil), rules...), expiresAt: time.Now().Add(s.cacheTTL)}
	s.mu.Unlock()
}

func (s *ProactiveModelRateLimitService) evict(userID *int64) {
	s.mu.Lock()
	delete(s.local, modelRateLimitScopeKey(userID))
	s.mu.Unlock()
}

func modelRateLimitScopeKey(userID *int64) string {
	if userID == nil {
		return "global"
	}
	return fmt.Sprintf("user:%d", *userID)
}

type ModelRateLimitUsage struct {
	Used              *int     `json:"used"`
	Limit             *int     `json:"limit"`
	WindowSeconds     *int     `json:"window_seconds,omitempty"`
	RetryAfterSeconds *int     `json:"retry_after_seconds,omitempty"`
	Utilization       *float64 `json:"utilization"`
	Saturated         bool     `json:"saturated"`
}

type ModelRateLimitSnapshotDimensions struct {
	Concurrency *ModelRateLimitUsage `json:"concurrency,omitempty"`
	RPM         *ModelRateLimitUsage `json:"rpm,omitempty"`
	TPM         *ModelRateLimitUsage `json:"tpm,omitempty"`
}

type ModelRateLimitSnapshotModel struct {
	Model          string                           `json:"model"`
	MatchedPattern string                           `json:"matched_pattern"`
	Source         ModelRateLimitSource             `json:"source"`
	Dimensions     ModelRateLimitSnapshotDimensions `json:"dimensions"`
}

type ModelRateLimitSaturated struct {
	Model     string                  `json:"model"`
	Dimension ModelRateLimitDimension `json:"dimension"`
}

type ModelRateLimitSnapshot struct {
	GeneratedAt        time.Time                     `json:"generated_at"`
	RefreshAfterMS     int                           `json:"refresh_after_ms"`
	OverallConcurrency ModelRateLimitUsage           `json:"overall_concurrency"`
	OverallRPM         *ModelRateLimitUsage          `json:"overall_rpm,omitempty"`
	Models             []ModelRateLimitSnapshotModel `json:"models"`
	Saturated          []ModelRateLimitSaturated     `json:"saturated"`
	UsageAvailable     bool                          `json:"usage_available"`
}

func (s *ProactiveModelRateLimitService) Snapshot(
	ctx context.Context,
	user *User,
	overallConcurrencyUsed, overallRPMUsed *int,
	usageAvailable bool,
) (*ModelRateLimitSnapshot, error) {
	if user == nil {
		return nil, ErrUserNotFound
	}
	models, err := s.candidatesForUser(ctx, user.ID)
	if err != nil {
		return nil, err
	}
	return s.snapshotForModels(ctx, user, models, overallConcurrencyUsed, overallRPMUsed, usageAvailable, false)
}

func (s *ProactiveModelRateLimitService) SnapshotForModels(
	ctx context.Context,
	user *User,
	models []string,
	overallConcurrencyUsed, overallRPMUsed *int,
	usageAvailable bool,
) (*ModelRateLimitSnapshot, error) {
	if user == nil {
		return nil, ErrUserNotFound
	}
	return s.snapshotForModels(ctx, user, models, overallConcurrencyUsed, overallRPMUsed, usageAvailable, true)
}

func (s *ProactiveModelRateLimitService) snapshotForModels(
	ctx context.Context,
	user *User,
	models []string,
	overallConcurrencyUsed, overallRPMUsed *int,
	usageAvailable bool,
	includeUnlimitedMatched bool,
) (*ModelRateLimitSnapshot, error) {
	userRules, err := s.loadRules(ctx, &user.ID)
	if err != nil {
		return nil, err
	}
	globalRules, err := s.loadRules(ctx, nil)
	if err != nil {
		return nil, err
	}

	type effectiveModel struct {
		model    string
		resolved ResolvedModelRateLimit
	}
	effective := make([]effectiveModel, 0)
	keys := make([]string, 0)
	seenKeys := make(map[string]struct{})
	for _, model := range models {
		resolved := ResolveModelRateLimit(model, userRules, globalRules)
		if !resolved.Matched || (!includeUnlimitedMatched && resolved.ConcurrencyLimit <= 0 && resolved.RPMLimit <= 0) {
			continue
		}
		effective = append(effective, effectiveModel{model: model, resolved: resolved})
		if _, ok := seenKeys[resolved.EffectiveModelKey]; !ok {
			seenKeys[resolved.EffectiveModelKey] = struct{}{}
			keys = append(keys, resolved.EffectiveModelKey)
		}
	}

	counts := make(map[string]ModelRateLimitUsageCounts)
	if len(keys) > 0 && usageAvailable {
		reader, ok := s.counters.(ModelRateLimitUsageReader)
		if !ok {
			usageAvailable = false
		} else if counts, err = reader.GetModelRateLimitUsageBatch(ctx, user.ID, keys); err != nil {
			usageAvailable = false
		}
	}
	if !usageAvailable {
		overallConcurrencyUsed = nil
		overallRPMUsed = nil
	}

	snapshot := &ModelRateLimitSnapshot{
		GeneratedAt: time.Now().UTC(), RefreshAfterMS: 5000,
		OverallConcurrency: *newModelRateLimitUsage(overallConcurrencyUsed, nullablePositiveLimit(user.Concurrency), 0, 0),
		Models:             make([]ModelRateLimitSnapshotModel, 0, len(effective)),
		Saturated:          make([]ModelRateLimitSaturated, 0),
		UsageAvailable:     usageAvailable,
	}
	if user.RPMLimit > 0 {
		snapshot.OverallRPM = newModelRateLimitUsage(overallRPMUsed, intValuePointer(user.RPMLimit), ModelRateLimitRPMWindowSeconds, 0)
	}
	for _, item := range effective {
		count := counts[item.resolved.EffectiveModelKey]
		entry := ModelRateLimitSnapshotModel{
			Model: item.model, MatchedPattern: item.resolved.MatchedPattern, Source: item.resolved.Source,
		}
		if item.resolved.ConcurrencyLimit > 0 {
			var used *int
			if usageAvailable {
				used = intValuePointer(count.Concurrency)
			}
			entry.Dimensions.Concurrency = newModelRateLimitUsage(used, intValuePointer(item.resolved.ConcurrencyLimit), 0, 0)
			if entry.Dimensions.Concurrency.Saturated {
				snapshot.Saturated = append(snapshot.Saturated, ModelRateLimitSaturated{Model: item.model, Dimension: ModelRateLimitDimensionConcurrency})
			}
		}
		if item.resolved.RPMLimit > 0 {
			var used *int
			if usageAvailable {
				used = intValuePointer(count.RPM)
			}
			entry.Dimensions.RPM = newModelRateLimitUsage(used, intValuePointer(item.resolved.RPMLimit), ModelRateLimitRPMWindowSeconds, count.RPMRetryAfterSeconds)
			if entry.Dimensions.RPM.Saturated {
				snapshot.Saturated = append(snapshot.Saturated, ModelRateLimitSaturated{Model: item.model, Dimension: ModelRateLimitDimensionRPM})
			}
		}
		snapshot.Models = append(snapshot.Models, entry)
	}
	if snapshot.OverallConcurrency.Saturated {
		snapshot.Saturated = append(snapshot.Saturated, ModelRateLimitSaturated{Dimension: ModelRateLimitDimensionConcurrency})
	}
	if snapshot.OverallRPM != nil && snapshot.OverallRPM.Saturated {
		snapshot.Saturated = append(snapshot.Saturated, ModelRateLimitSaturated{Dimension: ModelRateLimitDimensionRPM})
	}
	return snapshot, nil
}

func newModelRateLimitUsage(used, limit *int, windowSeconds, retryAfter int) *ModelRateLimitUsage {
	usage := &ModelRateLimitUsage{Used: used, Limit: limit}
	if windowSeconds > 0 {
		usage.WindowSeconds = intValuePointer(windowSeconds)
		usage.RetryAfterSeconds = intValuePointer(retryAfter)
	}
	if used != nil && limit != nil && *limit > 0 {
		value := float64(*used) * 100 / float64(*limit)
		usage.Utilization = &value
		usage.Saturated = *used >= *limit
	}
	return usage
}

func nullablePositiveLimit(value int) *int {
	if value <= 0 {
		return nil
	}
	return intValuePointer(value)
}

func intValuePointer(value int) *int { return &value }
