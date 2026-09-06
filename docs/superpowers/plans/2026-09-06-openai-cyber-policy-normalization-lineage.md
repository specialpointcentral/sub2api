# OpenAI Cyber-Policy Normalization and Session Lineage Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Preserve CPA legacy top-level `cyber_policy` errors across every OpenAI Responses transport and synchronously block the same explicit, `previous_response_id`-lineage, or transcript-derived downstream session before the failure becomes client-visible.

**Architecture:** A shared pure error extractor supplies code, semantic type, message, transport status, and semantic status to HTTP/SSE/WS consumers. A narrow optional Redis lineage store maps downstream response IDs to opaque API-key-isolated session roots; a request-scoped identity observes response IDs and activates an idempotent block from `MarkOpsCyberPolicy` before downstream error delivery. The final implementation is one private patch inserted immediately after site patch `081537dee`; the open handshake PR #6645 remains untouched.

**Tech Stack:** Go 1.27.0, Gin, gjson/sjson, go-redis v9, miniredis, Testify, Docker/Testcontainers, PostgreSQL 16, golangci-lint v2.13.

## Global Constraints

- Do not infer cyber policy from message text; require exact case-insensitive `code=cyber_policy`.
- Support nested `error.*`, nested `response.error.*`, and CPA legacy top-level `code/message`.
- Preserve SSE transport status separately from semantic status 400.
- A cyber hit must not fail over, cool down, disable, or make an upstream account temporarily unschedulable.
- Block sessions, never the entire API key, IP, User-Agent, user, group, or upstream account.
- Keep `cyber_session_block_enabled=false` and the default TTL of 3600 seconds.
- Keep Redis/store errors fail-open and observable.
- API key + IP + normalized User-Agent remains only a transcript lookup gate, never a block key.
- `previous_response_id` lineage is isolated by group and downstream API-key ID.
- The block must be queryable before the cyber failure is written downstream when Redis is healthy.
- An `error`/`response.failed` pair records and charges exactly once.
- Do not modify CPA, PR #6645, its remote branch, tags, releases, or services.
- The final feature is one commit immediately after `081537dee`; all later site patches are replayed and semantically verified.
- Re-check PR #6645 and upstream `main` immediately before history integration because either may move during implementation.
- Use current CI tools: Go 1.27.0, golangci-lint v2.13, and real Testcontainers with `postgres:16-alpine`.

---

### Task 1: Create an isolated front-of-stack implementation worktree and verify the baseline

**Files:**

- Worktree: `/tmp/sub2api-cyber-lineage`
- Branch: `cyber-policy-normalization-lineage`
- Base: site patch `081537dee`

**Interfaces:**

- Consumes: upstream-merged PR #6636 and patch-equivalent open-PR #6645 site commit `081537dee`.
- Produces: a clean implementation branch whose parent is exactly `081537dee`.

- [ ] **Step 1: Refresh upstream and PR state without changing the open PR branch**

Run:

```bash
git fetch origin main
git fetch fork cyber-ws-handshake-policy-fix site
gh pr view 6645 --repo Wei-Shaw/sub2api --json state,headRefOid,mergedAt,url
```

Expected at plan creation: PR #6645 is OPEN at `5af0a085a`; `081537dee` has the same stable patch ID. If it has merged, stop and recompute the base so the new feature remains the first follow-up private patch rather than replaying an upstream-equivalent handshake patch.

- [ ] **Step 2: Verify the exact insertion parent and create the worktree**

Run:

```bash
test "$(git show -s --format=%s 081537dee)" = "fix(openai): record cyber WS handshake failures"
test -z "$(git status --porcelain)"
git worktree add -b cyber-policy-normalization-lineage /tmp/sub2api-cyber-lineage 081537dee
git -C /tmp/sub2api-cyber-lineage status --short --branch
```

Expected: clean named branch at `081537dee`. Do not reuse `/home/huqi/sub2api-cyber-pr` or `/tmp/sub2api-pr-cyber`; they belong to earlier PR work.

- [ ] **Step 3: Run focused baseline packages under Go 1.27**

Run:

```bash
mkdir -p /tmp/sub2api-cyber-lineage-go-cache/mod /tmp/sub2api-cyber-lineage-go-cache/build
docker run --rm \
  -v /tmp/sub2api-cyber-lineage:/src \
  -v /tmp/sub2api-cyber-lineage-go-cache/mod:/go/pkg/mod \
  -v /tmp/sub2api-cyber-lineage-go-cache/build:/root/.cache/go-build \
  -w /src/backend golang:1.27 \
  sh -c 'go test ./internal/service ./internal/handler ./internal/repository'
```

Expected: baseline PASS before any production edit. If a baseline failure is unrelated to cyber behavior, stop and report it rather than modifying tests to hide it.

