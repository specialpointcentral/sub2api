package handler

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type proactiveModelRateLimitAdmitter interface {
	Admit(ctx context.Context, userID int64, model string) (*service.ModelRateLimitAdmission, error)
	HasEffectiveRules(ctx context.Context, userID int64) bool
}

const proactiveModelRateLimitPreAdmittedKey = "proactive_model_rate_limit_pre_admitted"

func admitProactiveModelRateLimit(c *gin.Context, admitter proactiveModelRateLimitAdmitter, userID int64, model string) (func(), bool) {
	release, _, admitted := admitProactiveModelRateLimitWithLifecycle(c, admitter, userID, model, true)
	return release, admitted
}

func admitProactiveModelRateLimitRaw(c *gin.Context, admitter proactiveModelRateLimitAdmitter, userID int64, model string) (func(), bool) {
	release, _, admitted := admitProactiveModelRateLimitWithLifecycle(c, admitter, userID, model, false)
	return release, admitted
}

func admitProactiveModelRateLimitRawDetailed(c *gin.Context, admitter proactiveModelRateLimitAdmitter, userID int64, model string) (func(), *service.ModelRateLimitAdmission, bool) {
	return admitProactiveModelRateLimitWithLifecycle(c, admitter, userID, model, false)
}

func admitProactiveModelRateLimitWithLifecycle(c *gin.Context, admitter proactiveModelRateLimitAdmitter, userID int64, model string, bindRequestContext bool) (func(), *service.ModelRateLimitAdmission, bool) {
	if c != nil && c.GetBool(proactiveModelRateLimitPreAdmittedKey) {
		return nil, nil, true
	}
	if admitter == nil || userID <= 0 || strings.TrimSpace(model) == "" {
		return nil, nil, true
	}
	admission, err := admitter.Admit(c.Request.Context(), userID, model)
	if err != nil {
		writeProactiveModelRateLimitError(c, http.StatusServiceUnavailable, "rate_limit_service_unavailable", "Per-model rate limit service is temporarily unavailable", nil)
		return nil, nil, false
	}
	if admission == nil || admission.Allowed {
		if admission == nil || admission.Release == nil {
			return nil, admission, true
		}
		if bindRequestContext {
			return wrapReleaseOnDone(c.Request.Context(), admission.Release), admission, true
		}
		return admission.Release, admission, true
	}
	retryAfter := admission.RetryAfterSeconds
	if retryAfter < 1 {
		retryAfter = 1
	}
	c.Header("Retry-After", strconv.Itoa(retryAfter))
	logProactiveModelRateLimitRejected(userID, admission, retryAfter)
	writeProactiveModelRateLimitError(c, http.StatusTooManyRequests, "model_rate_limit_exceeded", "Per-model user rate limit exceeded", admission)
	return nil, admission, false
}

func logProactiveModelRateLimitRejected(userID int64, admission *service.ModelRateLimitAdmission, retryAfter int) {
	if admission == nil {
		return
	}
	logger.L().Info("proactive_model_rate_limit_rejected",
		zap.Int64("user_id", userID),
		zap.String("model", admission.Model),
		zap.String("effective_model", admission.EffectiveModelKey),
		zap.String("matched_pattern", admission.MatchedPattern),
		zap.String("source", string(admission.Source)),
		zap.String("dimension", string(admission.Dimension)),
		zap.Int("used", admission.Used),
		zap.Int("limit", admission.Limit),
		zap.Int("retry_after_seconds", retryAfter),
	)
}

func writeProactiveModelRateLimitError(c *gin.Context, status int, code, message string, admission *service.ModelRateLimitAdmission) {
	path := c.Request.URL.Path
	switch {
	case strings.HasPrefix(path, "/v1beta/"):
		googleStatus := "RESOURCE_EXHAUSTED"
		if status == http.StatusServiceUnavailable {
			googleStatus = "UNAVAILABLE"
		}
		c.JSON(status, gin.H{"error": gin.H{"code": status, "message": message, "status": googleStatus}})
	case path == "/v1/messages" || strings.HasSuffix(path, "/messages"):
		errorType := "rate_limit_error"
		if status == http.StatusServiceUnavailable {
			errorType = "api_error"
		}
		c.JSON(status, gin.H{"type": "error", "error": gin.H{"type": errorType, "message": message}})
	default:
		param := any(nil)
		if admission != nil {
			param = "model"
		}
		errorType := "rate_limit_error"
		if status == http.StatusServiceUnavailable {
			errorType = "api_error"
		}
		c.JSON(status, gin.H{"error": gin.H{"type": errorType, "code": code, "param": param, "message": message}})
	}
}
