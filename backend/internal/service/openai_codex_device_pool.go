package service

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
)

const defaultOpenAICodexDevicePoolPlatformRatio = "1:1:2"
const codexDevicePoolExtraKey = "codex_device_pool"

type codexDeviceSlot struct {
	ID          int            `json:"id"`
	Platform    codexUAPersona `json:"platform"`
	Sandbox     string         `json:"sandbox"`
	CreatedFor  string         `json:"created_for"`
	RootSession string         `json:"root_session,omitempty"`
}

type codexDevicePoolState struct {
	Version  int               `json:"version"`
	NextSlot int               `json:"next_slot"`
	Slots    []codexDeviceSlot `json:"slots"`
}

// codexDevicePlatformRatioWeightMax bounds accepted admin input so quota
// arithmetic stays small and a ratio cannot encode meaningless giant weights.
const codexDevicePlatformRatioWeightMax = 1_000_000

type codexDevicePlatformRatio struct {
	Mac     int
	Windows int
	Linux   int
}

type codexDevicePlatformQuotas struct {
	Mac     int
	Windows int
	Linux   int
}

func canonicalCodexDevicePoolState(value any) (codexDevicePoolState, bool) {
	if value == nil {
		return codexDevicePoolState{Version: 1, NextSlot: 1, Slots: []codexDeviceSlot{}}, true
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return codexDevicePoolState{}, false
	}
	var state codexDevicePoolState
	if err := json.Unmarshal(raw, &state); err != nil || state.Version != 1 || state.NextSlot < 1 {
		return codexDevicePoolState{}, false
	}
	seen := make(map[int]struct{}, len(state.Slots))
	maxSlot := 0
	for _, slot := range state.Slots {
		selection, ok := normalizeCodexUAPersonaSelection(string(slot.Platform), slot.Sandbox)
		if !ok || selection.Platform != slot.Platform || slot.ID < 1 || strings.TrimSpace(slot.CreatedFor) == "" {
			return codexDevicePoolState{}, false
		}
		if _, duplicate := seen[slot.ID]; duplicate {
			return codexDevicePoolState{}, false
		}
		// Slots are append-only IDs in oldest-to-newest order. Enforcing the
		// order makes deterministic shrink semantics survive serialization.
		if slot.ID <= maxSlot {
			return codexDevicePoolState{}, false
		}
		seen[slot.ID] = struct{}{}
		maxSlot = slot.ID
	}
	if state.NextSlot <= maxSlot {
		return codexDevicePoolState{}, false
	}
	if state.Slots == nil {
		state.Slots = []codexDeviceSlot{}
	}
	return state, true
}

func (q codexDevicePlatformQuotas) forPlatform(platform codexUAPersona) int {
	switch platform {
	case codexUAPersonaMac:
		return q.Mac
	case codexUAPersonaWindows:
		return q.Windows
	case codexUAPersonaUbuntu:
		return q.Linux
	default:
		return 0
	}
}

func codexDevicePoolUserKey(userID int64) string {
	if userID <= 0 {
		return "anonymous"
	}
	return strconv.FormatInt(userID, 10)
}

// codexRendezvousPoolSlot selects from the already-created, platform-tagged
// devices. Stable slot IDs keep scores unchanged across restarts and make HRW
// expansion/removal move only users affected by the changed nodes. Persisted
// slots use a namespace disjoint from legacy slot 0, so first 1→3
// enablement rotates every identity once; later N→N+1 expansion only moves users
// won by the new slot.
func codexRendezvousPoolSlot(seed string, userID int64, slots []codexDeviceSlot) codexDeviceSlot {
	if len(slots) == 0 {
		return codexDeviceSlot{}
	}
	userKey := codexDevicePoolUserKey(userID)
	best := slots[0]
	var bestScore uint64
	for index, slot := range slots {
		hash := sha256.Sum256([]byte("sub2api:codex-platform-rendezvous:v1:" + seed + ":" + userKey + ":" + strconv.Itoa(slot.ID)))
		score := binary.BigEndian.Uint64(hash[:8])
		if index == 0 || score > bestScore {
			best = slot
			bestScore = score
		}
	}
	return best
}

// codexRendezvousPoolSlotForPlatform keeps a user's device assignment within
// the platform observed for the current request. A platform that has not
// created any slots yet falls back to the whole retained pool, preserving
// availability while lazy creation and persistence converge.
func codexRendezvousPoolSlotForPlatform(seed string, userID int64, observedPlatform codexUAPersona, slots []codexDeviceSlot) codexDeviceSlot {
	platformSlots := make([]codexDeviceSlot, 0, len(slots))
	for _, slot := range slots {
		if slot.Platform == observedPlatform {
			platformSlots = append(platformSlots, slot)
		}
	}
	if len(platformSlots) == 0 {
		platformSlots = slots
	}
	return codexRendezvousPoolSlot(seed, userID, platformSlots)
}

