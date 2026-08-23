package service

import "context"

// effectiveOpenAIAccountConcurrency composes the administrator-wide OAuth
// turn cap with the account's existing slot limit. Zero keeps the legacy
// unlimited meaning unless the global cap supplies a bound.
func effectiveOpenAIAccountConcurrency(account *Account, globalLimit int) int {
	if account == nil {
		return 0
	}
	accountLimit := account.Concurrency
	if !account.IsOpenAIOAuth() || globalLimit <= 0 {
		return accountLimit
	}
	if accountLimit <= 0 || globalLimit < accountLimit {
		return globalLimit
	}
	return accountLimit
}

func effectiveOpenAIAccountLoadFactor(account *Account, globalLimit int) int {
	if account == nil {
		return 1
	}
	loadFactor := account.EffectiveLoadFactor()
	slotLimit := effectiveOpenAIAccountConcurrency(account, globalLimit)
	if slotLimit > 0 && loadFactor > slotLimit {
		return slotLimit
	}
	return loadFactor
}

func (s *OpenAIGatewayService) openAIAccountThreadLimit(ctx context.Context, account *Account) int {
	if s == nil || s.settingService == nil || account == nil || !account.IsOpenAIOAuth() {
		return 0
	}
	return s.settingService.GetOpenAITrafficShapingSettings(ctx).AccountThreadConcurrencyLimit
}

func (s *OpenAIGatewayService) effectiveOpenAIAccountConcurrency(ctx context.Context, account *Account) int {
	return effectiveOpenAIAccountConcurrency(account, s.openAIAccountThreadLimit(ctx, account))
}

// openAIWSAccountConcurrencyLimit returns only an administrator-enabled
// traffic-shaping cap. Zero leaves the WebSocket pool's legacy dynamic/hard-cap
// policy unchanged when the new account-thread setting is disabled.
func (s *OpenAIGatewayService) openAIWSAccountConcurrencyLimit(ctx context.Context, account *Account) int {
	globalLimit := s.openAIAccountThreadLimit(ctx, account)
	if globalLimit <= 0 {
		return 0
	}
	return effectiveOpenAIAccountConcurrency(account, globalLimit)
}

// EffectiveOpenAIAccountConcurrency exposes the exact Redis slot limit to the
// handler's per-turn WebSocket reacquisition path.
func (s *OpenAIGatewayService) EffectiveOpenAIAccountConcurrency(ctx context.Context, account *Account) int {
	return s.effectiveOpenAIAccountConcurrency(ctx, account)
}

func (s *OpenAIGatewayService) effectiveOpenAIAccountLoadFactor(ctx context.Context, account *Account) int {
	return effectiveOpenAIAccountLoadFactor(account, s.openAIAccountThreadLimit(ctx, account))
}
