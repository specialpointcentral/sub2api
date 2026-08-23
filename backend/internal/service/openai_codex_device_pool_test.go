package service

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseCodexDevicePlatformRatio(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want codexDevicePlatformRatio
		ok   bool
	}{
		{name: "default ratio", raw: "1:1:2", want: codexDevicePlatformRatio{Mac: 1, Windows: 1, Linux: 2}, ok: true},
		{name: "trimmed values", raw: " 2 : 3 : 5 ", want: codexDevicePlatformRatio{Mac: 2, Windows: 3, Linux: 5}, ok: true},
		{name: "maximum weight", raw: "1000000:1:1", want: codexDevicePlatformRatio{Mac: 1_000_000, Windows: 1, Linux: 1}, ok: true},
		{name: "weight above maximum", raw: "1000001:1:1", ok: false},
		{name: "missing platform", raw: "1:2", ok: false},
		{name: "zero platform", raw: "1:0:2", ok: false},
		{name: "negative platform", raw: "1:-1:2", ok: false},
		{name: "non numeric", raw: "mac:windows:linux", ok: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseCodexDevicePlatformRatio(tt.raw)
			require.Equal(t, tt.ok, ok)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestBindCodexDevicePoolSlotLazilyCreatesFromObservedPersona(t *testing.T) {
	quotas := codexDevicePlatformQuotas{Mac: 1, Windows: 2, Linux: 1}
	observed := codexUAPersonaSelection{Platform: codexUAPersonaWindows, Sandbox: "none"}

	state, first, changed := bindCodexDevicePoolSlot(codexDevicePoolState{}, observed, 101, quotas)
	require.True(t, changed)
	require.Equal(t, codexDeviceSlot{ID: 1, Platform: codexUAPersonaWindows, Sandbox: "none", CreatedFor: "101"}, first)
	require.Equal(t, 2, state.NextSlot)
	require.Equal(t, []codexDeviceSlot{first}, state.Slots)

	sameState, same, changed := bindCodexDevicePoolSlot(state, observed, 101, quotas)
	require.False(t, changed, "the same first-seen user must not fill the platform quota alone")
	require.Equal(t, state, sameState)
	require.Equal(t, first, same)

	expanded, second, changed := bindCodexDevicePoolSlot(state, observed, 202, quotas)
	require.True(t, changed)
	require.Equal(t, 2, second.ID)
	require.Equal(t, observed.Platform, second.Platform)
	require.Equal(t, observed.Sandbox, second.Sandbox)
	require.Len(t, expanded.Slots, 2)
}

func TestBindCodexDevicePoolSlotAtQuotaUsesExistingPlatformDevice(t *testing.T) {
	observed := codexUAPersonaSelection{Platform: codexUAPersonaMac, Sandbox: "seatbelt"}
	state, first, changed := bindCodexDevicePoolSlot(codexDevicePoolState{}, observed, 101, codexDevicePlatformQuotas{Mac: 1, Windows: 1, Linux: 1})
	require.True(t, changed)

	unchanged, bound, changed := bindCodexDevicePoolSlot(state, observed, 202, codexDevicePlatformQuotas{Mac: 1, Windows: 1, Linux: 1})
	require.False(t, changed)
	require.Equal(t, state, unchanged)
	require.Equal(t, first, bound)
}

func TestCodexDevicePoolStateRoundTripPreservesSlotAndInstallation(t *testing.T) {
	state := codexDevicePoolState{Version: 1, NextSlot: 8, Slots: []codexDeviceSlot{{
		ID: 7, Platform: codexUAPersonaUbuntu, Sandbox: "seccomp", CreatedFor: "303",
	}}}
	raw, err := json.Marshal(state)
	require.NoError(t, err)
	var persisted any
	require.NoError(t, json.Unmarshal(raw, &persisted))

	reloaded, ok := canonicalCodexDevicePoolState(persisted)
	require.True(t, ok)
	require.Equal(t, state, reloaded)
	account := newTestOAuthAccount(42, nil)
	first := resolveConvergedInstallationIDForSlot(account, testCodexFingerprintSeed, state.Slots[0].ID)
	second := resolveConvergedInstallationIDForSlot(account, testCodexFingerprintSeed, reloaded.Slots[0].ID)
	require.Equal(t, first, second)
	require.NotEqual(t, resolveConvergedInstallationID(account, testCodexFingerprintSeed), second)
}

func TestCodexRendezvousPoolSlotIsSticky(t *testing.T) {
	slots := []codexDeviceSlot{
		{ID: 2, Platform: codexUAPersonaWindows, Sandbox: "none", CreatedFor: "1"},
		{ID: 7, Platform: codexUAPersonaWindows, Sandbox: "none", CreatedFor: "2"},
		{ID: 9, Platform: codexUAPersonaWindows, Sandbox: "none", CreatedFor: "3"},
	}
	for userID := int64(1); userID <= 1000; userID++ {
		first := codexRendezvousPoolSlot(testCodexFingerprintSeed, userID, slots)
		second := codexRendezvousPoolSlot(testCodexFingerprintSeed, userID, slots)
		require.Equal(t, first, second)
		require.Equal(t, codexUAPersonaWindows, first.Platform)
	}
}

func TestCodexRendezvousPoolSlotForPlatformStaysWithinObservedPlatformAndBalances(t *testing.T) {
	slots := []codexDeviceSlot{
		{ID: 1, Platform: codexUAPersonaMac, Sandbox: "seatbelt", CreatedFor: "1"},
		{ID: 2, Platform: codexUAPersonaWindows, Sandbox: "none", CreatedFor: "2"},
		{ID: 3, Platform: codexUAPersonaUbuntu, Sandbox: "seccomp", CreatedFor: "3"},
		{ID: 4, Platform: codexUAPersonaWindows, Sandbox: "none", CreatedFor: "4"},
	}
	counts := map[int]int{}
	for userID := int64(1); userID <= 10000; userID++ {
		first := codexRendezvousPoolSlotForPlatform(testCodexFingerprintSeed, userID, codexUAPersonaWindows, slots)
		second := codexRendezvousPoolSlotForPlatform(testCodexFingerprintSeed, userID, codexUAPersonaWindows, slots)
		require.Equal(t, first, second, "platform-scoped rendezvous must remain sticky")
		require.Equal(t, codexUAPersonaWindows, first.Platform)
		counts[first.ID]++
	}
	require.InDelta(t, 0.5, float64(counts[2])/10000, 0.02)
	require.InDelta(t, 0.5, float64(counts[4])/10000, 0.02)
}

func TestCodexRendezvousPoolSlotForPlatformFallsBackToWholePoolWhenUnobserved(t *testing.T) {
	slots := []codexDeviceSlot{
		{ID: 2, Platform: codexUAPersonaWindows, Sandbox: "none", CreatedFor: "2"},
		{ID: 3, Platform: codexUAPersonaUbuntu, Sandbox: "seccomp", CreatedFor: "3"},
	}
	seen := map[int]bool{}
	for userID := int64(1); userID <= 1000; userID++ {
		slot := codexRendezvousPoolSlotForPlatform(testCodexFingerprintSeed, userID, codexUAPersonaMac, slots)
		require.Contains(t, []int{2, 3}, slot.ID)
		seen[slot.ID] = true
	}
	require.Equal(t, map[int]bool{2: true, 3: true}, seen)
}

func TestCodexRendezvousPoolSlotIncrementalExpansionOnlyMovesToNewDevice(t *testing.T) {
	const users = 10000
	for beforeSize := 3; beforeSize <= 7; beforeSize++ {
		t.Run(fmt.Sprintf("%d_to_%d", beforeSize, beforeSize+1), func(t *testing.T) {
			before := codexDeviceSlots(beforeSize)
			after := codexDeviceSlots(beforeSize + 1)
			moved := 0
			for userID := int64(1); userID <= users; userID++ {
				oldSlot := codexRendezvousPoolSlot(testCodexFingerprintSeed, userID, before)
				newSlot := codexRendezvousPoolSlot(testCodexFingerprintSeed, userID, after)
				if oldSlot.ID != newSlot.ID {
					moved++
					require.Equal(t, beforeSize+1, newSlot.ID, "HRW expansion may only move users to the added slot")
				}
			}
			require.InDelta(t, 1.0/float64(beforeSize+1), float64(moved)/users, 0.02)
		})
	}
}

func codexDeviceSlots(size int) []codexDeviceSlot {
	slots := make([]codexDeviceSlot, 0, size)
	for id := 1; id <= size; id++ {
		slots = append(slots, codexDeviceSlot{ID: id})
	}
	return slots
}

func TestCodexRendezvousPoolSlotShrinkOnlyMovesRemovedDevices(t *testing.T) {
	before := []codexDeviceSlot{{ID: 1}, {ID: 2}, {ID: 3}, {ID: 4}}
	after := []codexDeviceSlot{{ID: 1}, {ID: 2}}
	moved := 0
	for userID := int64(1); userID <= 10000; userID++ {
		oldSlot := codexRendezvousPoolSlot(testCodexFingerprintSeed, userID, before)
		newSlot := codexRendezvousPoolSlot(testCodexFingerprintSeed, userID, after)
		if oldSlot.ID != newSlot.ID {
			moved++
			require.Contains(t, []int{3, 4}, oldSlot.ID)
		}
		require.Contains(t, []int{1, 2}, newSlot.ID)
	}
	require.Greater(t, moved, 0)
}

func TestBindCodexDevicePoolSlotShrinkThenExpansionStaysLazy(t *testing.T) {
	state := codexDevicePoolState{Version: 1, NextSlot: 4, Slots: []codexDeviceSlot{
		{ID: 1, Platform: codexUAPersonaWindows, Sandbox: "none", CreatedFor: "101"},
		{ID: 2, Platform: codexUAPersonaWindows, Sandbox: "none", CreatedFor: "202"},
		{ID: 3, Platform: codexUAPersonaWindows, Sandbox: "none", CreatedFor: "303"},
	}}
	observed := codexUAPersonaSelection{Platform: codexUAPersonaWindows, Sandbox: "none"}

	shrunk, _, changed := bindCodexDevicePoolSlot(state, observed, 404, codexDevicePlatformQuotas{Windows: 2})
	require.True(t, changed)
	require.Equal(t, []int{1, 2}, []int{shrunk.Slots[0].ID, shrunk.Slots[1].ID})

	notExpanded, _, changed := bindCodexDevicePoolSlot(shrunk, observed, 101, codexDevicePlatformQuotas{Windows: 3})
	require.False(t, changed, "an existing user must not eagerly fill expanded capacity")
	require.Len(t, notExpanded.Slots, 2)

	expanded, created, changed := bindCodexDevicePoolSlot(shrunk, observed, 505, codexDevicePlatformQuotas{Windows: 3})
	require.True(t, changed)
	require.Equal(t, 4, created.ID, "retired slot IDs must not be reused after shrink")
	require.Len(t, expanded.Slots, 3)
}

func TestBindCodexDevicePoolSlotShrinkKeepsOldestSlotsRegardlessOfInputOrder(t *testing.T) {
	state := codexDevicePoolState{Version: 1, NextSlot: 4, Slots: []codexDeviceSlot{
		{ID: 3, Platform: codexUAPersonaWindows, Sandbox: "none", CreatedFor: "303"},
		{ID: 1, Platform: codexUAPersonaWindows, Sandbox: "none", CreatedFor: "101"},
		{ID: 2, Platform: codexUAPersonaWindows, Sandbox: "none", CreatedFor: "202"},
	}}
	observed := codexUAPersonaSelection{Platform: codexUAPersonaWindows, Sandbox: "none"}

	shrunk, _, changed := bindCodexDevicePoolSlot(state, observed, 404, codexDevicePlatformQuotas{Windows: 2})

	require.True(t, changed)
	require.Equal(t, []int{1, 2}, []int{shrunk.Slots[0].ID, shrunk.Slots[1].ID})
}

func TestCanonicalCodexDevicePoolStateRejectsOutOfOrderSlotIDs(t *testing.T) {
	_, valid := canonicalCodexDevicePoolState(codexDevicePoolState{
		Version:  1,
		NextSlot: 4,
		Slots: []codexDeviceSlot{
			{ID: 2, Platform: codexUAPersonaMac, Sandbox: "seatbelt", CreatedFor: "202"},
			{ID: 1, Platform: codexUAPersonaMac, Sandbox: "seatbelt", CreatedFor: "101"},
		},
	})
	require.False(t, valid)
}

func TestCodexDevicePlatformQuotasGuaranteeEveryPlatform(t *testing.T) {
	ratio := codexDevicePlatformRatio{Mac: 1, Windows: 1, Linux: 2}

	require.Equal(t, codexDevicePlatformQuotas{Mac: 1, Windows: 1, Linux: 1}, allocateCodexDevicePlatformQuotas(3, ratio))
	require.Equal(t, codexDevicePlatformQuotas{Mac: 1, Windows: 1, Linux: 2}, allocateCodexDevicePlatformQuotas(4, ratio))
	require.Equal(t, codexDevicePlatformQuotas{Mac: 2, Windows: 2, Linux: 4}, allocateCodexDevicePlatformQuotas(8, ratio))
}

func TestCodexDevicePlatformQuotasPoolOnePreservesLegacyDevice(t *testing.T) {
	ratio := codexDevicePlatformRatio{Mac: 1, Windows: 1, Linux: 2}
	require.Equal(t, codexDevicePlatformQuotas{}, allocateCodexDevicePlatformQuotas(1, ratio))
}