### Task 2: Add a shared error-field extractor

**Files:**

- Create: `backend/internal/service/openai_response_error_fields.go`
- Create: `backend/internal/service/openai_response_error_fields_test.go`

**Interfaces:**

- Produces: `extractOpenAIResponsesErrorFields(payload []byte, transportStatus int) openAIResponsesErrorFields`.
- Produces fields: `Code string`, `Type string`, `Message string`, `TransportStatus int`, `SemanticStatus int`.
- Does not classify message text as cyber policy.

- [ ] **Step 1: Write failing table tests for all three schemas**

Create `openai_response_error_fields_test.go` with cases equivalent to:

```go
func TestExtractOpenAIResponsesErrorFields(t *testing.T) {
	tests := []struct {
		name            string
		payload         string
		transportStatus int
		wantCode        string
		wantType        string
		wantMessage     string
		wantSemantic    int
	}{
		{
			name: "nested error",
			payload: `{"error":{"type":"invalid_request","code":"cyber_policy","message":"nested"}}`,
			transportStatus: http.StatusBadRequest,
			wantCode: "cyber_policy", wantType: "invalid_request",
			wantMessage: "nested", wantSemantic: http.StatusBadRequest,
		},
		{
			name: "response failed",
			payload: `{"type":"response.failed","response":{"error":{"type":"invalid_request","code":"cyber_policy","message":"wrapped"}}}`,
			transportStatus: http.StatusOK,
			wantCode: "cyber_policy", wantType: "invalid_request",
			wantMessage: "wrapped", wantSemantic: http.StatusBadRequest,
		},
		{
			name: "CPA legacy top level",
			payload: `{"type":"error","code":"Cyber_Policy","message":"legacy","sequence_number":0}`,
			transportStatus: http.StatusOK,
			wantCode: "Cyber_Policy", wantType: "",
			wantMessage: "legacy", wantSemantic: http.StatusBadRequest,
		},
		{
			name: "top level event type is not semantic type",
			payload: `{"type":"error","code":"provider_error","message":"failed"}`,
			transportStatus: http.StatusOK,
			wantCode: "provider_error", wantType: "",
			wantMessage: "failed", wantSemantic: http.StatusBadGateway,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractOpenAIResponsesErrorFields([]byte(tt.payload), tt.transportStatus)
			require.Equal(t, tt.wantCode, got.Code)
			require.Equal(t, tt.wantType, got.Type)
			require.Equal(t, tt.wantMessage, got.Message)
			require.Equal(t, tt.transportStatus, got.TransportStatus)
			require.Equal(t, tt.wantSemantic, got.SemanticStatus)
		})
	}
}
```

- [ ] **Step 2: Run RED**

Run:

```bash
docker run --rm -v /tmp/sub2api-cyber-lineage:/src -v /tmp/sub2api-cyber-lineage-go-cache/mod:/go/pkg/mod -v /tmp/sub2api-cyber-lineage-go-cache/build:/root/.cache/go-build -w /src/backend golang:1.27 sh -c 'go test ./internal/service -run TestExtractOpenAIResponsesErrorFields -count=1'
```

Expected: compile failure because the new type/function does not exist.

- [ ] **Step 3: Implement the pure extractor**

Create `openai_response_error_fields.go` with a small first-non-empty helper and explicit status mapping:

```go
package service

import (
	"net/http"
	"strings"

	"github.com/tidwall/gjson"
)

type openAIResponsesErrorFields struct {
	Code            string
	Type            string
	Message         string
	TransportStatus int
	SemanticStatus  int
}

func firstOpenAIResponsesErrorString(payload []byte, paths ...string) string {
	for _, path := range paths {
		if value := strings.TrimSpace(gjson.GetBytes(payload, path).String()); value != "" {
			return value
		}
	}
	return ""
}

func extractOpenAIResponsesErrorFields(payload []byte, transportStatus int) openAIResponsesErrorFields {
	fields := openAIResponsesErrorFields{
		Code: firstOpenAIResponsesErrorString(payload,
			"error.code", "response.error.code", "code"),
		Type: firstOpenAIResponsesErrorString(payload,
			"error.type", "response.error.type"),
		Message: firstOpenAIResponsesErrorString(payload,
			"response.error.message", "error.message", "message"),
		TransportStatus: transportStatus,
		SemanticStatus:  http.StatusBadGateway,
	}
	for _, path := range []string{
		"response.error.status_code", "error.status_code", "status_code",
		"response.error.status", "error.status", "status",
	} {
		if status := int(gjson.GetBytes(payload, path).Int()); status >= 400 && status <= 599 {
			fields.SemanticStatus = status
			break
		}
	}
	code := strings.ToLower(strings.TrimSpace(fields.Code))
	errType := strings.ToLower(strings.TrimSpace(fields.Type))
	switch {
	case code == "cyber_policy":
		fields.SemanticStatus = http.StatusBadRequest
	case strings.Contains(code, "rate_limit"):
		fields.SemanticStatus = http.StatusTooManyRequests
	case strings.Contains(errType, "invalid_request"):
		fields.SemanticStatus = http.StatusBadRequest
	}
	return fields
}
```

