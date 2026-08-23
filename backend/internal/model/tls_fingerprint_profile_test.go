package model

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTLSFingerprintProfileValidateRejectsH2CALPN(t *testing.T) {
	// Cleartext HTTP/2 can never be negotiated by the TLS-only upstream
	// transport, so h2c must fail closed at validation time instead of breaking
	// at runtime. Built-in presets (e.g. PresetChrome120HTTP1) are constructed
	// in code and never pass through this validation, so they are unaffected.
	for _, alpn := range [][]string{
		{"h2c"},
		{"H2C"},
		{"http/1.1", "h2c"},
	} {
		profile := &TLSFingerprintProfile{Name: "test", ALPNProtocols: alpn}
		err := profile.Validate()
		require.Error(t, err, "ALPN %v must be rejected", alpn)
		validationErr, ok := err.(*ValidationError)
		require.True(t, ok, "expected *ValidationError, got %T", err)
		require.Equal(t, "alpn_protocols", validationErr.Field)
	}
}

func TestTLSFingerprintProfileValidateAllowsSupportedALPN(t *testing.T) {
	// "h2" is supported via TLSRoundTripper's ALPN sniffing: the uTLS handshake
	// negotiates the protocol and the request is dispatched to http2.Transport
	// or http.Transport accordingly.
	for _, alpn := range [][]string{
		nil,
		{},
		{"http/1.1"},
		{"h2"},
		{"h2", "http/1.1"},
		{"http/1.1", "http/1.0"},
	} {
		profile := &TLSFingerprintProfile{Name: "test", ALPNProtocols: alpn}
		require.NoError(t, profile.Validate(), "ALPN %v must be accepted", alpn)
	}
}

func TestTLSFingerprintProfileValidateRequiresName(t *testing.T) {
	profile := &TLSFingerprintProfile{}
	err := profile.Validate()
	require.Error(t, err)
	validationErr, ok := err.(*ValidationError)
	require.True(t, ok)
	require.Equal(t, "name", validationErr.Field)
}
