package handler

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type ModelRateLimitHandler struct {
	service     *service.ProactiveModelRateLimitService
	users       *service.UserService
	concurrency *service.ConcurrencyService
	userRPM     service.UserRPMCache
}

func NewModelRateLimitHandler(
	limiter *service.ProactiveModelRateLimitService,
	users *service.UserService,
	concurrency *service.ConcurrencyService,
	userRPM service.UserRPMCache,
) *ModelRateLimitHandler {
	return &ModelRateLimitHandler{service: limiter, users: users, concurrency: concurrency, userRPM: userRPM}
}

type modelRateLimitRulesRequest struct {
	Rules []service.ModelRateLimitRuleInput `json:"rules"`
}

type modelRateLimitRulesResponse struct {
	Rules     []service.ModelRateLimitRuleView `json:"rules"`
	UpdatedAt *time.Time                       `json:"updated_at"`
}

func (h *ModelRateLimitHandler) GetGlobalRules(c *gin.Context) { h.getRules(c, nil) }
func (h *ModelRateLimitHandler) PutGlobalRules(c *gin.Context) { h.putRules(c, nil) }

func (h *ModelRateLimitHandler) GetUserRules(c *gin.Context) {
	userID, ok := modelRateLimitUserIDParam(c)
	if !ok {
		return
	}
	if _, err := h.users.GetByID(c.Request.Context(), userID); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	h.getRules(c, &userID)
}

func (h *ModelRateLimitHandler) PutUserRules(c *gin.Context) {
	userID, ok := modelRateLimitUserIDParam(c)
	if !ok {
		return
	}
	if _, err := h.users.GetByID(c.Request.Context(), userID); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	h.putRules(c, &userID)
}

func (h *ModelRateLimitHandler) getRules(c *gin.Context, userID *int64) {
	rules, err := h.service.ListRules(c.Request.Context(), userID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, modelRateLimitRuleViews(rules))
}

func (h *ModelRateLimitHandler) putRules(c *gin.Context, userID *int64) {
	var request modelRateLimitRulesRequest
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		response.ErrorWithDetails(c, http.StatusBadRequest, "invalid model rate limit rules", "invalid_rule_payload", map[string]string{"detail": err.Error()})
		return
	}
	rules, err := h.service.ReplaceRules(c.Request.Context(), userID, request.Rules)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, modelRateLimitRuleViews(rules))
}

func modelRateLimitRuleViews(rules []service.ModelRateLimitRule) modelRateLimitRulesResponse {
	result := modelRateLimitRulesResponse{Rules: make([]service.ModelRateLimitRuleView, 0, len(rules))}
	for _, rule := range rules {
		result.Rules = append(result.Rules, rule.View())
		if result.UpdatedAt == nil || rule.UpdatedAt.After(*result.UpdatedAt) {
			updatedAt := rule.UpdatedAt
			result.UpdatedAt = &updatedAt
		}
	}
	return result
}

func modelRateLimitUserIDParam(c *gin.Context) (int64, bool) {
	raw := c.Param("id")
	if raw == "" {
		raw = c.Param("user_id")
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "invalid user ID")
		return 0, false
	}
	return id, true
}

func (h *ModelRateLimitHandler) Candidates(c *gin.Context) {
	models, err := h.service.Candidates(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, map[string]any{"models": models})
}

func (h *ModelRateLimitHandler) Snapshot(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	snapshot, err := h.snapshotForUser(c, subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	response.Success(c, snapshot)
}

func (h *ModelRateLimitHandler) State(c *gin.Context) {
	userID, err := strconv.ParseInt(c.Query("user_id"), 10, 64)
	if err != nil || userID <= 0 {
		response.BadRequest(c, "user_id is required")
		return
	}
	requestedModel := strings.TrimSpace(c.Query("model"))
	var requestedModels []string
	if requestedModel != "" {
		requestedModels = []string{requestedModel}
	}
	snapshot, err := h.snapshotForUser(c, userID, requestedModels...)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	models := make([]string, 0, len(snapshot.Models))
	for _, entry := range snapshot.Models {
		models = append(models, entry.Model)
	}
	recent, err := h.service.RecentTotals(c.Request.Context(), userID, models)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, map[string]any{"snapshot": snapshot, "recent_5m": recent})
}

func (h *ModelRateLimitHandler) snapshotForUser(c *gin.Context, userID int64, models ...string) (*service.ModelRateLimitSnapshot, error) {
	user, err := h.users.GetByID(c.Request.Context(), userID)
	if err != nil {
		return nil, err
	}
	usageAvailable := true
	var concurrencyUsed *int
	if h.concurrency != nil {
		loads, loadErr := h.concurrency.GetUsersLoadBatch(c.Request.Context(), []service.UserWithConcurrency{{ID: userID, MaxConcurrency: user.Concurrency}})
		if loadErr != nil {
			usageAvailable = false
		} else {
			used := 0
			if load := loads[userID]; load != nil {
				used = load.CurrentConcurrency
			}
			concurrencyUsed = &used
		}
	}
	var rpmUsed *int
	if user.RPMLimit > 0 && h.userRPM != nil {
		used, rpmErr := h.userRPM.GetUserRPM(c.Request.Context(), userID)
		if rpmErr != nil {
			usageAvailable = false
		} else {
			rpmUsed = &used
		}
	}
	if len(models) > 0 {
		return h.service.SnapshotForModels(c.Request.Context(), user, models, concurrencyUsed, rpmUsed, usageAvailable)
	}
	return h.service.Snapshot(c.Request.Context(), user, concurrencyUsed, rpmUsed, usageAvailable)
}
