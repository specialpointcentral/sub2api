package service

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

func extractOpenAINonCodexPiBufferedTerminal(body []byte) (string, []byte) {
	var errorPayload []byte
	var terminalType string
	var terminalPayload []byte
	forEachOpenAISSEDataPayload(string(body), func(payload []byte) {
		eventType := strings.TrimSpace(gjson.GetBytes(payload, "type").String())
		if eventType == "error" {
			errorPayload = append(errorPayload[:0], payload...)
		}
		switch eventType {
		case "response.completed", "response.done", "response.failed", "response.incomplete", "response.cancelled", "response.canceled":
			terminalType = eventType
			terminalPayload = append(terminalPayload[:0], payload...)
		}
	})
	if terminalPayload != nil {
		return terminalType, terminalPayload
	}
	if errorPayload != nil {
		return "error", errorPayload
	}
	return "", nil
}

func (s *OpenAIGatewayService) prepareOpenAINonCodexPiBufferedResponse(
	c *gin.Context,
	account *Account,
	resp *http.Response,
	passthrough bool,
) error {
	if stagedOpenAINonCodexPiProjection(c) == nil || resp == nil || !isEventStreamResponse(resp.Header) {
		return nil
	}
	body, err := ReadUpstreamResponseBody(resp.Body, s.cfg, c, openAITooLargeError)
	if err != nil {
		return err
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))

	eventType, payload := extractOpenAINonCodexPiBufferedTerminal(body)
	switch eventType {
	case "response.completed", "response.done":
		return nil
	case "response.incomplete", "response.cancelled", "response.canceled":
		response := gjson.GetBytes(payload, "response")
		if response.Exists() && response.IsObject() && response.Raw != "" {
			resp.Body = io.NopCloser(strings.NewReader(response.Raw))
			resp.Header = resp.Header.Clone()
			resp.Header.Set("Content-Type", "application/json; charset=utf-8")
			return nil
		}
		message := fmt.Sprintf("OpenAI upstream returned %s without a response object", eventType)
		s.recordOpenAIStreamUpstreamError(c, account, passthrough, resp.Header.Get("x-request-id"), "http_error", payload, message)
		return s.writeOpenAINonStreamingProtocolError(resp, c, message)
	case "response.failed", "error":
		return s.handleOpenAINonCodexPiBufferedFailure(c, account, resp, passthrough, eventType, payload)
	default:
		message := "OpenAI stream ended before a terminal event"
		return s.newOpenAIStreamFailoverError(c, account, passthrough, resp.Header.Get("x-request-id"), nil, message, resp.Header)
	}
}

func (s *OpenAIGatewayService) handleOpenAINonCodexPiBufferedFailure(
	c *gin.Context,
	account *Account,
	resp *http.Response,
	passthrough bool,
	eventType string,
	payload []byte,
) error {
	message := extractOpenAISSEErrorMessage(payload)
	if message == "" {
		message = "OpenAI upstream response failed"
	}
	usage := &OpenAIUsage{}
	s.parseSSEUsageBytes(payload, usage)
	if hit, code, cyberMessage := detectOpenAICyberPolicy(payload); hit {
		MarkOpsCyberPolicy(c, CyberPolicyMark{
			Code:           code,
			Message:        cyberMessage,
			Body:           truncateString(string(payload), 4096),
			UpstreamStatus: http.StatusOK,
			UpstreamInTok:  usage.InputTokens,
			UpstreamOutTok: usage.OutputTokens,
		})
	}
	platform := ""
	if account != nil {
		platform = account.Platform
	}
	if status, errType, errMessage, matched := applyOpenAIStreamFailedErrorPassthroughRule(c, platform, payload, message); matched {
		s.recordOpenAIStreamUpstreamError(c, account, passthrough, resp.Header.Get("x-request-id"), "http_error", payload, message)
		MarkResponseCommitted(c)
		c.Header("Content-Type", "application/json; charset=utf-8")
		c.JSON(status, gin.H{"error": gin.H{"type": errType, "message": errMessage}})
		return fmt.Errorf("upstream %s event: passthrough rule matched message=%s", eventType, errMessage)
	}
	retryable := false
	if eventType == "response.failed" {
		retryable = openAIStreamFailedEventShouldFailover(payload, message)
	} else {
		retryable = openAIStreamErrorEventShouldFailover(payload, message) || isOpenAIUpstreamCapacityShedEvent(payload)
	}
	if retryable {
		return s.newOpenAIStreamFailoverError(c, account, passthrough, resp.Header.Get("x-request-id"), payload, message, resp.Header)
	}
	s.recordOpenAIStreamUpstreamError(c, account, passthrough, resp.Header.Get("x-request-id"), "http_error", payload, message)
	return s.writeOpenAINonStreamingProtocolError(resp, c, message)
}