func setCodexDeviceSlotRoot(state codexDevicePoolState, slotID int, clientSessionID string) (codexDevicePoolState, codexDeviceSlot, bool) {
	clientSessionID = strings.TrimSpace(clientSessionID)
	for index := range state.Slots {
		if state.Slots[index].ID != slotID {
			continue
		}
		if state.Slots[index].RootSession != "" || clientSessionID == "" {
			return state, state.Slots[index], false
		}
		state.Slots[index].RootSession = clientSessionID
		return state, state.Slots[index], true
	}
	return state, codexDeviceSlot{}, false
}

// bindCodexDevicePoolSlot prunes an observed platform to its current quota by
// retaining the oldest (lowest-ID) slots and deleting the most recently expanded
// (highest-ID) slots. It lazily creates at most one slot for each first-seen user
// while capacity remains. The caller performs platform-scoped rendezvous
// selection over the retained platform-tagged slots.
func bindCodexDevicePoolSlot(state codexDevicePoolState, observed codexUAPersonaSelection, userID int64, quotas codexDevicePlatformQuotas) (codexDevicePoolState, codexDeviceSlot, bool) {
	if state.Version == 0 {
		state = codexDevicePoolState{Version: 1, NextSlot: 1, Slots: []codexDeviceSlot{}}
	}
	quota := quotas.forPlatform(observed.Platform)
	if quota <= 0 {
		return state, codexDeviceSlot{}, false
	}
	sortedSlots := append([]codexDeviceSlot(nil), state.Slots...)
	sort.Slice(sortedSlots, func(i, j int) bool { return sortedSlots[i].ID < sortedSlots[j].ID })
	orderChanged := false
	for index := range sortedSlots {
		if sortedSlots[index].ID != state.Slots[index].ID {
			orderChanged = true
			break
		}
	}
	state.Slots = sortedSlots
	userKey := codexDevicePoolUserKey(userID)
	retained := make([]codexDeviceSlot, 0, len(state.Slots))
	platformSlots := make([]codexDeviceSlot, 0, quota)
	for _, slot := range state.Slots {
		if slot.Platform != observed.Platform {
			retained = append(retained, slot)
			continue
		}
		if len(platformSlots) < quota {
			platformSlots = append(platformSlots, slot)
			retained = append(retained, slot)
		}
	}
	changed := orderChanged || len(retained) != len(state.Slots)
	if changed {
		state.Slots = retained
	}
	for _, slot := range platformSlots {
		if slot.CreatedFor == userKey {
			return state, slot, changed
		}
	}
	if len(platformSlots) >= quota {
		return state, platformSlots[0], changed
	}
	created := codexDeviceSlot{
		ID:         state.NextSlot,
		Platform:   observed.Platform,
		Sandbox:    observed.Sandbox,
		CreatedFor: userKey,
	}
	state.NextSlot++
	state.Slots = append(state.Slots, created)
	return state, created, true
}

func parseCodexDevicePlatformRatio(raw string) (codexDevicePlatformRatio, bool) {
	parts := strings.Split(raw, ":")
	if len(parts) != 3 {
		return codexDevicePlatformRatio{}, false
	}
	values := [3]int{}
	for index, part := range parts {
		value, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || value <= 0 || value > codexDevicePlatformRatioWeightMax {
			return codexDevicePlatformRatio{}, false
		}
		values[index] = value
	}
	return codexDevicePlatformRatio{Mac: values[0], Windows: values[1], Linux: values[2]}, true
}

// NormalizeCodexDevicePlatformRatio validates mac:windows:linux positive integer
// weights and returns their canonical representation.
func NormalizeCodexDevicePlatformRatio(raw string) (string, bool) {
	ratio, ok := parseCodexDevicePlatformRatio(raw)
	if !ok {
		return "", false
	}
	return strconv.Itoa(ratio.Mac) + ":" + strconv.Itoa(ratio.Windows) + ":" + strconv.Itoa(ratio.Linux), true
}

// allocateCodexDevicePlatformQuotas reserves one device for every platform,
// then apportions remaining capacity by largest remainder. Equal remainders use
// the stable mac, windows, linux order.
func allocateCodexDevicePlatformQuotas(poolSize int, ratio codexDevicePlatformRatio) codexDevicePlatformQuotas {
	if poolSize < 3 {
		return codexDevicePlatformQuotas{}
	}
	weights := [3]int{ratio.Mac, ratio.Windows, ratio.Linux}
	totalWeight := weights[0] + weights[1] + weights[2]
	if totalWeight <= 0 {
		weights = [3]int{1, 1, 2}
		totalWeight = 4
	}

	remaining := poolSize - 3
	quotas := [3]int{1, 1, 1}
	remainders := [3]int{}
	assigned := 0
	for index, weight := range weights {
		product := remaining * weight
		quotas[index] += product / totalWeight
		remainders[index] = product % totalWeight
		assigned += product / totalWeight
	}
	for left := remaining - assigned; left > 0; left-- {
		best := 0
		for index := 1; index < len(remainders); index++ {
			if remainders[index] > remainders[best] {
				best = index
			}
		}
		quotas[best]++
		remainders[best] = -1
	}
	return codexDevicePlatformQuotas{Mac: quotas[0], Windows: quotas[1], Linux: quotas[2]}
}
