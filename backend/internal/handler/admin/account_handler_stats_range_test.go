//go:build unit

package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type accountStatsUsageRepoCapture struct {
	service.UsageLogRepository
	accountID int64
	startTime time.Time
	endTime   time.Time
}

func (r *accountStatsUsageRepoCapture) GetAccountUsageStats(ctx context.Context, accountID int64, startTime, endTime time.Time) (*usagestats.AccountUsageStatsResponse, error) {
	r.accountID = accountID
	r.startTime = startTime
	r.endTime = endTime
	return &usagestats.AccountUsageStatsResponse{}, nil
}

func TestAccountHandlerGetStatsUsesExplicitDateRange(t *testing.T) {
	gin.SetMode(gin.TestMode)

	repo := &accountStatsUsageRepoCapture{}
	usageSvc := service.NewAccountUsageService(nil, repo, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	handler := NewAccountHandler(nil, nil, nil, nil, nil, nil, usageSvc, nil, nil, nil, nil, nil, nil)

	router := gin.New()
	router.GET("/admin/accounts/:id/stats", handler.GetStats)

	req := httptest.NewRequest(http.MethodGet, "/admin/accounts/42/stats?start_date=2026-06-01&end_date=2026-06-03&timezone=Asia%2FHong_Kong", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, int64(42), repo.accountID)
	require.Equal(t, "2026-06-01T00:00:00+08:00", repo.startTime.Format(time.RFC3339))
	require.Equal(t, "2026-06-04T00:00:00+08:00", repo.endTime.Format(time.RFC3339))
}