Keep broader authentication/capacity/access-state mapping in the existing specialized functions; the extractor establishes fields and the exact cyber semantic mapping without replacing unrelated policy code.

- [ ] **Step 4: Run GREEN and commit the pure unit**

Run:

```bash
docker run --rm -v /tmp/sub2api-cyber-lineage:/src -v /tmp/sub2api-cyber-lineage-go-cache/mod:/go/pkg/mod -v /tmp/sub2api-cyber-lineage-go-cache/build:/root/.cache/go-build -w /src/backend golang:1.27 sh -c 'gofmt -w internal/service/openai_response_error_fields.go internal/service/openai_response_error_fields_test.go && go test ./internal/service -run TestExtractOpenAIResponsesErrorFields -count=1'
git add backend/internal/service/openai_response_error_fields.go backend/internal/service/openai_response_error_fields_test.go
git commit -m "test(openai): define Responses error field contract"
```

Expected: test PASS and a self-contained commit with only the new extractor and tests.

### Task 3: Route cyber detection, synthesis, status, and WS logging through the shared fields

**Files:**

- Modify: `backend/internal/service/openai_cyber_policy.go`
- Modify: `backend/internal/service/openai_gateway_response_handling.go`
- Modify: `backend/internal/service/openai_gateway_passthrough.go`
- Modify: `backend/internal/service/openai_ws_http_bridge.go`
- Modify: `backend/internal/service/openai_ws_forwarder_logutil.go`
- Modify: `backend/internal/service/openai_ws_forwarder_support.go`
- Create: `backend/internal/service/openai_cpa_legacy_cyber_test.go`

**Interfaces:**

- Consumes: `extractOpenAIResponsesErrorFields` from Task 2.
- Produces: identical `cyber_policy` client and mark behavior for A/B/C.
- Guarantees: CPA legacy cyber is semantic 400, never failoverable, and never account-health actionable.

- [ ] **Step 1: Write the cross-path RED tests**

Create tests that use exactly:

```go
const cpaLegacyCyberEvent = `{"type":"error","code":"cyber_policy","message":"blocked by CPA","sequence_number":0}`
```

Cover these assertions:

```go
hit, code, message := detectOpenAICyberPolicy([]byte(cpaLegacyCyberEvent))
require.True(t, hit)
require.Equal(t, "cyber_policy", code)
require.Equal(t, "blocked by CPA", message)

httpSSE := buildOpenAIResponseFailedSSE("resp_cpa", "gpt-5.6-sol", []byte(cpaLegacyCyberEvent), "")
require.Equal(t, "cyber_policy", gjson.Get(httpSSEData(httpSSE), "response.error.code").String())

wsEvent := buildOpenAIWSHTTPBridgeFailedEvent("resp_cpa", "gpt-5.6-sol", []byte(cpaLegacyCyberEvent), "")
require.Equal(t, "cyber_policy", gjson.GetBytes(wsEvent, "response.error.code").String())

require.Equal(t, "cyber_policy", openAIStreamFailedEventErrorCode([]byte(cpaLegacyCyberEvent)))
require.Equal(t, http.StatusBadRequest, openAIStreamFailedEventSemanticStatus([]byte(cpaLegacyCyberEvent), "blocked by CPA"))
require.False(t, openAIStreamErrorEventShouldFailover([]byte(cpaLegacyCyberEvent), "please retry after policy review"))
```

Use a local test helper to extract the JSON after `data:`; do not change production code for test parsing.

Add table cases to existing native/passthrough/WS bridge tests proving:

- a CPA bare event followed by EOF produces one `response.failed` with code `cyber_policy`;
- `GetOpsCyberPolicy(c)` is non-nil;
- account state repositories/rate-limit fakes receive zero cooldown/disable calls;
- bare `error` followed by nested `response.failed` records one mark and one usage decision.

- [ ] **Step 2: Run RED**

Run:

```bash
docker run --rm -v /tmp/sub2api-cyber-lineage:/src -v /tmp/sub2api-cyber-lineage-go-cache/mod:/go/pkg/mod -v /tmp/sub2api-cyber-lineage-go-cache/build:/root/.cache/go-build -w /src/backend golang:1.27 sh -c 'go test ./internal/service -run "CPALegacyCyber|DetectOpenAICyberPolicy|BareError" -count=1'
```

