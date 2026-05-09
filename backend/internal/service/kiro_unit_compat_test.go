//go:build unit

package service

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

func isKiroInvalidModelIDBody(respBody []byte) bool {
	var payload struct {
		Reason  string `json:"reason"`
		Message string `json:"message"`
		Error   struct {
			Reason  string `json:"reason"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(respBody, &payload) != nil {
		return looksLikeKiroBadRequestInvalidModelError(strings.ToLower(string(respBody)))
	}
	return strings.EqualFold(strings.TrimSpace(payload.Reason), "INVALID_MODEL_ID") ||
		strings.EqualFold(strings.TrimSpace(payload.Error.Reason), "INVALID_MODEL_ID") ||
		looksLikeKiroBadRequestInvalidModelError(strings.ToLower(payload.Message)) ||
		looksLikeKiroBadRequestInvalidModelError(strings.ToLower(payload.Error.Message))
}

func buildKiroPayloadForAccount(ctx context.Context, account *Account, anthropicBody []byte, modelID, token, requestModel string, headers http.Header) ([]byte, error) {
	result, err := buildKiroPayloadForAccountWithRepo(ctx, nil, account, anthropicBody, modelID, token, requestModel, headers)
	if err != nil {
		return nil, err
	}
	return result.Payload, nil
}
