package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestHandleOpenAIRequestPacingErrorUsesEndpointEnvelope(t *testing.T) {
	tests := []struct {
		name       string
		anthropic  bool
		assertBody func(*testing.T, []byte)
	}{
		{
			name: "openai",
			assertBody: func(t *testing.T, body []byte) {
				require.Equal(t, "rate_limit_error", gjson.GetBytes(body, "error.type").String())
				require.False(t, gjson.GetBytes(body, "type").Exists())
			},
		},
		{
			name:      "anthropic messages",
			anthropic: true,
			assertBody: func(t *testing.T, body []byte) {
				require.Equal(t, "error", gjson.GetBytes(body, "type").String())
				require.Equal(t, "rate_limit_error", gjson.GetBytes(body, "error.type").String())
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			h := &OpenAIGatewayHandler{}

			require.True(t, h.handleOpenAIRequestPacingError(ctx, service.ErrOpenAIRequestPacingTimeout, false, tt.anthropic))
			require.Equal(t, http.StatusTooManyRequests, recorder.Code)
			require.True(t, service.HasOpsClientBusinessLimited(ctx))
			tt.assertBody(t, recorder.Body.Bytes())
		})
	}
}
