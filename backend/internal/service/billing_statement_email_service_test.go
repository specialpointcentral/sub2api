package service

import (
	"context"
	"errors"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/stretchr/testify/require"
)

func TestBillingStatementEmailService_SendStatementsUsesPerEmailTimeout(t *testing.T) {
	oldTimeout := billingStatementEmailSendTimeout
	billingStatementEmailSendTimeout = 5 * time.Millisecond
	t.Cleanup(func() { billingStatementEmailSendTimeout = oldTimeout })

	ctx := context.Background()
	settings := newNotificationEmailMemorySettingRepo()
	for _, userID := range []int64{1, 2} {
		require.NoError(t, settings.Set(ctx,
			SettingKeyBillingStatementUserPreferencePrefix+strconv.FormatInt(userID, 10),
			`{"daily_enabled":true}`,
		))
	}

	usageRepo := &billingStatementContextUsageRepoStub{}
	svc := &BillingStatementEmailService{
		settingRepo: settings,
		userRepo: &billingStatementUserRepoStub{users: []User{
			{ID: 1, Email: "first@example.com", Status: StatusActive},
			{ID: 2, Email: "second@example.com", Status: StatusActive},
		}},
		usageRepo: usageRepo,
	}

	svc.sendStatements(ctx, "daily", "日账单 / Daily Billing Statement", time.Date(2026, 7, 1, 8, 0, 0, 0, time.UTC))

	require.Equal(t, int64(2), usageRepo.calls.Load())
	require.True(t, usageRepo.firstTimedOut.Load(), "first email should get its own deadline")
	require.False(t, usageRepo.secondStartedCanceled.Load(), "second email should get a fresh context")
}

func TestBillingStatementEmailService_SendStatementsSkipsDisabledUsers(t *testing.T) {
	ctx := context.Background()
	settings := newNotificationEmailMemorySettingRepo()
	for _, userID := range []int64{1, 2} {
		require.NoError(t, settings.Set(ctx,
			SettingKeyBillingStatementUserPreferencePrefix+strconv.FormatInt(userID, 10),
			`{"monthly_enabled":true}`,
		))
	}

	usageRepo := &billingStatementUsageRepoStub{}
	svc := &BillingStatementEmailService{
		settingRepo: settings,
		userRepo: &billingStatementUserRepoStub{users: []User{
			{ID: 1, Email: "disabled@example.com", Status: StatusDisabled},
			{ID: 2, Email: "active@example.com", Status: StatusActive},
		}},
		usageRepo: usageRepo,
	}

	svc.sendStatements(ctx, "monthly", "月账单 / Monthly Billing Statement", time.Date(2026, 7, 1, 8, 0, 0, 0, time.UTC))

	require.Equal(t, 1, usageRepo.calls)
	require.Equal(t, int64(2), usageRepo.userID)
}

func TestBillingStatementEmailService_UserPeriodEnabledDefaultsToAllPeriodsWhenPreferenceMissing(t *testing.T) {
	svc := &BillingStatementEmailService{settingRepo: newNotificationEmailMemorySettingRepo()}

	require.True(t, svc.isUserPeriodEnabled(context.Background(), 42, "daily"))
	require.True(t, svc.isUserPeriodEnabled(context.Background(), 42, "weekly"))
	require.True(t, svc.isUserPeriodEnabled(context.Background(), 42, "monthly"))
}

func TestParseBillingStatementEmailConfig_Empty(t *testing.T) {
	cfg := ParseBillingStatementEmailConfig("")
	def := DefaultBillingStatementEmailConfig()
	if cfg.Enabled != def.Enabled {
		t.Errorf("expected Enabled=%v, got %v", def.Enabled, cfg.Enabled)
	}
	if cfg.DailySchedule != def.DailySchedule {
		t.Errorf("expected DailySchedule=%q, got %q", def.DailySchedule, cfg.DailySchedule)
	}
}

