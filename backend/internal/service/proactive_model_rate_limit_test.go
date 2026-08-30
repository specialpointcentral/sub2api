//go:build unit

package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProactiveModelRateLimitResolver(t *testing.T) {
	global := []ModelRateLimitRule{
		{ID: 1, ModelPattern: "claude-*", ConcurrencyLimit: 2, RPMLimit: 100},
		{ID: 2, ModelPattern: "claude-opus-*", ConcurrencyLimit: 1, RPMLimit: 20},
		{ID: 3, ModelPattern: "gpt-5.6-luna", ConcurrencyLimit: 4, RPMLimit: 40},
		{ID: 4, ModelPattern: "gpt-*-luna*", ConcurrencyLimit: 5, RPMLimit: 50},
		{ID: 5, ModelPattern: "gpt-5.6-*", ConcurrencyLimit: 6, RPMLimit: 60},
	}
	user := []ModelRateLimitRule{
		{ID: 10, UserID: int64Pointer(7), ModelPattern: "claude-*", ConcurrencyLimit: 0, RPMLimit: 10},
		{ID: 11, UserID: int64Pointer(7), ModelPattern: "gpt-5.6-luna-high", ConcurrencyLimit: 0, RPMLimit: 0},
	}

	tests := []struct {
		name        string
		model       string
		userRules   []ModelRateLimitRule
		wantPattern string
		wantSource  ModelRateLimitSource
		wantKey     string
		wantConc    int
		wantRPM     int
		wantMatched bool
	}{
		{name: "literal variant beats canonical base", model: "GPT-5.6-LUNA-HIGH", userRules: user, wantPattern: "gpt-5.6-luna-high", wantSource: ModelRateLimitSourceUser, wantKey: "gpt-5.6-luna-high", wantMatched: true},
		{name: "user broad row replaces more specific global row as a whole", model: "claude-opus-4-1", userRules: user, wantPattern: "claude-*", wantSource: ModelRateLimitSourceUser, wantKey: "claude-opus-4-1", wantRPM: 10, wantMatched: true},
		{name: "longest glob literal prefix wins deterministically", model: "claude-opus-4-1", wantPattern: "claude-opus-*", wantSource: ModelRateLimitSourceGlobal, wantKey: "claude-opus-4-1", wantConc: 1, wantRPM: 20, wantMatched: true},
		{name: "fewer wildcards breaks equal-prefix overlap", model: "gpt-5.6-luna-fast", wantPattern: "gpt-5.6-*", wantSource: ModelRateLimitSourceGlobal, wantKey: "gpt-5.6-luna-fast", wantConc: 6, wantRPM: 60, wantMatched: true},
		{name: "known suffix falls back to canonical exact", model: "openai/gpt-5.6-luna-xhigh", wantPattern: "gpt-5.6-luna", wantSource: ModelRateLimitSourceGlobal, wantKey: "gpt-5.6-luna", wantConc: 4, wantRPM: 40, wantMatched: true},
		{name: "user canonical exemption beats global literal match", model: "openai/gpt-5.6-luna-xhigh", userRules: []ModelRateLimitRule{{ID: 12, UserID: int64Pointer(7), ModelPattern: "gpt-5.6-luna"}}, wantPattern: "gpt-5.6-luna", wantSource: ModelRateLimitSourceUser, wantKey: "gpt-5.6-luna", wantMatched: true},
		{name: "no match is unlimited", model: "gemini-3-pro", wantKey: "gemini-3-pro"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolveModelRateLimit(tt.model, tt.userRules, global)
			require.Equal(t, tt.wantMatched, got.Matched)
			require.Equal(t, tt.wantPattern, got.MatchedPattern)
			require.Equal(t, tt.wantSource, got.Source)
			require.Equal(t, tt.wantKey, got.EffectiveModelKey)
			require.Equal(t, tt.wantConc, got.ConcurrencyLimit)
			require.Equal(t, tt.wantRPM, got.RPMLimit)
		})
	}
}

type modelRateLimitRuleStoreStub struct {
	rules        []ModelRateLimitRule
	replaceCalls int
}