Expected: failures showing missing detector hit, `upstream_error`, semantic 502, and/or missing mark.

- [ ] **Step 3: Replace duplicated field reads**

Implement these exact behavior changes:

```go
func detectOpenAICyberPolicy(payload []byte) (bool, string, string) {
	fields := extractOpenAIResponsesErrorFields(payload, 0)
	if !strings.EqualFold(strings.TrimSpace(fields.Code), "cyber_policy") {
		return false, "", ""
	}
	return true, "cyber_policy", strings.TrimSpace(fields.Message)
}
```

In both synthetic failed builders:

```go
fields := extractOpenAIResponsesErrorFields(source, http.StatusOK)
errorType := fields.Type
code := fields.Code
if code == "" {
	code = "upstream_error"
}
message := sanitizeUpstreamErrorMessage(fields.Message)
```

In `openAIStreamFailedEventErrorCode`, return lowercased normalized `fields.Code`.

In `openAIStreamFailedEventSemanticStatus`, return `fields.SemanticStatus` immediately for exact cyber policy before the existing specialized auth/access/capacity logic.

In `parseOpenAIWSErrorEventFields`, return `fields.Code`, `fields.Type`, and `fields.Message`.

In `openAIWSPayloadTransientStatus`, use normalized code/type fields, but return 0 for `cyber_policy` so no transient account mutation is possible.

Extend `sanitizeOpenAICapacityShedErrorCodeForClient` to rewrite a top-level `code` only when that field exists and equals a recognized capacity code. Never rewrite `cyber_policy`.

- [ ] **Step 4: Run focused GREEN and neighboring regression tests**

Run:

```bash
docker run --rm -v /tmp/sub2api-cyber-lineage:/src -v /tmp/sub2api-cyber-lineage-go-cache/mod:/go/pkg/mod -v /tmp/sub2api-cyber-lineage-go-cache/build:/root/.cache/go-build -w /src/backend golang:1.27 sh -c 'gofmt -w internal/service/openai_cyber_policy.go internal/service/openai_gateway_response_handling.go internal/service/openai_gateway_passthrough.go internal/service/openai_ws_http_bridge.go internal/service/openai_ws_forwarder_logutil.go internal/service/openai_ws_forwarder_support.go internal/service/openai_cpa_legacy_cyber_test.go && go test ./internal/service -run "CPALegacyCyber|DetectOpenAICyberPolicy|ResponsesStreamCyberPolicy|WSHTTPBridge.*Cyber|Ingress.*Cyber|CapacityShed" -count=1'
```

Expected: all selected tests PASS.

- [ ] **Step 5: Commit the transport-normalization milestone**

Run:

```bash
git diff --check
git add backend/internal/service/openai_cyber_policy.go backend/internal/service/openai_gateway_response_handling.go backend/internal/service/openai_gateway_passthrough.go backend/internal/service/openai_ws_http_bridge.go backend/internal/service/openai_ws_forwarder_logutil.go backend/internal/service/openai_ws_forwarder_support.go backend/internal/service/openai_cpa_legacy_cyber_test.go
git commit -m "fix(openai): normalize CPA legacy cyber errors"
```

### Task 4: Add the optional Redis response-lineage store

**Files:**

- Modify: `backend/internal/service/openai_cyber_session_block.go`
- Modify: `backend/internal/repository/gateway_cache.go`
- Modify: `backend/internal/repository/gateway_cache_cyber_test.go`

**Interfaces:**

- Produces `CyberSessionLineageStore` with `BindCyberSessionRoot` and `GetCyberSessionRoot` signatures from the design.
- Redis miss returns `found=false, err=nil`.
- Raw API keys, response IDs, and roots do not appear in Redis key names.

- [ ] **Step 1: Write failing repository round-trip and isolation tests**

Add miniredis tests equivalent to:

```go
func TestGatewayCacheCyberSessionLineageRoundTripAndIsolation(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	store, ok := NewGatewayCache(client).(service.CyberSessionLineageStore)
	require.True(t, ok)

	ctx := context.Background()
	require.NoError(t, store.BindCyberSessionRoot(ctx, 7, 11, "resp_1", "root-a", time.Minute))
	root, found, err := store.GetCyberSessionRoot(ctx, 7, 11, "resp_1")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "root-a", root)

	_, found, err = store.GetCyberSessionRoot(ctx, 7, 12, "resp_1")
	require.NoError(t, err)
	require.False(t, found)
	_, found, err = store.GetCyberSessionRoot(ctx, 8, 11, "resp_1")
	require.NoError(t, err)
	require.False(t, found)
}
```

Also assert that the visible Redis keys contain neither `resp_1` nor `root-a` and that expiry removes the mapping.

- [ ] **Step 2: Run RED**

