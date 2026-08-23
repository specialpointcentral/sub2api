package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestEffectiveOpenAIAccountConcurrency(t *testing.T) {
	oauth := func(concurrency int) *Account {
		return &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: concurrency}
	}

	tests := []struct {
		name        string
		account     *Account
		globalLimit int
		want        int
	}{
		{name: "disabled global limit", account: oauth(10), globalLimit: 0, want: 10},
		{name: "global limit supplies unbounded account", account: oauth(0), globalLimit: 4, want: 4},
		{name: "global limit supplies legacy negative account", account: oauth(-1), globalLimit: 4, want: 4},
		{name: "global limit caps account", account: oauth(10), globalLimit: 4, want: 4},
		{name: "stricter account wins", account: oauth(2), globalLimit: 4, want: 2},
		{name: "api key unchanged", account: &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Concurrency: 10}, globalLimit: 4, want: 10},
		{name: "grok unchanged", account: &Account{Platform: PlatformGrok, Type: AccountTypeOAuth, Concurrency: 10}, globalLimit: 4, want: 10},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, effectiveOpenAIAccountConcurrency(tt.account, tt.globalLimit))
		})
	}
}

func TestOpenAIGatewayServiceEffectiveOpenAIAccountConcurrencyReadsConfiguredCap(t *testing.T) {
	svc := &OpenAIGatewayService{settingService: NewSettingService(&codexVersionSettingRepoStub{values: map[string]string{
		SettingKeyOpenAIAccountThreadConcurrencyLimit: "4",
	}}, &config.Config{})}
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 10}

	require.Equal(t, 4, svc.EffectiveOpenAIAccountConcurrency(context.Background(), account))
	require.Equal(t, 4, svc.openAIWSAccountConcurrencyLimit(context.Background(), account))

	disabled := &OpenAIGatewayService{settingService: NewSettingService(&codexVersionSettingRepoStub{values: map[string]string{}}, &config.Config{})}
	require.Zero(t, disabled.openAIWSAccountConcurrencyLimit(context.Background(), account))
}

func TestEffectiveOpenAIAccountLoadFactorNeverExceedsSlotLimit(t *testing.T) {
	loadFactor := 12
	account := &Account{
		Platform: PlatformOpenAI, Type: AccountTypeOAuth,
		Concurrency: 10, LoadFactor: &loadFactor,
	}
	require.Equal(t, 4, effectiveOpenAIAccountLoadFactor(account, 4))
}