func TestParseBillingStatementEmailConfig_Valid(t *testing.T) {
	raw := `{"enabled":true,"daily_enabled":true,"weekly_enabled":false,"monthly_enabled":true,"daily_schedule":"30 9 * * *","weekly_schedule":"0 8 * * 1","monthly_schedule":"0 8 1 * *"}`
	cfg := ParseBillingStatementEmailConfig(raw)
	if !cfg.Enabled {
		t.Error("expected Enabled=true")
	}
	if !cfg.DailyEnabled {
		t.Error("expected DailyEnabled=true")
	}
	if cfg.WeeklyEnabled {
		t.Error("expected WeeklyEnabled=false")
	}
	if !cfg.MonthlyEnabled {
		t.Error("expected MonthlyEnabled=true")
	}
	if cfg.DailySchedule != "30 9 * * *" {
		t.Errorf("expected DailySchedule='30 9 * * *', got %q", cfg.DailySchedule)
	}
}

func TestParseBillingStatementEmailConfig_Invalid(t *testing.T) {
	cfg := ParseBillingStatementEmailConfig("{invalid json")
	def := DefaultBillingStatementEmailConfig()
	if cfg.Enabled != def.Enabled {
		t.Errorf("expected fallback to default on invalid JSON")
	}
}

func TestIsValidEmailForBilling(t *testing.T) {
	tests := []struct {
		email string
		want  bool
	}{
		{"user@example.com", true},
		{"", false},
		{"noatsign", false},
		{"user@linuxdo-connect.invalid", false},
		{"user@oidc-connect.invalid", false},
		{"user@OIDC-CONNECT.INVALID", false},
		{"user@wechat-connect.invalid", false},
		{"admin@company.org", true},
	}
	for _, tt := range tests {
		got := isValidEmailForBilling(tt.email)
		if got != tt.want {
			t.Errorf("isValidEmailForBilling(%q) = %v, want %v", tt.email, got, tt.want)
		}
	}
}

func TestBuildBillingStatementEmailHTML_Nil(t *testing.T) {
	html := buildBillingStatementEmailHTML(nil)
	if html != "<p>无数据 / No data.</p>" {
		t.Errorf("expected no-data HTML for nil statement")
	}
}

func TestBuildBillingStatementEmailHTML_Basic(t *testing.T) {
	gid := int64(1)
	stmt := &BillingStatement{
		UserID:     1,
		UserEmail:  "test@example.com",
		PeriodName: "日账单",
		Start:      time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		End:        time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC),
		Lines: []BillingStatementLine{
			{
				Model:       "claude-sonnet-4-20250514",
				BillingMode: "token",
				GroupID:     &gid,
				Requests:    10,
				TotalTokens: 50000,
				TotalCost:   1.5,
				ActualCost:  1.2,
				Discount:    0.3,
			},
		},
		TotalCost:  1.5,
		ActualCost: 1.2,
		Discount:   0.3,
		Balance:    8.5,
	}
	html := buildBillingStatementEmailHTML(stmt)
	if html == "" {
		t.Error("expected non-empty HTML")
	}
	// Check key content is present
	if !containsStr(html, "日账单") {
		t.Error("expected period name in HTML")
	}
	if !containsStr(html, "致 test@example.com") || !containsStr(html, "To test@example.com") {
		t.Error("expected recipient greeting in HTML")
	}
	if !containsStr(html, "claude-sonnet-4-20250514") {
		t.Error("expected model name in HTML")
	}
	if !containsStr(html, "$1.5000") {
		t.Error("expected total cost in HTML")
	}
	if !containsStr(html, "$8.5000") {
		t.Error("expected balance in HTML")
	}
}