Run:

```bash
docker run --rm -v /tmp/sub2api-cyber-lineage:/src -v /tmp/sub2api-cyber-lineage-go-cache/mod:/go/pkg/mod -v /tmp/sub2api-cyber-lineage-go-cache/build:/root/.cache/go-build -w /src/backend golang:1.27 sh -c 'go test ./internal/repository -run TestGatewayCacheCyberSessionLineage -count=1'
```

Expected: compile failure because `CyberSessionLineageStore` and its methods do not exist.

- [ ] **Step 3: Implement the narrow optional store**

In service:

```go
type CyberSessionLineageStore interface {
	BindCyberSessionRoot(ctx context.Context, groupID, apiKeyID int64, responseID, root string, ttl time.Duration) error
	GetCyberSessionRoot(ctx context.Context, groupID, apiKeyID int64, responseID string) (string, bool, error)
}
```

In repository, add a compile assertion and hashed key builder:

```go
const cyberSessionLineagePrefix = "cyber_session_lineage:"

var _ service.CyberSessionLineageStore = (*gatewayCache)(nil)

func cyberSessionLineageKey(groupID, apiKeyID int64, responseID string) string {
	raw := fmt.Sprintf("v1|group=%d|api_key=%d|response=%s", groupID, apiKeyID, strings.TrimSpace(responseID))
	sum := sha256.Sum256([]byte(raw))
	return cyberSessionLineagePrefix + hex.EncodeToString(sum[:])
}
```

`BindCyberSessionRoot` validates positive IDs and non-empty response/root, normalizes non-positive TTL to one hour, and stores the root string. `GetCyberSessionRoot` treats `redis.Nil` as a miss and returns other Redis errors.

- [ ] **Step 4: Run GREEN and commit**

Run:

```bash
docker run --rm -v /tmp/sub2api-cyber-lineage:/src -v /tmp/sub2api-cyber-lineage-go-cache/mod:/go/pkg/mod -v /tmp/sub2api-cyber-lineage-go-cache/build:/root/.cache/go-build -w /src/backend golang:1.27 sh -c 'gofmt -w internal/service/openai_cyber_session_block.go internal/repository/gateway_cache.go internal/repository/gateway_cache_cyber_test.go && go test ./internal/repository -run "GatewayCacheCyberSession" -count=1'
git add backend/internal/service/openai_cyber_session_block.go backend/internal/repository/gateway_cache.go backend/internal/repository/gateway_cache_cyber_test.go
git commit -m "feat(openai): persist cyber response lineages"
```

### Task 5: Resolve request identities and block `previous_response_id` chains

**Files:**

- Modify: `backend/internal/service/openai_cyber_session_block.go`
- Modify: `backend/internal/service/openai_cyber_session_block_test.go`
- Modify: `backend/internal/handler/openai_gateway_handler.go`
- Modify: `backend/internal/handler/openai_gateway_cyber_test.go`

**Interfaces:**

- Produces `CyberSessionIdentity` from the design.
- Produces `PrepareCyberSessionIdentity`, `BindCyberSessionIdentity`, `ObserveCyberSessionResponseID`, and idempotent activation helpers.
- Existing `FindCyberSessionBlockedForRequest` remains available and delegates through the bound identity when present.

- [ ] **Step 1: Write failing lineage-resolution tests**

Cover:

```text
explicit session -> stable root
previous_response_id mapped -> stored root
previous_response_id missing -> deterministic API-key-isolated root
no explicit/previous -> non-empty request-local root
different API key -> different root
same IP/UA but different root -> not blocked
blocked root -> rejected before transcript lookup
lineage store error -> explicit/transcript behavior remains fail-open
```

Use a fake implementing both optional cyber interfaces. Assert lookup order by recording method calls: explicit block first, lineage root second, scope-gated transcript last.

- [ ] **Step 2: Run RED**

Run:

```bash
docker run --rm -v /tmp/sub2api-cyber-lineage:/src -v /tmp/sub2api-cyber-lineage-go-cache/mod:/go/pkg/mod -v /tmp/sub2api-cyber-lineage-go-cache/build:/root/.cache/go-build -w /src/backend golang:1.27 sh -c 'go test ./internal/service ./internal/handler -run "CyberSessionIdentity|PreviousResponseLineage" -count=1'
```

- [ ] **Step 3: Implement identity preparation and lookup**

Use a request-context identity containing:

```go
type CyberSessionIdentity struct {
	mu                  sync.Mutex
	gateway             *OpenAIGatewayService
	groupID             int64
	apiKeyID            int64
	explicitKey         string
	lineageRoot         string
	transcriptKeys      []string
	scopeKey            string
	observedResponseIDs map[string]struct{}
	activated           bool
}
```

