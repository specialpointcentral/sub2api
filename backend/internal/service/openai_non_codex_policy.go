package service

import "strings"

type NonCodexTrafficPolicy string

const (
	NonCodexTrafficPolicyOff NonCodexTrafficPolicy = "off"
	NonCodexTrafficPolicyPi  NonCodexTrafficPolicy = "pi"

	DefaultOpenAINonCodexOriginator = "pi"
	DefaultOpenAINonCodexUserAgent  = "pi ({platform} {release}; {arch})"
	nonCodexTrafficPolicyExtraKey   = "non_codex_traffic_policy"
)

func NormalizeNonCodexTrafficPolicy(value string) (NonCodexTrafficPolicy, bool) {
	switch NonCodexTrafficPolicy(strings.ToLower(strings.TrimSpace(value))) {
	case NonCodexTrafficPolicyOff:
		return NonCodexTrafficPolicyOff, true
	case NonCodexTrafficPolicyPi:
		return NonCodexTrafficPolicyPi, true
	default:
		return NonCodexTrafficPolicyOff, false
	}
}

func isOpenAINonCodexTrafficAccount(account *Account) bool {
	return account != nil && (account.IsOpenAIOAuth() || account.IsOpenAIApiKey())
}

// GetOpenAINonCodexTrafficPolicy returns the account-scoped policy for OpenAI
// OAuth and API key accounts. Missing, null, legacy, and malformed values are
// intentionally tolerated as off so stale global settings cannot affect traffic.
func (a *Account) GetOpenAINonCodexTrafficPolicy() NonCodexTrafficPolicy {
	if !isOpenAINonCodexTrafficAccount(a) || a.Extra == nil {
		return NonCodexTrafficPolicyOff
	}
	raw, _ := a.Extra[nonCodexTrafficPolicyExtraKey].(string)
	policy, _ := NormalizeNonCodexTrafficPolicy(raw)
	return policy
}
