package admin

import "testing"

func TestValidateCodexRectifierSettings(t *testing.T) {
	tests := []struct {
		name          string
		poolSize      *int
		platformRatio *string
		staggerHours  *int
		wantErr       bool
	}{
		{name: "defaults omitted"},
		{name: "minimums", poolSize: codexIntPtr(1), platformRatio: codexStringPtr("1:1:2"), staggerHours: codexIntPtr(0)},
		{name: "maximums", poolSize: codexIntPtr(8), platformRatio: codexStringPtr("1000000:1:1"), staggerHours: codexIntPtr(48)},
		{name: "pool below range", poolSize: codexIntPtr(0), wantErr: true},
		{name: "pool size two is forbidden", poolSize: codexIntPtr(2), wantErr: true},
		{name: "pool above range", poolSize: codexIntPtr(9), wantErr: true},
		{name: "platform ratio missing field", platformRatio: codexStringPtr("1:2"), wantErr: true},
		{name: "platform ratio requires positive weights", platformRatio: codexStringPtr("1:0:2"), wantErr: true},
		{name: "platform ratio rejects weight above maximum", platformRatio: codexStringPtr("1000001:1:1"), wantErr: true},
		{name: "stagger below range", staggerHours: codexIntPtr(-1), wantErr: true},
		{name: "stagger above range", staggerHours: codexIntPtr(49), wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateCodexRectifierSettings(tt.poolSize, tt.platformRatio, tt.staggerHours)
			if tt.wantErr && err == nil {
				t.Fatal("expected validation error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected validation error: %v", err)
			}
		})
	}
}

func codexIntPtr(value int) *int          { return &value }
func codexStringPtr(value string) *string { return &value }