Derive `previous_response_id` from `openAIRequestPayloadView(body)`. Use the lineage store when available. A missing mapping uses:

```go
hashCyberSessionBlockKey(apiKeyID, "response-lineage:"+previousResponseID)
```

A request without explicit or previous signals uses a UUID-derived request root, still passed through `hashCyberSessionBlockKey` for a uniform opaque key.

`PrepareCyberSessionIdentity` must return nil without store work when the runtime block setting is disabled.

`FindCyberSessionBlockedForRequest` checks:

1. explicit key;
2. lineage root when different from explicit key;
3. existing scope-gated transcript keys.

The scope alone never returns blocked.

- [ ] **Step 4: Bind identity at HTTP and WS request entry**

In `findBlockedCyberSessionKey`, resolve group ID from context, prepare the identity, bind it to Gin context, then perform the ordered lookup. Reuse this helper for `/v1/responses`, Chat Completions, Messages, and the first WS turn.

In WS `BeforeRequest`, prepare and replace the per-turn identity for every subsequent `response.create`, before security/account-slot work. Preserve `cyberBlockedThisConn` as defense in depth.

- [ ] **Step 5: Run GREEN and commit**

Run:

```bash
docker run --rm -v /tmp/sub2api-cyber-lineage:/src -v /tmp/sub2api-cyber-lineage-go-cache/mod:/go/pkg/mod -v /tmp/sub2api-cyber-lineage-go-cache/build:/root/.cache/go-build -w /src/backend golang:1.27 sh -c 'gofmt -w internal/service/openai_cyber_session_block.go internal/service/openai_cyber_session_block_test.go internal/handler/openai_gateway_handler.go internal/handler/openai_gateway_cyber_test.go && go test ./internal/service ./internal/handler -run "CyberSession|PreviousResponseLineage" -count=1'
git add backend/internal/service/openai_cyber_session_block.go backend/internal/service/openai_cyber_session_block_test.go backend/internal/handler/openai_gateway_handler.go backend/internal/handler/openai_gateway_cyber_test.go
git commit -m "feat(openai): resolve cyber session response lineages"
```

### Task 6: Observe response IDs and activate blocks before client visibility

**Files:**

- Modify: `backend/internal/service/openai_cyber_policy.go`
- Modify: `backend/internal/service/openai_cyber_session_block.go`
- Modify: `backend/internal/service/openai_gateway_response_handling.go`
- Modify: `backend/internal/service/openai_gateway_passthrough.go`
- Modify: `backend/internal/service/openai_ws_http_bridge.go`
- Modify: `backend/internal/service/openai_ws_forwarder_v2.go`
- Modify: `backend/internal/service/openai_ws_forwarder_ingress.go`
- Modify: `backend/internal/handler/openai_gateway_handler.go`
- Modify tests in the corresponding service/handler cyber and lifecycle files.

**Interfaces:**

- Consumes: bound `CyberSessionIdentity` and lineage store.
- Produces: `ObserveCyberSessionResponseID(c *gin.Context, responseID string)` and synchronous activation invoked by the first `MarkOpsCyberPolicy`.
- Guarantees: healthy Redis can answer the block lookup before downstream cyber delivery.

- [ ] **Step 1: Write a failing ordering test**

Use a fake block/lineage store and downstream writer. Record ordered labels:

```text
lineage_bind
block_set
downstream_write
```

Feed CPA's exact top-level event through native streaming, passthrough, and WS HTTP bridge. Assert:

```go
require.Equal(t, []string{"lineage_bind", "block_set", "downstream_write"}, events)
```

Also test `error` followed by `response.failed` and require one block activation.

- [ ] **Step 2: Run RED**

Run:

```bash
docker run --rm -v /tmp/sub2api-cyber-lineage:/src -v /tmp/sub2api-cyber-lineage-go-cache/mod:/go/pkg/mod -v /tmp/sub2api-cyber-lineage-go-cache/build:/root/.cache/go-build -w /src/backend golang:1.27 sh -c 'go test ./internal/service ./internal/handler -run "CyberBlockBeforeClientWrite|CyberLineageResponseID|CPALegacyCyber" -count=1'
```

- [ ] **Step 3: Observe response IDs at existing extraction points**

Immediately after every successful response-ID extraction, call the observer before any downstream write:

```go
if responseID == "" {
	responseID = extractOpenAIResponseIDFromJSONBytes(dataBytes)
}
ObserveCyberSessionResponseID(c, responseID)
```

Apply the equivalent call at:

- native HTTP SSE
- passthrough HTTP SSE
- WS HTTP bridge
- native WS v2
- pooled WS ingress
- non-streaming response binding before handler return

The identity deduplicates repeated observations, so completed/failed frames do not repeat Redis writes for the same ID.

