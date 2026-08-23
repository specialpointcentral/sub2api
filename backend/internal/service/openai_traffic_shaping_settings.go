package service

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultOpenAIRequestPacingMinIntervalMS = 250
	DefaultOpenAIRequestPacingMaxIntervalMS = 750
	DefaultOpenAIQuotaProbeIntervalMinutes  = 10
	DefaultOpenAIQuotaProbeJitterRatio      = 0.25
	MaxOpenAIRequestPacingIntervalMS        = 60_000
	MaxOpenAIAccountThreadConcurrencyLimit  = 10_000
	MaxOpenAIQuotaProbeIntervalMinutes      = 1_440
	MaxOpenAIQuotaProbeJitterRatio          = 0.5
	openAITrafficShapingCacheTTL            = 30 * time.Second
	openAITrafficShapingErrorTTL            = 5 * time.Second
)

var openAITrafficShapingSettingKeys = []string{
	SettingKeyOpenAIRequestPacingEnabled,
	SettingKeyOpenAIRequestPacingMinIntervalMS,
	SettingKeyOpenAIRequestPacingMaxIntervalMS,
	SettingKeyOpenAIAccountThreadConcurrencyLimit,
	SettingKeyOpenAIQuotaProbeIntervalMinutes,
	SettingKeyOpenAIQuotaProbeJitterRatio,
}

// OpenAITrafficShapingSettings is the fail-safe runtime snapshot shared by
// pacing, concurrency and quota probing. Invalid or unavailable storage values
// normalize to behavior-preserving defaults.
type OpenAITrafficShapingSettings struct {
	RequestPacingEnabled          bool
	RequestPacingMinIntervalMS    int
	RequestPacingMaxIntervalMS    int
	AccountThreadConcurrencyLimit int
	QuotaProbeIntervalMinutes     int
	QuotaProbeJitterRatio         float64
}

type cachedOpenAITrafficShapingSettings struct {
	settings  OpenAITrafficShapingSettings
	expiresAt int64
}

func defaultOpenAITrafficShapingSettings() OpenAITrafficShapingSettings {
	return OpenAITrafficShapingSettings{
		RequestPacingMinIntervalMS: DefaultOpenAIRequestPacingMinIntervalMS,
		RequestPacingMaxIntervalMS: DefaultOpenAIRequestPacingMaxIntervalMS,
		QuotaProbeIntervalMinutes:  DefaultOpenAIQuotaProbeIntervalMinutes,
		QuotaProbeJitterRatio:      DefaultOpenAIQuotaProbeJitterRatio,
	}
}

func normalizeOpenAITrafficShapingSettings(values map[string]string) OpenAITrafficShapingSettings {
	result := defaultOpenAITrafficShapingSettings()
	result.RequestPacingEnabled = strings.TrimSpace(values[SettingKeyOpenAIRequestPacingEnabled]) == "true"

	if value, err := strconv.Atoi(strings.TrimSpace(values[SettingKeyOpenAIRequestPacingMinIntervalMS])); err == nil && value >= 0 && value <= MaxOpenAIRequestPacingIntervalMS {
		result.RequestPacingMinIntervalMS = value
	}
	if value, err := strconv.Atoi(strings.TrimSpace(values[SettingKeyOpenAIRequestPacingMaxIntervalMS])); err == nil && value >= 0 && value <= MaxOpenAIRequestPacingIntervalMS {
		result.RequestPacingMaxIntervalMS = value
	}
	if result.RequestPacingMinIntervalMS > result.RequestPacingMaxIntervalMS {
		result.RequestPacingMinIntervalMS = DefaultOpenAIRequestPacingMinIntervalMS
		result.RequestPacingMaxIntervalMS = DefaultOpenAIRequestPacingMaxIntervalMS
	}
	if value, err := strconv.Atoi(strings.TrimSpace(values[SettingKeyOpenAIAccountThreadConcurrencyLimit])); err == nil && value >= 0 && value <= MaxOpenAIAccountThreadConcurrencyLimit {
		result.AccountThreadConcurrencyLimit = value
	}
	if value, err := strconv.Atoi(strings.TrimSpace(values[SettingKeyOpenAIQuotaProbeIntervalMinutes])); err == nil && value >= 1 && value <= MaxOpenAIQuotaProbeIntervalMinutes {
		result.QuotaProbeIntervalMinutes = value
	}
	if value, err := strconv.ParseFloat(strings.TrimSpace(values[SettingKeyOpenAIQuotaProbeJitterRatio]), 64); err == nil && value >= 0 && value <= MaxOpenAIQuotaProbeJitterRatio {
		result.QuotaProbeJitterRatio = value
	}
	return result
}

