package kirocooldown

import (
	"context"
	"testing"

	"github.com/redis/go-redis/v9"
)

func TestClearEarliestTransientCooldownEmptyKeysIsSafe(t *testing.T) {
	store := NewStore(redis.NewClient(&redis.Options{Addr: "127.0.0.1:0"}))

	cleared, err := store.ClearEarliestTransientCooldown(context.Background(), nil)
	if err != nil {
		t.Fatalf("ClearEarliestTransientCooldown(nil) error = %v", err)
	}
	if cleared {
		t.Fatal("ClearEarliestTransientCooldown(nil) cleared = true, want false")
	}
}

func TestClearEarliestTransientCooldownUnavailableStore(t *testing.T) {
	store := NewStore(nil)

	cleared, err := store.ClearEarliestTransientCooldown(context.Background(), []string{"token"})
	if err == nil {
		t.Fatal("ClearEarliestTransientCooldown unavailable store error = nil")
	}
	if cleared {
		t.Fatal("ClearEarliestTransientCooldown unavailable store cleared = true, want false")
	}
}

func TestClampInt64ToInt(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	minInt := -maxInt - 1

	tests := []struct {
		name  string
		value int64
		want  int
	}{
		{name: "maximum", value: int64(^uint64(0) >> 1), want: maxInt},
		{name: "minimum", value: -int64(^uint64(0)>>1) - 1, want: minInt},
		{name: "ordinary", value: 7, want: 7},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := clampInt64ToInt(tt.value); got != tt.want {
				t.Fatalf("clampInt64ToInt(%d) = %d, want %d", tt.value, got, tt.want)
			}
		})
	}
}