- [ ] **Step 4: Activate from the central mark point**

After `MarkOpsCyberPolicy` wins first-mark storage, synchronously invoke the bound identity activator. Activation snapshots observed IDs under its mutex, binds them to the root, and stores root/explicit/transcript block keys. Set `activated=true` only after the in-process activation attempt completes; repeated error/failed frames become no-ops.

Keep the handler's `recordCyberPolicyIfMarked` write plan as an idempotent fallback. It must include the lineage root when an identity is bound.

- [ ] **Step 5: Verify full handler lifecycle behavior**

Add an end-to-end handler test with `cyber_session_block_enabled=true`:

1. First request has no explicit session and receives `resp_1`.
2. Second request sends `previous_response_id=resp_1` and receives CPA legacy cyber with `resp_2`.
3. Before the second downstream failure is written, lookup of both `resp_1` and `resp_2` roots reports blocked.
4. Third request with `previous_response_id=resp_1` or `resp_2` is rejected before account selection with HTTP 403 and `session_blocked_by_cyber_policy`.
5. A new request without previous ID is allowed.
6. The upstream account's health/status mocks remain unchanged.
7. Moderation, ops, usage, and block activation each occur once.

- [ ] **Step 6: Run focused GREEN and commit**

Run:

```bash
docker run --rm -v /tmp/sub2api-cyber-lineage:/src -v /tmp/sub2api-cyber-lineage-go-cache/mod:/go/pkg/mod -v /tmp/sub2api-cyber-lineage-go-cache/build:/root/.cache/go-build -w /src/backend golang:1.27 sh -c 'gofmt -w internal/service/openai_cyber_policy.go internal/service/openai_cyber_session_block.go internal/service/openai_gateway_response_handling.go internal/service/openai_gateway_passthrough.go internal/service/openai_ws_http_bridge.go internal/service/openai_ws_forwarder_v2.go internal/service/openai_ws_forwarder_ingress.go internal/handler/openai_gateway_handler.go && go test ./internal/service ./internal/handler ./internal/repository -run "Cyber|CPALegacy|ResponseLineage" -count=1'
git add -- \
  backend/internal/service/openai_cyber_policy.go \
  backend/internal/service/openai_cyber_session_block.go \
  backend/internal/service/openai_cyber_session_block_test.go \
  backend/internal/service/openai_cpa_legacy_cyber_test.go \
  backend/internal/service/openai_gateway_response_handling.go \
  backend/internal/service/openai_gateway_passthrough.go \
  backend/internal/service/openai_ws_http_bridge.go \
  backend/internal/service/openai_ws_forwarder_v2.go \
  backend/internal/service/openai_ws_forwarder_ingress.go \
  backend/internal/handler/openai_gateway_handler.go \
  backend/internal/handler/openai_gateway_cyber_test.go
git commit -m "fix(openai): block cyber response lineages before delivery"
```

Before committing, inspect the staged name list and unstage any unrelated service file; do not rely on the broad directory path without review:

```bash
git diff --cached --name-status
```

### Task 7: Simplify, verify, and squash the implementation into one front patch

**Files:**

- Review every file changed in Tasks 2–6.
- Final commit parent: `081537dee`.

**Interfaces:**

- Produces one patch `fix(openai): normalize cyber errors and block response lineages`.

- [ ] **Step 1: Run code simplification review without changing behavior**

Check for duplicated field precedence, repeated root derivation, unnecessary exported APIs, lock scope around Redis calls, and handler/service ownership. Keep Redis I/O outside mutexes; only snapshot/deduplicate state under lock.

- [ ] **Step 2: Run focused packages and race-sensitive tests**

Run:

```bash
docker run --rm -v /tmp/sub2api-cyber-lineage:/src -v /tmp/sub2api-cyber-lineage-go-cache/mod:/go/pkg/mod -v /tmp/sub2api-cyber-lineage-go-cache/build:/root/.cache/go-build -w /src/backend golang:1.27 sh -c 'go test ./internal/service ./internal/handler ./internal/repository -run "Cyber|CPALegacy|ResponseLineage" -count=1 && go test -race ./internal/service ./internal/handler -run "CyberSessionIdentity|CyberBlockBeforeClientWrite" -count=1'
```

- [ ] **Step 3: Squash task commits into one patch**

Create a backup, then squash only commits after `081537dee`:

```bash
git branch backup/cyber-lineage-before-squash-20260906 HEAD
git reset --soft 081537dee
git diff --cached --check
git diff --cached --name-status
git commit -m "fix(openai): normalize cyber errors and block response lineages"
test "$(git rev-parse HEAD^)" = "$(git rev-parse 081537dee)"
```

### Task 8: Insert the patch into the current site stack and validate every later patch

**Files:**

