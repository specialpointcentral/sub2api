package service

import "testing"

func TestBuildCodexCommonHeadersReusesCanonicalCodexIdentity(t *testing.T) {
	headers := buildCodexCommonHeaders("token", "account", false)
	wantUserAgent, wantOriginator := CodexCanonicalAuthIdentity()
	if got := headers["user-agent"]; got != wantUserAgent {
		t.Fatalf("user-agent = %q, want canonical %q", got, wantUserAgent)
	}
	if got := headers["originator"]; got != wantOriginator {
		t.Fatalf("originator = %q, want paired %q", got, wantOriginator)
	}
	if got, want := headers["version"], CodexCanonicalClientVersion(); got != want {
		t.Fatalf("version = %q, want canonical %q", got, want)
	}
}

func TestApplyCodexQuotaIdentityUsesAccountOverrideTriplet(t *testing.T) {
	account := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"user_agent": "codex_vscode/0.125.0 (Ubuntu 22.4.0; x86_64) vscode",
		},
	}
	headers := map[string]string{}

	applyCodexQuotaIdentity(headers, account)

	wantVersion := CodexCanonicalClientVersion()
	if got, want := headers["user-agent"], "codex_vscode/"+wantVersion+" (Ubuntu 22.4.0; x86_64) vscode"; got != want {
		t.Fatalf("user-agent = %q, want %q", got, want)
	}
	if got := headers["originator"]; got != "codex_vscode" {
		t.Fatalf("originator = %q, want codex_vscode", got)
	}
	if got := headers["version"]; got != wantVersion {
		t.Fatalf("version = %q, want %q", got, wantVersion)
	}
}
