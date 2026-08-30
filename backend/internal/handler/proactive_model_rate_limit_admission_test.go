//go:build unit

package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type denyingModelRateLimitAdmitter struct {
	forwardCalls      int
	accountMutations  int
	runtimeBlockCalls int
}

func (s *denyingModelRateLimitAdmitter) Admit(context.Context, int64, string) (*service.ModelRateLimitAdmission, error) {
	return &service.ModelRateLimitAdmission{
		Allowed:           false,
		Dimension:         service.ModelRateLimitDimensionRPM,
		Model:             "gpt-5.6-luna-high",
		Used:              30,
		Limit:             30,
		RetryAfterSeconds: 17,
	}, nil
}

func (s *denyingModelRateLimitAdmitter) HasEffectiveRules(context.Context, int64) bool {
	return true
}

func (s *denyingModelRateLimitAdmitter) downstream() {
	s.forwardCalls++
	s.accountMutations++
	s.runtimeBlockCalls++
}

type admissionRuleSnapshotCache struct {
	userRules   map[int64][]service.ModelRateLimitRule
	globalRules []service.ModelRateLimitRule
}

func (s *admissionRuleSnapshotCache) LoadModelRateLimitRules(_ context.Context, userID *int64) ([]service.ModelRateLimitRule, bool, error) {
	if userID == nil {
		return append([]service.ModelRateLimitRule(nil), s.globalRules...), true, nil
	}
	return append([]service.ModelRateLimitRule(nil), s.userRules[*userID]...), true, nil
}

func (*admissionRuleSnapshotCache) StoreModelRateLimitRules(context.Context, *int64, []service.ModelRateLimitRule) error {
	return nil
}

func (*admissionRuleSnapshotCache) PublishModelRateLimitInvalidation(context.Context, *int64) error {
	return nil
}

type admissionDenyingCounterCache struct{}

func (*admissionDenyingCounterCache) AdmitModelRateLimit(context.Context, int64, string, int, int, string, int64) (service.ModelRateLimitCacheAdmission, error) {
	return service.ModelRateLimitCacheAdmission{
		Allowed: false, Dimension: service.ModelRateLimitDimensionRPM,
		Used: 30, RetryAfterSeconds: 17,
	}, nil
}

func (*admissionDenyingCounterCache) RefreshModelRateLimitConcurrency(context.Context, int64, string, string) (bool, error) {
	return false, nil
}

func (*admissionDenyingCounterCache) ReleaseModelRateLimit(context.Context, int64, string, string) error {
	return nil
}

func newAdmissionLimiter(rules map[int64][]service.ModelRateLimitRule) *service.ProactiveModelRateLimitService {
	return service.NewProactiveModelRateLimitService(
		nil,
		&admissionRuleSnapshotCache{userRules: rules},
		&admissionDenyingCounterCache{},
		nil,
	)
}

