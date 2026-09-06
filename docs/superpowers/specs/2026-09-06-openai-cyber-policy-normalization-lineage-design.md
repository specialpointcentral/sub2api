# OpenAI Cyber-Policy Normalization and Session Lineage Design

## Status and scope

This design follows the read-only source investigation of Sub2API `site` at
`4b90290f8` and CLIProxyAPI `v7.2.151` at
`5208aec703b5ce7e3445f6e9d91cc13b3e78003a`.

The change has two inseparable outcomes:

1. Preserve `cyber_policy` across HTTP, Responses SSE, passthrough, and
   WebSocket error shapes, including CPA's legacy top-level event.
2. Block the same downstream Responses session immediately, including
   non-Codex clients that use `previous_response_id` instead of a stable Codex
   session header.

The block is session-scoped. A cyber-policy rejection must never cool down,
disable, or rotate an otherwise healthy CPA/OpenAI upstream account.

## Verified root cause

Sub2API currently recognizes these shapes:

```json
{"error":{"type":"invalid_request","code":"cyber_policy","message":"blocked"}}
```

```json
{"type":"response.failed","response":{"error":{"type":"invalid_request","code":"cyber_policy","message":"blocked"}}}
```

CPA also emits this legacy streaming shape for clients it does not identify as
official Codex:

```json
{"type":"error","code":"cyber_policy","message":"blocked","sequence_number":0}
```

`effectiveOpenAISSEEventType` recognizes the last payload as an `error`, and the
message extractor reads top-level `message`. However, the HTTP and WS synthetic
`response.failed` builders, cyber detector, stream code/status classifier, and
WS error field parser do not read top-level `code`. The builders therefore emit
`upstream_error`, and the detector never creates `CyberPolicyMark`.

Without the mark, the handler skips dedicated risk-control recording, cyber
usage classification, and session-block activation.

## Goals

- Support the three verified error shapes with one parsing contract.
- Preserve `code=cyber_policy` and the upstream message in every client-visible
  Responses failure.
- Treat cyber policy as semantic HTTP 400 while retaining the actual SSE
  transport status (normally HTTP 200) as separate evidence.
- Short-circuit failover and account-health mutation before generic error
  handling.
- Activate the session block before the cyber failure becomes client-visible.
- Recognize non-Codex Responses chains through `previous_response_id` lineage.
- Keep the existing explicit-session and transcript-prefix mechanisms.
- Allow a genuinely new session to proceed.
- Preserve API-key isolation, bounded Redis work, TTL expiration, idempotent
  error/failed-pair handling, and fail-open behavior on infrastructure errors.

## Non-goals

- Do not infer cyber policy from free-form message text.
- Do not block an entire API key, IP address, User-Agent, user, group, or
  upstream account because one session was rejected.
- Do not make `cyber_session_block_enabled` default to true.
- Do not treat top-level event `type="error"` as semantic
  `error.type="invalid_request"`.
- Do not change CPA or require CPA to impersonate an official Codex client.
- Do not require third-party clients to implement a new proprietary header.
- Do not amend or broaden upstream PR #6645.
- Do not create or update an upstream PR unless separately requested.

## Error normalization

### Shared representation

Introduce a pure error-field extractor with a single documented result:

```go
type openAIResponsesErrorFields struct {
	Code            string
	Type            string
	Message         string
	TransportStatus int
	SemanticStatus  int
}
```

The extractor accepts a payload plus the known transport status. It preserves
existing precedence and adds the CPA fallback:

```text
code:
  error.code
  response.error.code
  code

semantic type:
  error.type
  response.error.type

message:
  response.error.message
  error.message
  message

explicit status:
  response.error.status_code
  error.status_code
  status_code
  response.error.status
  error.status
  status
```

Top-level `type` is excluded from semantic type because it is the event kind in
CPA's legacy frame.

`code` matching is case-insensitive after trimming. Exact `cyber_policy` maps to
semantic status 400. The transport status remains the actual HTTP status, such
as 200 for an established SSE stream.

### Consumers

The shared extractor replaces duplicated field selection in:

- `detectOpenAICyberPolicy`
- `buildOpenAIResponseFailedSSE`
- `buildOpenAIWSHTTPBridgeFailedEvent`
- `openAIStreamFailedEventErrorCode`
- `openAIStreamFailedEventSemanticStatus`
- `parseOpenAIWSErrorEventFields`
- `openAIWSPayloadTransientStatus`
- capacity-shed client-code rewriting when the source uses a top-level code

Existing non-cyber behavior remains unchanged unless it uses the same verified
top-level legacy schema.

### Processing order

Every HTTP/SSE/WS error path follows this order:

```text
parse normalized fields
    -> exact cyber_policy?
       -> mark cyber
       -> activate session block
       -> preserve client code/message
       -> skip failover
       -> skip account health mutation
    -> otherwise execute existing generic classification/failover logic
```

An `error` followed by an authoritative `response.failed` remains one logical
failure. Existing first-mark/recorded guards and idempotent Redis SETs prevent
duplicate moderation records, usage rows, and block counts.

## Non-Codex session identity

### Existing strong signals

The current explicit signals remain highest priority:

```text
session-id
session_id
conversation_id
X-Session-Affinity
X-Session-Id
X-OpenCode-Session
X-Conversation-ID
prompt_cache_key
```

The exact block key remains isolated by the downstream Sub2API API-key ID.

### Response lineage

Add a session-root mapping for standard Responses continuation:

```text
(group ID, API-key ID, response ID) -> session root
```

Session-root resolution at request entry is:

1. If an explicit session signal exists, its existing exact block key is also
   the lineage root.
2. Otherwise, if `previous_response_id` exists, resolve its stored lineage root.
3. If the previous response has no mapping, derive a deterministic root from
   the API-key ID and `previous_response_id`; bind subsequent response IDs to
   that root.
4. If neither signal exists, allocate a random request-local root. Bind the
   first observed response ID to it.

The root is kept in request context across same-request account failover.
Changing the upstream account must not create a new downstream session.

Whenever a response ID is observed, bind it to the current root before making
that event visible downstream. This applies to `response.created`, terminal
events, non-streaming Responses results, HTTP passthrough, WS HTTP bridge, and
native WS paths.

At a cyber hit, block the root and bind any response ID contained in or already
observed for the current turn. A later request using any mapped
`previous_response_id` resolves to the blocked root and is rejected before
account selection.

Lineage mappings use the cyber-session TTL and refresh on each turn. An idle
chain that outlives the TTL is intentionally treated as a new session, matching
block expiration semantics.

### Transcript fallback

The existing semantic transcript hashes remain the final compatibility layer
for clients that resend history rather than use stable IDs. The existing
coarse scope remains:

```text
API-key ID + resolved client IP + normalized User-Agent
```

The scope only gates transcript parsing and Redis lookup. It is never itself a
block key. Different sessions behind the same NAT, API key, and SDK User-Agent
must not be blocked unless an exact session root or transcript prefix matches.

Clients that provide no explicit session signal, no `previous_response_id`, and
no repeated transcript are indistinguishable from a new session. The server
must not claim to identify those requests as the same session.

## Storage design

Extend the existing OpenAI response state store, which already persists
response ownership and response-to-account mappings, with lineage operations:

```go
BindCyberSessionRoot(
	ctx context.Context,
	groupID int64,
	apiKeyID int64,
	responseID string,
	root string,
	ttl time.Duration,
) error

GetCyberSessionRoot(
	ctx context.Context,
	groupID int64,
	apiKeyID int64,
	responseID string,
) (root string, found bool, err error)
```

Use both bounded in-process cache and Redis, following the existing HTTP
response-owner pattern. Redis keys contain hashes, not raw response IDs or
session identifiers. Different API keys cannot resolve each other's roots.

Lineage storage failure is fail-open and emits a bounded structured warning.
It must not make the model request unavailable when Redis is degraded.

## Immediate activation and race closure

At request entry, derive `CyberSessionIdentity` before account selection and
bind it to the Gin request context:

```go
type CyberSessionIdentity struct {
	ExplicitKey    string
	LineageRoot    string
	TranscriptKeys []string
	ScopeKey       string
}
```