func TestBillingStatementEmailService_UsesNotificationTemplate(t *testing.T) {
	ctx := context.Background()
	server := startNotificationEmailTestSMTPServer(t)
	repo := newNotificationEmailMemorySettingRepo()
	for key, value := range server.settings() {
		if err := repo.Set(ctx, key, value); err != nil {
			t.Fatalf("set smtp setting %s: %v", key, err)
		}
	}

	emailService := NewEmailService(repo, nil)
	notificationEmailService := NewNotificationEmailService(repo, emailService)
	emailService.SetNotificationEmailService(notificationEmailService)
	if err := repo.Set(ctx, SettingKeySiteName, "Sub2API Test"); err != nil {
		t.Fatalf("set site name: %v", err)
	}
	_, err := notificationEmailService.UpdateTemplate(ctx, NotificationEmailEventBillingStatement, "en",
		"Custom statement {{period_name}}",
		"<h1>Custom billing statement</h1>{{statement_html}}",
	)
	if err != nil {
		t.Fatalf("update billing statement template: %v", err)
	}

	stmt := &BillingStatement{
		UserID:     7,
		UserEmail:  "billing@example.com",
		PeriodName: "Monthly Billing Statement",
		Start:      time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
		End:        time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		Timezone:   "UTC",
		Lines: []BillingStatementLine{{
			Model:      "gpt-5",
			Requests:   3,
			TotalCost:  1.25,
			ActualCost: 1.00,
			Discount:   0.25,
		}},
		TotalCost:  1.25,
		ActualCost: 1.00,
		Discount:   0.25,
		Balance:    9.50,
	}
	svc := &BillingStatementEmailService{emailService: emailService}

	if !svc.sendStatementWithTemplate(ctx, &User{ID: 7, Email: "billing@example.com"}, stmt) {
		t.Fatal("expected billing statement to be sent through notification template service")
	}
	if got := server.messageCount(); got != 1 {
		t.Fatalf("expected one templated email, got %d", got)
	}
}

func TestAggregateUserUsageUsesRepositoryAggregation(t *testing.T) {
	start := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	repo := &billingStatementUsageRepoStub{
		lines: []BillingStatementLine{
			{
				Model:       "claude-sonnet-4-5",
				BillingMode: "token",
				Requests:    12001,
				TotalTokens: 900000,
				TotalCost:   240.02,
				ActualCost:  120.01,
				Discount:    120.01,
			},
		},
	}
	svc := &BillingStatementEmailService{usageRepo: repo}

	lines := svc.aggregateUserUsage(context.Background(), 42, start, end)

	if repo.calls != 1 {
		t.Fatalf("expected one aggregate query, got %d", repo.calls)
	}
	if repo.userID != 42 || !repo.start.Equal(start) || !repo.end.Equal(end) {
		t.Fatalf("aggregate query used wrong range: user=%d start=%s end=%s", repo.userID, repo.start, repo.end)
	}
	if len(lines) != 1 {
		t.Fatalf("expected one line, got %d", len(lines))
	}
	if lines[0].Requests != 12001 {
		t.Fatalf("expected untruncated request count, got %d", lines[0].Requests)
	}
	if lines[0].ActualCost != 120.01 {
		t.Fatalf("expected untruncated actual cost, got %.2f", lines[0].ActualCost)
	}
}

type billingStatementUsageRepoStub struct {
	UsageLogRepository
	lines  []BillingStatementLine
	calls  int
	userID int64
	start  time.Time
	end    time.Time
}

func (r *billingStatementUsageRepoStub) GetBillingStatementLines(ctx context.Context, userID int64, startTime, endTime time.Time) ([]BillingStatementLine, error) {
	r.calls++
	r.userID = userID
	r.start = startTime
	r.end = endTime
	return r.lines, nil
}

type billingStatementUserRepoStub struct {
	UserRepository
	users []User
}

func (r *billingStatementUserRepoStub) List(context.Context, pagination.PaginationParams) ([]User, *pagination.PaginationResult, error) {
	return r.users, &pagination.PaginationResult{Total: int64(len(r.users)), Page: 1, PageSize: len(r.users), Pages: 1}, nil
}

type billingStatementContextUsageRepoStub struct {
	UsageLogRepository
	calls                 atomic.Int64
	firstTimedOut         atomic.Bool
	secondStartedCanceled atomic.Bool
}

func (r *billingStatementContextUsageRepoStub) GetBillingStatementLines(ctx context.Context, userID int64, startTime, endTime time.Time) ([]BillingStatementLine, error) {
	call := r.calls.Add(1)
	switch call {
	case 1:
		select {
		case <-ctx.Done():
			r.firstTimedOut.Store(true)
			return nil, ctx.Err()
		case <-time.After(50 * time.Millisecond):
			return nil, errors.New("expected per-email context deadline")
		}
	case 2:
		if ctx.Err() != nil {
			r.secondStartedCanceled.Store(true)
		}
	}
	return nil, nil
}

func containsStr(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && (s == substr || len(s) > len(substr) && findSubstr(s, substr))
}

func findSubstr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
