//go:build unit

package service

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAccount_ShouldStripBillingHeader(t *testing.T) {
	tests := []struct {
		name        string
		credentials map[string]any
		expected    bool
	}{
		{
			name:        "nil credentials",
			credentials: nil,
			expected:    false,
		},
		{
			name:        "missing field",
			credentials: map[string]any{"access_token": "tok"},
			expected:    false,
		},
		{
			name:        "bool true",
			credentials: map[string]any{"strip_billing_header": true},
			expected:    true,
		},
		{
			name:        "bool false",
			credentials: map[string]any{"strip_billing_header": false},
			expected:    false,
		},
		{
			name:        "string true",
			credentials: map[string]any{"strip_billing_header": " true "},
			expected:    true,
		},
		{
			name:        "other type",
			credentials: map[string]any{"strip_billing_header": 1},
			expected:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			account := &Account{Credentials: tt.credentials}
			require.Equal(t, tt.expected, account.ShouldStripBillingHeader())
		})
	}
}

func TestStripBillingHeaderSystemText_StringSystemRemovesOnlyHeaderLines(t *testing.T) {
	body := []byte(`{
		"model":"claude-sonnet-4-5",
		"system":"x-anthropic-billing-header: cc_version=2.1.121; cch=36db8;\nKeep this system prompt.",
		"messages":[{"role":"user","content":"hi"}]
	}`)

	stripped, changed := stripBillingHeaderSystemText(body)
	require.True(t, changed)
	require.JSONEq(t, `{
		"model":"claude-sonnet-4-5",
		"system":"Keep this system prompt.",
		"messages":[{"role":"user","content":"hi"}]
	}`, string(stripped))
}

func TestStripBillingHeaderSystemText_StringSystemDeletesEmptySystem(t *testing.T) {
	body := []byte(`{
		"model":"claude-sonnet-4-5",
		"system":"x-anthropic-billing-header: cc_version=2.1.121; cch=36db8;",
		"messages":[{"role":"user","content":"hi"}]
	}`)

	stripped, changed := stripBillingHeaderSystemText(body)
	require.True(t, changed)
	require.False(t, jsonFieldExists(stripped, "system"))
	require.JSONEq(t, `{
		"model":"claude-sonnet-4-5",
		"messages":[{"role":"user","content":"hi"}]
	}`, string(stripped))
}

func TestStripBillingHeaderSystemText_ArraySystemRemovesHeaderBlocks(t *testing.T) {
	body := []byte(`{
		"model":"claude-sonnet-4-5",
		"system":[
			{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.121; cch=36db8;","cache_control":{"type":"ephemeral"}},
			{"type":"text","text":"Keep this system prompt.","cache_control":{"type":"ephemeral"}},
			{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.122; cch=abcde;\nKeep this line too."}
		],
		"messages":[{"role":"user","content":"hi"}]
	}`)

	stripped, changed := stripBillingHeaderSystemText(body)
	require.True(t, changed)
	require.JSONEq(t, `{
		"model":"claude-sonnet-4-5",
		"system":[
			{"type":"text","text":"Keep this system prompt.","cache_control":{"type":"ephemeral"}},
			{"type":"text","text":"Keep this line too."}
		],
		"messages":[{"role":"user","content":"hi"}]
	}`, string(stripped))
}

func TestStripBillingHeaderSystemText_DoesNotTouchMessages(t *testing.T) {
	body := []byte(`{
		"model":"claude-sonnet-4-5",
		"system":"Keep this system prompt.",
		"messages":[
			{"role":"user","content":"x-anthropic-billing-header: cc_version=2.1.121; cch=36db8;"}
		]
	}`)

	stripped, changed := stripBillingHeaderSystemText(body)
	require.False(t, changed)
	require.Equal(t, string(body), string(stripped))
}

func TestStripBillingHeaderSystemText_DoesNotRemoveNonHeaderSystemText(t *testing.T) {
	body := []byte(`{
		"model":"claude-sonnet-4-5",
		"system":[
			{"type":"text","text":"x-anthropic-billing-header is discussed here, but this is not the injected header."},
			{"type":"text","text":" x-anthropic-billing-header: cc_version=2.1.121.540; cc_entrypoint=claude-desktop-3p; cch=36db8;"}
		],
		"messages":[{"role":"user","content":"hi"}]
	}`)

	stripped, changed := stripBillingHeaderSystemText(body)
	require.True(t, changed)
	require.JSONEq(t, `{
		"model":"claude-sonnet-4-5",
		"system":[
			{"type":"text","text":"x-anthropic-billing-header is discussed here, but this is not the injected header."}
		],
		"messages":[{"role":"user","content":"hi"}]
	}`, string(stripped))
}

func TestStripBillingHeaderFromParsedHonorsAccountFlag(t *testing.T) {
	body := []byte(`{
		"model":"claude-sonnet-4-5",
		"system":"x-anthropic-billing-header: cc_version=2.1.121; cch=36db8;\nKeep this system prompt.",
		"messages":[{"role":"user","content":"hi"}]
	}`)
	parsed, err := ParseGatewayRequest(body, "")
	require.NoError(t, err)

	stripBillingHeaderFromParsed(&Account{Credentials: map[string]any{}}, parsed)
	require.Equal(t, string(body), string(parsed.Body))
	require.Equal(t, "x-anthropic-billing-header: cc_version=2.1.121; cch=36db8;\nKeep this system prompt.", parsed.System)

	stripBillingHeaderFromParsed(&Account{Credentials: map[string]any{"strip_billing_header": true}}, parsed)
	require.JSONEq(t, `{
		"model":"claude-sonnet-4-5",
		"system":"Keep this system prompt.",
		"messages":[{"role":"user","content":"hi"}]
	}`, string(parsed.Body))
	require.Equal(t, "Keep this system prompt.", parsed.System)
}

func jsonFieldExists(body []byte, field string) bool {
	var data map[string]any
	if err := json.Unmarshal(body, &data); err != nil {
		return false
	}
	_, ok := data[field]
	return ok
}