- Rewrite branch: `/tmp/sub2api-cyber-lineage-site-rewrite`
- Preserve current `site` through a backup ref.
- Do not push in this task.

**Interfaces:**

- Consumes: the one feature patch from Task 7 and the current live `site` tip.
- Produces: a local rewritten site branch with the feature at patch position 6 and all later patches replayed.

- [ ] **Step 1: Refresh upstream/PR state and create recovery refs**

Run:

```bash
git -C /home/huqi/sub2api fetch origin main
git -C /home/huqi/sub2api fetch fork site cyber-ws-handshake-policy-fix
gh pr view 6645 --repo Wei-Shaw/sub2api --json state,headRefOid,mergedAt
git -C /home/huqi/sub2api branch backup/site-before-cyber-lineage-20260906 site
git -C /home/huqi/sub2api worktree add -b site-cyber-lineage-rewrite /tmp/sub2api-cyber-lineage-site-rewrite site
```

If PR #6645 merged after Task 1, stop and rebuild the insertion base from refreshed upstream; do not replay `081537dee` as a duplicate.

- [ ] **Step 2: Insert the feature after `081537dee`**

Assuming #6645 remains open and patch-equivalent:

```bash
feature_tip=$(git -C /tmp/sub2api-cyber-lineage rev-parse HEAD)
git -C /tmp/sub2api-cyber-lineage-site-rewrite rebase --onto "$feature_tip" 081537dee
```

Resolve conflicts additively. For every conflict, inspect the original later patch with `git show REBASE_HEAD`, preserve the new normalized error/session APIs, then retain the later patch's independent behavior. Search for conflict markers before continuing:

```bash
rg -n '^<<<<<<<|^=======|^>>>>>>>' backend frontend deploy docs || true
git diff --check
GIT_EDITOR=true git rebase --continue
```

- [ ] **Step 3: Verify patch order and range-diff**

Run:

```bash
git -C /tmp/sub2api-cyber-lineage-site-rewrite log --reverse --oneline origin/main..HEAD | nl -ba | head -n 12
git -C /tmp/sub2api-cyber-lineage-site-rewrite range-diff --no-color origin/main..backup/site-before-cyber-lineage-20260906 origin/main..HEAD
git -C /tmp/sub2api-cyber-lineage-site-rewrite diff --check
```

Expected:

```text
1-4 TLS patches
5   handshake cyber patch equivalent to #6645
6   normalize cyber errors and block response lineages
7+  every former later patch paired as patch-equivalent or explicitly reviewed adaptation
```

No former site patch may disappear without an explicit explanation.

- [ ] **Step 4: Run the complete final-tree verification**

Run build/unit:

```bash
docker run --rm -v /tmp/sub2api-cyber-lineage-site-rewrite:/src -v /tmp/sub2api-cyber-lineage-go-cache/mod:/go/pkg/mod -v /tmp/sub2api-cyber-lineage-go-cache/build:/root/.cache/go-build -w /src/backend golang:1.27 sh -c 'go build ./... && make test-unit'
```

Run real integration:

```bash
docker run --rm \
  -v /tmp/sub2api-cyber-lineage-site-rewrite:/src \
  -v /tmp/sub2api-cyber-lineage-go-cache/mod:/go/pkg/mod \
  -v /tmp/sub2api-cyber-lineage-go-cache/build:/root/.cache/go-build \
  -v /usr/bin/docker:/usr/bin/docker \
  -v /run/user/1510000028/docker.sock:/var/run/docker.sock \
  --network host \
  -e DOCKER_HOST=unix:///var/run/docker.sock \
  -e TESTCONTAINERS_RYUK_DISABLED=true \
  -e SUB2API_TEST_POSTGRES_IMAGE=postgres:16-alpine \
  -w /src/backend golang:1.27 sh -c 'make test-integration'
```

Run lint:

```bash
docker run --rm -v /tmp/sub2api-cyber-lineage-site-rewrite:/src -v /tmp/sub2api-cyber-lineage-go-cache/mod:/go/pkg/mod -v /tmp/sub2api-cyber-lineage-go-cache/build:/root/.cache/go-build -w /src/backend golangci/golangci-lint:v2.13.2 golangci-lint run --timeout=30m
```

Run frontend checks only if conflict resolution or range-diff shows any frontend change:

```bash
make test-frontend
```

- [ ] **Step 5: Report the local result and stop before publication**

Report:

- feature patch SHA and parent SHA
- final rewritten site SHA
- PR #6645 state at integration time
- range-diff equivalence/adaptation counts
- exact test outputs
- any later patch requiring semantic conflict adaptation
- clean status for all created worktrees

Do not update local `site`, force-push `fork/site`, create a tag, modify PR #6645, or restart a service without a new explicit publication instruction.