func openAITrafficShapingSettingsFromSystem(settings *SystemSettings) OpenAITrafficShapingSettings {
	if settings == nil {
		return defaultOpenAITrafficShapingSettings()
	}
	return normalizeOpenAITrafficShapingSettings(map[string]string{
		SettingKeyOpenAIRequestPacingEnabled:          strconv.FormatBool(settings.OpenAIRequestPacingEnabled),
		SettingKeyOpenAIRequestPacingMinIntervalMS:    strconv.Itoa(settings.OpenAIRequestPacingMinIntervalMS),
		SettingKeyOpenAIRequestPacingMaxIntervalMS:    strconv.Itoa(settings.OpenAIRequestPacingMaxIntervalMS),
		SettingKeyOpenAIAccountThreadConcurrencyLimit: strconv.Itoa(settings.OpenAIAccountThreadConcurrencyLimit),
		SettingKeyOpenAIQuotaProbeIntervalMinutes:     strconv.Itoa(settings.OpenAIQuotaProbeIntervalMinutes),
		SettingKeyOpenAIQuotaProbeJitterRatio:         strconv.FormatFloat(settings.OpenAIQuotaProbeJitterRatio, 'f', -1, 64),
	})
}

func loadOpenAITrafficShapingSettingValues(ctx context.Context, repo SettingRepository) (values map[string]string, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			values = nil
			err = fmt.Errorf("openai traffic shaping settings repository panic: %v", recovered)
		}
	}()
	return repo.GetMultiple(ctx, openAITrafficShapingSettingKeys)
}

func (s *SettingService) GetOpenAITrafficShapingSettings(ctx context.Context) OpenAITrafficShapingSettings {
	fallback := defaultOpenAITrafficShapingSettings()
	if s == nil || s.settingRepo == nil {
		return fallback
	}
	if cached, ok := s.openAITrafficShapingCache.Load().(*cachedOpenAITrafficShapingSettings); ok && cached != nil && time.Now().UnixNano() < cached.expiresAt {
		return cached.settings
	}
	result, _, _ := s.openAITrafficShapingSF.Do(SettingKeyOpenAIRequestPacingEnabled, func() (any, error) {
		if cached, ok := s.openAITrafficShapingCache.Load().(*cachedOpenAITrafficShapingSettings); ok && cached != nil && time.Now().UnixNano() < cached.expiresAt {
			return cached.settings, nil
		}
		dbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		values, err := loadOpenAITrafficShapingSettingValues(dbCtx, s.settingRepo)
		ttl := openAITrafficShapingCacheTTL
		settings := fallback
		if err != nil {
			slog.Warn("openai_traffic_shaping_settings_load_failed", "error", err)
			ttl = openAITrafficShapingErrorTTL
		} else {
			settings = normalizeOpenAITrafficShapingSettings(values)
		}
		s.openAITrafficShapingCache.Store(&cachedOpenAITrafficShapingSettings{
			settings: settings, expiresAt: time.Now().Add(ttl).UnixNano(),
		})
		return settings, err
	})
	if settings, ok := result.(OpenAITrafficShapingSettings); ok {
		return settings
	}
	return fallback
}