Register a synchronous, idempotent block activator with the request context.
The first successful `MarkOpsCyberPolicy` invocation calls the activator before
the service writes the failure event downstream.

Activation performs, under the existing bounded timeout:

1. Bind the currently observed response ID to the lineage root.
2. Store the lineage root, explicit key, and transcript block keys.
3. Activate the coarse transcript scope only after exact keys succeed.
4. Mark activation complete for the request.

The handler's current post-forward block call remains as an idempotent fallback
for paths that cannot activate earlier. It is no longer the only write point.

The behavior when Redis is healthy is:

```text
detect cyber
    -> block is queryable
    -> write response.failed/code=cyber_policy
    -> handler records moderation and usage
```

Thus a client reacting immediately to the visible failure cannot race ahead of
the block write.

## Client and account behavior

The original rejected request remains an upstream cyber-policy failure:

- SSE transport status may be 200.
- Semantic status is 400.
- Client-visible code is `cyber_policy`.
- Original sanitized message is preserved.
- Upstream account remains schedulable.
- No generic failover occurs.

A later request resolving to the blocked session is rejected locally before
account selection:

```text
HTTP 403
code=session_blocked_by_cyber_policy
```

A request with no matching explicit key, lineage root, or transcript prefix is
a new session and proceeds normally.

## Configuration

Keep existing settings and defaults:

```text
cyber_session_block_enabled=false
cyber_session_block_ttl_seconds=3600
```

The immediate-block guarantee applies when the feature is enabled. Runtime
setting cache and fail-open behavior remain unchanged.

## Test strategy

### Error-shape matrix

For HTTP native streaming, HTTP passthrough, WS HTTP bridge, native WS v2, and
pooled ingress, test:

- nested `error.code`
- nested `response.error.code`
- CPA top-level `code`
- code and message preservation
- semantic status 400 with transport status 200
- cyber mark created before downstream write
- no failover
- no account cooldown/disable/temp-unschedulable transition
- paired `error`/`response.failed` records exactly once

### Session-lineage matrix

Test:

- same explicit session and API key is blocked despite IP/UA changes
- same explicit ID under a different API key is isolated
- response chain `resp_1 -> resp_2 -> resp_3` preserves one root
- cyber on a middle or terminal response blocks continuation through any mapped
  response ID
- first-turn response ID is bound before client visibility
- a request without a previous ID creates a new session root
- same API key/IP/UA with a different root is not blocked
- transcript-prefix fallback remains compatible
- UA version changes preserve the coarse scope
- lineage TTL refresh and expiration
- Redis read/write failures remain fail-open and observable
- overflow bounds and MGET batch limits remain intact
- setting disabled performs no lineage/block writes

### Integration tests

Use miniredis/store-instance tests for cross-instance persistence, handler tests
for pre-account-selection rejection, and end-to-end Responses SSE tests that
simulate CPA's exact legacy payload. No test may rely on message-only cyber
detection.

## Patch and PR placement

Verified upstream state:

- PR #6636, `fix(openai): record WS cyber policy failures across paths`, is
  merged into upstream `main` as merge commit `2dc287f2e`.
- PR #6645, `fix(openai): record cyber WS handshake failures`, remains open at
  `5af0a085a`. Its site equivalent `081537dee` has the same stable patch ID and
  is already the fifth private patch.

This change is a follow-up to merged PR #6636, not an extension of the narrowly
scoped handshake PR #6645. Do not amend #6645 or alter its remote branch.

The new cyber normalization/lineage implementation is one atomic private patch
placed immediately after `081537dee`, making it the sixth patch in
`origin/main..site` order. All later private patches are replayed unchanged on
top.

Because later patches touch OpenAI passthrough, identity, traffic policy,
account state, rate limiting, and handler code, history insertion requires:

- a pre-rewrite backup ref
- semantic conflict resolution, never wholesale ours/theirs
- `git range-diff` over the full private stack
- patch-ID checks for unaffected commits
- direct tree comparison where patch IDs change due to context
- focused error/session tests after the new patch
- full backend unit and real Testcontainers integration suites
- current golangci-lint
- frontend checks if any replay conflict touches frontend files

No force-push, tag, release, or PR update is authorized by this design alone.
