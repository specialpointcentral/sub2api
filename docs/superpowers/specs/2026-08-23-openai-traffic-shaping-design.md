# OpenAI traffic shaping design

## Scope and compatibility

Stage 4 adds configurable behavioral shaping for OpenAI inference traffic and automatic quota probes. Request pacing covers OpenAI OAuth and API Key accounts, while the active-thread cap remains specific to OpenAI OAuth accounts. Both controls are disabled by default, so existing request scheduling is unchanged until an administrator opts in. The quota probe keeps the existing ten-minute base interval and adds an internal, default-on uniform jitter of ±25 percent; administrator-forced refreshes are never delayed.

All configurable values live in the existing **Codex identity rectifier** settings section and use the established `SystemSettings -> setting keys -> runtime cache -> admin API -> SettingsView` chain.

## Settings

| Setting | Default | Valid range | Meaning |
| --- | ---: | ---: | --- |
| `openai_request_pacing_enabled` | `false` | boolean | Enable account-scoped inference-turn start pacing. |
| `openai_request_pacing_min_interval_ms` | `250` | `0..60000` | Lower bound for the next uniformly sampled start interval. |
| `openai_request_pacing_max_interval_ms` | `750` | `0..60000` | Upper bound; must not be below the lower bound. |
| `openai_account_thread_concurrency_limit` | `0` | `0..10000` | Global OpenAI OAuth active-turn cap; `0` means unlimited. |
| `openai_quota_probe_interval_minutes` | `10` | `1..1440` | Base automatic probe interval; ten minutes preserves the current frequency. |
| `openai_quota_probe_jitter_ratio` | `0.25` | `0..0.5` | Uniform jitter around the base interval. |

Invalid or unavailable stored values fail safe to these defaults. Runtime values use one short-lived cached snapshot so request hot paths do not query the settings database directly.

## OpenAI account request pacing

Pacing is a distributed, Redis-backed start gate keyed by OpenAI OAuth or API Key account ID. It does not hold a lock for the lifetime of a streamed response:

1. A request obtains the normal account concurrency slot.
2. Immediately before the upstream inference turn starts, it asks the atomic gate for permission.
3. The gate uses Redis server time and receives the caller's absolute 30-second admission deadline. If the account is eligible before that deadline, it records a new `next_start_at` plus a unique owner token using a uniform sample in the configured interval and lets the request proceed.
4. Otherwise it returns the remaining delay. The caller waits and retries until admitted, its context is canceled, or the 30-second shaping wait limit expires.
5. If cancellation or timeout races with a successful Redis admission, an owner-token CAS rollback removes only that caller's gate; a short-lived cancellation tombstone also prevents a delayed Lua execution from admitting it afterward.
6. Redis failures are logged and fail open, preserving request availability.

The gate is evaluated again after failover selects a different account. Because state is keyed by account ID, the old and new accounts never share pacing state. Only inference turns are paced; quota, authentication, token refresh, model manifest, and other auxiliary calls are excluded.

## Active account threads

No second semaphore or Redis namespace is introduced. The configured global cap is folded into every existing `AcquireAccountSlot` decision for OpenAI OAuth accounts:

```text
global cap == 0          => account.Concurrency
account.Concurrency <= 0 => global cap
otherwise                => min(account.Concurrency, global cap)
```

The effective limit must also be used in scheduler load snapshots, sticky and fallback wait plans, failover reacquisition, and WebSocket turns. Idle WebSocket connections do not occupy a slot; active turns do. Existing account wait counts, active-user reporting, cleanup, and release behavior therefore remain authoritative.

## Quota probe scheduling and identity

Automatic OpenAI probe eligibility becomes a per-account `nextProbeAt` gate. The deadline and schedule version are stored in Redis and claimed atomically with Redis server time, so replicas share one automatic probe window. A successful claim samples the next interval uniformly from:

```text
[baseInterval * (1 - jitterRatio), baseInterval * (1 + jitterRatio)]
```

Concurrent readers may not claim the same probe window. When shared schedule state is missing, the persisted `codex_usage_updated_at` plus the sampled interval initializes the first deadline, preventing a recently refreshed snapshot from being probed again while still honoring configured intervals shorter than ten minutes. Changing the base interval or jitter ratio invalidates the old deadline immediately. A forced administrator refresh bypasses this automatic gate without rewriting it. Redis failures are logged and fall back to a short-backoff process-local gate so quota reads remain available without retrying Redis on every read. Both regular `/responses` header probes and Spark `/wham/usage` probes use the same schedule.

Probe requests reuse the account's existing Codex outbound identity projection—resolved User-Agent/version/originator and TLS profile—and do not create a second probe-only persona.

## Commit and verification boundary

1. `feat(settings): configure OpenAI traffic shaping`
2. `feat(openai): pace OAuth request starts`
3. `feat(openai): cap active account threads`
4. `feat(openai): jitter quota probes`

Each behavior is developed red-green-refactor. Final verification covers backend unit packages `internal/service` and `internal/server`, plus frontend tests, typecheck, and formatting/lint checks.