func (s *modelRateLimitRuleStoreStub) ListModelRateLimitRules(context.Context, *int64) ([]ModelRateLimitRule, error) {
	return append([]ModelRateLimitRule(nil), s.rules...), nil
}

func (s *modelRateLimitRuleStoreStub) ReplaceModelRateLimitRules(_ context.Context, _ *int64, rules []ModelRateLimitRule) ([]ModelRateLimitRule, error) {
	s.replaceCalls++
	s.rules = append([]ModelRateLimitRule(nil), rules...)
	return append([]ModelRateLimitRule(nil), rules...), nil
}

type modelRateLimitConfigCacheStub struct{ invalidations int }

func (s *modelRateLimitConfigCacheStub) LoadModelRateLimitRules(context.Context, *int64) ([]ModelRateLimitRule, bool, error) {
	return nil, false, nil
}
func (s *modelRateLimitConfigCacheStub) StoreModelRateLimitRules(context.Context, *int64, []ModelRateLimitRule) error {
	return nil
}
func (s *modelRateLimitConfigCacheStub) PublishModelRateLimitInvalidation(context.Context, *int64) error {
	s.invalidations++
	return nil
}

func TestProactiveModelRateLimitAdminValidationAndAtomicReplace(t *testing.T) {
	store := &modelRateLimitRuleStoreStub{rules: []ModelRateLimitRule{{ID: 9, ModelPattern: "old-*", ConcurrencyLimit: 1}}}
	cache := &modelRateLimitConfigCacheStub{}
	svc := NewProactiveModelRateLimitService(store, cache, nil, nil)
	t.Run("non-integer JSON limit", func(t *testing.T) {
		var payload struct {
			Rules []ModelRateLimitRuleInput `json:"rules"`
		}
		decoder := json.NewDecoder(strings.NewReader(`{"rules":[{"model_pattern":"a","limits":{"rpm":1.5}}]}`))
		require.Error(t, decoder.Decode(&payload))
		require.Equal(t, 0, store.replaceCalls)
	})

	invalid := []struct {
		name  string
		rules []ModelRateLimitRuleInput
	}{
		{name: "empty pattern", rules: []ModelRateLimitRuleInput{{ModelPattern: " ", Limits: ModelRateLimitLimits{Concurrency: 1}}}},
		{name: "negative", rules: []ModelRateLimitRuleInput{{ModelPattern: "a", Limits: ModelRateLimitLimits{RPM: -1}}}},
		{name: "bad glob", rules: []ModelRateLimitRuleInput{{ModelPattern: "a?[b]", Limits: ModelRateLimitLimits{RPM: 1}}}},
		{name: "case insensitive duplicate", rules: []ModelRateLimitRuleInput{{ModelPattern: "A-*"}, {ModelPattern: "a-*"}}},
		{name: "phase one rejects tpm", rules: []ModelRateLimitRuleInput{{ModelPattern: "a", Limits: ModelRateLimitLimits{TPM: intPointer(1)}}}},
	}
	for _, tt := range invalid {
		t.Run(tt.name, func(t *testing.T) {
			_, err := svc.ReplaceRules(context.Background(), nil, tt.rules)
			require.Error(t, err)
			require.Equal(t, 0, store.replaceCalls)
			require.Equal(t, "old-*", store.rules[0].ModelPattern)
		})
	}

	got, err := svc.ReplaceRules(context.Background(), nil, []ModelRateLimitRuleInput{
		{ModelPattern: " Claude-* ", Limits: ModelRateLimitLimits{Concurrency: 3, RPM: 60}},
		{ModelPattern: "gpt-5.6-luna-high", Limits: ModelRateLimitLimits{}},
	})
	require.NoError(t, err)
	require.Equal(t, 1, store.replaceCalls)
	require.Equal(t, 1, cache.invalidations)
	require.Len(t, got, 2)
	require.Equal(t, "Claude-*", got[0].ModelPattern)
	require.Equal(t, "claude-*", got[0].NormalizedPattern)
	require.Zero(t, got[1].ConcurrencyLimit)
	require.Zero(t, got[1].RPMLimit)
}

func intPointer(v int) *int       { return &v }
func int64Pointer(v int64) *int64 { return &v }
