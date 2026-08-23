package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestParseSettingsCodexRectifierDefaultsPreserveCurrentBehavior(t *testing.T) {
	settings := NewSettingService(nil, &config.Config{}).parseSettings(map[string]string{})

	require.Equal(t, 1, settings.OpenAICodexDevicePoolSize)
	require.Equal(t, "1:1:2", settings.OpenAICodexDevicePoolPlatformRatio)
	require.False(t, settings.OpenAICodexUAPersonaEnabled)
	require.Zero(t, settings.OpenAICodexVersionStaggerMaxHours)
}

func TestGetOpenAICodexDevicePoolSize(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  int
	}{
		{name: "missing preserves single device", want: 1},
		{name: "configured minimum", value: "3", want: 3},
		{name: "configured maximum", value: "8", want: 8},
		{name: "invalid low", value: "0", want: 1},
		{name: "disabled gap", value: "2", want: 1},
		{name: "invalid high", value: "9", want: 1},
		{name: "invalid text", value: "many", want: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := NewSettingService(&codexVersionSettingRepoStub{values: map[string]string{
				SettingKeyOpenAICodexDevicePoolSize: tt.value,
			}}, &config.Config{})
			require.Equal(t, tt.want, svc.GetOpenAICodexDevicePoolSize(context.Background()))
		})
	}
}

func TestGetOpenAICodexDevicePoolSizeRejectsInvalidCachedValue(t *testing.T) {
	svc := NewSettingService(&codexVersionSettingRepoStub{}, &config.Config{})
	svc.openAICodexDevicePoolCache.Store(&cachedOpenAICodexDevicePoolSize{
		value: 9, expiresAt: time.Now().Add(time.Minute).UnixNano(),
	})

	require.Equal(t, 1, svc.GetOpenAICodexDevicePoolSize(context.Background()))
}

func TestGetOpenAICodexDevicePoolPlatformRatio(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  codexDevicePlatformRatio
	}{
		{name: "missing uses default", want: codexDevicePlatformRatio{Mac: 1, Windows: 1, Linux: 2}},
		{name: "configured", value: "2:3:5", want: codexDevicePlatformRatio{Mac: 2, Windows: 3, Linux: 5}},
		{name: "invalid uses default", value: "1:0:2", want: codexDevicePlatformRatio{Mac: 1, Windows: 1, Linux: 2}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := NewSettingService(&codexVersionSettingRepoStub{values: map[string]string{
				SettingKeyOpenAICodexDevicePoolPlatformRatio: tt.value,
			}}, &config.Config{})
			require.Equal(t, tt.want, svc.GetOpenAICodexDevicePoolPlatformRatio(context.Background()))
		})
	}
}

func TestGetOpenAICodexUAPersonaEnabledDefaultsOff(t *testing.T) {
	for _, tt := range []struct {
		value string
		want  bool
	}{
		{value: "", want: false},
		{value: "false", want: false},
		{value: "true", want: true},
	} {
		svc := NewSettingService(&codexVersionSettingRepoStub{values: map[string]string{
			SettingKeyOpenAICodexUAPersonaEnabled: tt.value,
		}}, &config.Config{})
		require.Equal(t, tt.want, svc.GetOpenAICodexUAPersonaEnabled(context.Background()))
	}
}

func TestParseSettingsCodexRectifierValues(t *testing.T) {
	settings := NewSettingService(nil, &config.Config{}).parseSettings(map[string]string{
		SettingKeyOpenAICodexDevicePoolSize:          "8",
		SettingKeyOpenAICodexDevicePoolPlatformRatio: "2:3:5",
		SettingKeyOpenAICodexUAPersonaEnabled:        "true",
		SettingKeyOpenAICodexVersionStaggerMaxHours:  "36",
	})

	require.Equal(t, 8, settings.OpenAICodexDevicePoolSize)
	require.Equal(t, "2:3:5", settings.OpenAICodexDevicePoolPlatformRatio)
	require.True(t, settings.OpenAICodexUAPersonaEnabled)
	require.Equal(t, 36, settings.OpenAICodexVersionStaggerMaxHours)
}

func TestParseSettingsCodexVersionStaggerClampsAboveFortyEightHours(t *testing.T) {
	settings := NewSettingService(nil, &config.Config{}).parseSettings(map[string]string{
		SettingKeyOpenAICodexVersionStaggerMaxHours: "49",
	})

	require.Equal(t, 48, settings.OpenAICodexVersionStaggerMaxHours)
}