func TestGatewayProactiveModelRateLimitRejectsBeforeSchedulingWithProtocolEnvelopeAndRouteCoverage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	admitter := &denyingModelRateLimitAdmitter{}
	for _, tc := range []struct {
		name     string
		path     string
		wantBody string
	}{
		{name: "openai", path: "/v1/responses", wantBody: `"code":"model_rate_limit_exceeded"`},
		{name: "anthropic", path: "/v1/messages", wantBody: `"type":"rate_limit_error"`},
		{name: "gemini", path: "/v1beta/models/gemini-3-pro:generateContent", wantBody: `"code":429`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, tc.path, nil)
			release, admitted := admitProactiveModelRateLimit(c, admitter, 7, "gpt-5.6-luna-high")
			if admitted {
				defer release()
				admitter.downstream()
			}
			require.False(t, admitted)
			require.Nil(t, release)
			require.Equal(t, http.StatusTooManyRequests, recorder.Code)
			require.Equal(t, "17", recorder.Header().Get("Retry-After"))
			require.Contains(t, recorder.Body.String(), tc.wantBody)
			if tc.name == "openai" {
				var envelope struct {
					Error struct {
						Type  string  `json:"type"`
						Code  string  `json:"code"`
						Param *string `json:"param"`
					} `json:"error"`
				}
				require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &envelope))
				require.Equal(t, "rate_limit_error", envelope.Error.Type)
				require.Equal(t, "model_rate_limit_exceeded", envelope.Error.Code)
				require.Equal(t, "model", *envelope.Error.Param)
			}
		})
	}
	require.Zero(t, admitter.forwardCalls)
	require.Zero(t, admitter.accountMutations)
	require.Zero(t, admitter.runtimeBlockCalls)

	covered := []struct {
		file, function, admissionToken string
	}{
		{"gateway_handler.go", "Messages", "admitProactiveModelRateLimit"},
		{"gateway_handler_chat_completions.go", "ChatCompletions", "admitProactiveModelRateLimit"},
		{"gateway_handler_responses.go", "Responses", "admitProactiveModelRateLimit"},
		{"gemini_v1beta_handler.go", "GeminiV1BetaModels", "admitProactiveModelRateLimit"},
		{"openai_gateway_handler.go", "Responses", "admitProactiveModelRateLimit"},
		{"openai_gateway_handler.go", "Messages", "admitProactiveModelRateLimit"},
		{"openai_gateway_handler.go", "ResponsesWebSocket", "admitModelRateLimitTurn"},
		{"openai_chat_completions.go", "ChatCompletions", "admitProactiveModelRateLimit"},
		{"openai_images.go", "Images", "admitProactiveModelRateLimit"},
		{"grok_media.go", "handleGrokMedia", "admitProactiveModelRateLimit"},
		{"openai_embeddings.go", "Embeddings", "admitProactiveModelRateLimit"},
		{"openai_alpha_search.go", "AlphaSearch", "admitProactiveModelRateLimit"},
		{"openai_live.go", "Live", "admitProactiveModelRateLimitRawDetailed"},
		{"gateway_web_search.go", "WebSearch", "admitProactiveModelRateLimit"},
		{"image_task_handler.go", "Submit", "admitProactiveModelRateLimitRaw"},
		{"batch_image_handler.go", "Submit", "admitProactiveModelRateLimit"},
		{"grok_audio.go", "GrokRealtime", "admitProactiveModelRateLimit"},
	}
	for _, route := range covered {
		source := stripGoComments(goFunctionSource(t, route.file, route.function))
		require.Containsf(t, source, route.admissionToken, "%s/%s must use shared proactive admission", route.file, route.function)
		billingIndex := strings.Index(source, "CheckBillingEligibility")
		admissionIndex := strings.Index(source, route.admissionToken)
		require.NotEqual(t, -1, billingIndex, "%s/%s must check billing", route.file, route.function)
		require.Less(t, billingIndex, admissionIndex)
		for _, scheduling := range []string{"SelectAccount", ".Forward", "CreateLiveCall"} {
			if index := strings.Index(source, scheduling); index >= 0 {
				require.Lessf(t, admissionIndex, index, "%s/%s must admit before %s", route.file, route.function, scheduling)
			}
		}
	}
	wsSource := stripGoComments(goFunctionSource(t, "openai_gateway_handler.go", "ResponsesWebSocket"))
	require.Contains(t, wsSource, "releaseModelRateLimitTurn")
	require.Contains(t, wsSource, "writeProactiveModelRateLimitWSError")

	t.Run("overall wait preserves a clean model-limit denial when rules apply", func(t *testing.T) {
		cache := &helperConcurrencyCacheStub{userSeq: []bool{false, true}, waitAllowed: true}
		helper := NewConcurrencyHelper(service.NewConcurrencyService(cache), SSEPingFormatComment, 5*time.Millisecond)
		limiter := newAdmissionLimiter(map[int64][]service.ModelRateLimitRule{
			7: {{ID: 1, ModelPattern: "gpt-5.6-luna-high", RPMLimit: 30}},
		})
		helper.SetModelRateLimiter(limiter)
		c, recorder := newHelperTestContext(http.MethodPost, "/v1/responses")
		streamStarted := false

		userRelease, err := helper.AcquireUserSlotWithWait(c, 7, 1, true, &streamStarted)
		require.NoError(t, err)
		require.NotNil(t, userRelease)
		require.False(t, streamStarted)
		require.Empty(t, recorder.Body.String())
		userRelease()

		modelRelease, admitted := admitProactiveModelRateLimit(c, limiter, 7, "gpt-5.6-luna-high")
		require.False(t, admitted)
		require.Nil(t, modelRelease)
		require.Equal(t, http.StatusTooManyRequests, recorder.Code)
		require.Equal(t, "17", recorder.Header().Get("Retry-After"))
		require.NotContains(t, recorder.Body.String(), string(SSEPingFormatComment))
		require.Contains(t, recorder.Body.String(), `"code":"model_rate_limit_exceeded"`)
	})

	t.Run("overall wait preserves legacy heartbeats when no rules apply", func(t *testing.T) {
		cache := &helperConcurrencyCacheStub{userSeq: []bool{false, true}, waitAllowed: true}
		helper := NewConcurrencyHelper(service.NewConcurrencyService(cache), SSEPingFormatComment, 5*time.Millisecond)
		helper.SetModelRateLimiter(newAdmissionLimiter(nil))
		c, recorder := newHelperTestContext(http.MethodPost, "/v1/responses")
		streamStarted := false

		userRelease, err := helper.AcquireUserSlotWithWait(c, 8, 1, true, &streamStarted)
		require.NoError(t, err)
		require.NotNil(t, userRelease)
		require.True(t, streamStarted)
		require.Contains(t, recorder.Body.String(), string(SSEPingFormatComment))
		userRelease()
	})
}
