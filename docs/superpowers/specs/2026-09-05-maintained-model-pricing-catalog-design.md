# Maintained Model Pricing Catalog Design

## Status

Approved direction: publish a site-owned fork of the model-pricing catalog, keep
automatic LiteLLM synchronization for models outside the managed set, and make
the site-owned catalog the default remote pricing source for `sub2api`.

The GitHub fork already exists at
`specialpointcentral/model-price-repo`. The unfinished Kimi session created the
fork and left uncommitted exploratory changes in `/tmp/model-price-repo`; it did
not rebuild, test, commit, or publish the catalog, and it did not change the
`sub2api` defaults.

## Problem

`sub2api` currently defaults to the remote catalog published by
`Wei-Shaw/model-price-repo`. A downloaded remote entry takes precedence over the
repository's embedded catalog and static Go fallbacks. As a result, reverting a
price change only in the embedded JSON and fallbacks does not control production
billing while remote updates remain enabled.

This is visible for two GPT-5.6 models:

| Model | Current remote input/output per MTok | Site-owned input/output per MTok |
| --- | ---: | ---: |
| `gpt-5.6-luna` | $0.20 / $1.20 | $1.00 / $6.00 |
| `gpt-5.6-terra` | $2.00 / $12.00 | $2.50 / $15.00 |

`gpt-5.6-sol` remains outside the managed set because its remote and site-owned
prices already agree at $5.00 / $30.00 per MTok.

## Goals

- Make `specialpointcentral/model-price-repo` the default catalog for this
  `sub2api` fork.
- Keep automatic LiteLLM synchronization for every model not explicitly managed
  by the site.
- Make each managed model entry deterministic and free of price fields inherited
  from upstream.
- Ensure aliases of managed models are generated from the final managed entry,
  so an alias cannot retain the pre-override upstream price.
- Initially manage the complete `gpt-5.6-luna` and `gpt-5.6-terra` entries.
- Publish a matching SHA-256 file for every generated catalog revision.
- Prove standard, priority, flex, batch, cache, and long-context price behavior
  with automated tests.
- Resolve GPT-5.6 family suffixes to `sol`, `terra`, or `luna` before the generic
  OpenAI numeric-version fallback can match the differently priced bare
  `gpt-5.6` catalog entry.
- Preserve current `site` history and leave publishing or tagging the `sub2api`
  repository as a separate, explicit step.

## Non-goals

- Do not disable automatic price synchronization for the rest of the catalog.
- Do not use a deployment-local `pricing.override_file` as the primary fix.
- Do not change group or channel-specific pricing precedence.
- Do not add an administration UI for editing the catalog.
- Do not push `sub2api/site` or create a release tag as part of implementation
  without a later publication instruction.

## Architecture

The pricing fork remains a generated catalog. Its existing sync pipeline fetches
LiteLLM data, filters and merges it, applies custom models and managed entries,
regenerates aliases from those final source entries, and then writes a stable,
sorted JSON document and its SHA-256.

A final managed-entry stage is added immediately before serialization:

1. Load a repository-owned `price_overrides.json` document.
2. Validate that the root is a JSON object, every key is a non-empty model name,
   and every value is a JSON object. Each entry must contain at least one of
   `input_cost_per_token`, `output_cost_per_token`,
   `cache_read_input_token_cost`, or `cache_creation_input_token_cost` as a
   finite, non-negative number; Luna and Terra are additionally tested against
   their complete expected price matrices.
3. Replace each matching synchronized entry wholesale with the managed entry.
4. Generate aliases after replacement so `codex-auto-review`, whose source is
   `gpt-5.6-luna`, receives the final site-owned Luna entry rather than the
   reduced upstream entry.
5. Serialize the complete catalog deterministically.
6. Hash the exact serialized bytes and write the `.sha256` file.

Whole-entry replacement is intentional. A field-level merge could leave
upstream `*_above_272k_tokens`, priority, flex, or future price fields beside the
site-owned fields. That would publish a contradictory price card whose result
depends on consumer-specific precedence. Whole-entry replacement makes the
catalog itself unambiguous and causes future upstream price dimensions to remain
excluded until explicitly reviewed.

This also freezes non-price metadata inside the two managed entries. That is an
accepted trade-off for the initial implementation. Updates to capabilities,
endpoints, and token limits for managed models must be reviewed together with
their price cards. Expanding to price-domain-only replacement is future work and
requires a separately reviewed definition of all price-bearing fields.

## Managed Catalog Data

All numeric values below are per token. The generated public catalog represents
per-MTok prices by multiplying them by one million.

### `gpt-5.6-luna`

| Dimension | Standard | Priority | Flex | Batch |
| --- | ---: | ---: | ---: | ---: |
| Input | `1e-6` | `2e-6` | `5e-7` | `5e-7` |
| Output | `6e-6` | `1.2e-5` | `3e-6` | `3e-6` |
| Cache creation | `1.25e-6` | `2.5e-6` | `6.25e-7` | `6.25e-7` |
| Cache read | `1e-7` | `2e-7` | `5e-8` | inherited from the supported batch semantics |

### `gpt-5.6-terra`

| Dimension | Standard | Priority | Flex | Batch |
| --- | ---: | ---: | ---: | ---: |
| Input | `2.5e-6` | `5e-6` | `1.25e-6` | `1.25e-6` |
| Output | `1.5e-5` | `3e-5` | `7.5e-6` | `7.5e-6` |
| Cache creation | `3.125e-6` | `6.25e-6` | `1.5625e-6` | `1.5625e-6` |
| Cache read | `2.5e-7` | `5e-7` | `1.25e-7` | inherited from the supported batch semantics |

Both entries use:

```json
{
  "long_context_input_token_threshold": 272000,
  "long_context_input_cost_multiplier": 2.0,
  "long_context_output_cost_multiplier": 1.5
}
```

The threshold comparison is strict: a total context of exactly 272,000 tokens
uses the base rate, while a context above 272,000 applies the long-context tier.
Input, cache-read, and cache-creation prices use the input multiplier; output
uses the output multiplier.

The complete managed entries, not merely the fields tabulated above, are stored
in `price_overrides.json`. Their initial metadata is copied from the current
site-owned embedded catalog and reviewed for supported endpoints, modalities,
token limits, service tiers, and reasoning capabilities.

## `model-price-repo` Components

### Managed entry document

`price_overrides.json` is the source of truth for models whose complete price
cards are site-owned. Adding a model to this file opts that model out of
automatic per-entry updates. Removing a model returns it to the ordinary sync
result on the next rebuild. A managed key must not also appear in
`config.json.custom_models`; Luna and Terra are removed from that section so the
repository contains only one editable source of truth for each managed entry.

### Synchronization logic

`scripts/sync_prices.py` exposes a focused helper that accepts synchronized data
and a managed-entry document, validates it, and returns the final catalog. The
helper replaces entries without mutating unrelated models. Invalid managed data
is a hard error, because silently publishing upstream prices is unsafe for
billing.

The command retains its existing machine-readable output:

```text
CHANGED=true|false
HASH=<sha256>
```

### Documentation

The fork README documents:

- that most entries track LiteLLM automatically;
- that entries in `price_overrides.json` are maintained locally;
- the whole-entry replacement precedence;
- the rebuild and validation commands;
- the review required when adding or updating a managed model.

### CI behavior

The existing scheduled and manual workflow remains enabled. Tests run before a
generated catalog is committed. A failed validation or test prevents the
workflow from publishing a catalog or hash. Alias generation is deliberately
the last catalog-transformation step: aliases always copy the final custom or
managed source entry.

## `sub2api` Integration

The default URLs in `backend/internal/config/config.go` become:

```text
https://raw.githubusercontent.com/specialpointcentral/model-price-repo/main/model_prices_and_context_window.json
https://raw.githubusercontent.com/specialpointcentral/model-price-repo/main/model_prices_and_context_window.sha256
```

The same canonical URLs are shown in `deploy/config.example.yaml`; the existing
`refs/heads/main//` form with a duplicate slash is removed. Operators who
explicitly configure different URLs retain their configuration unchanged.

No new runtime precedence layer is introduced. `sub2api` continues to download,
hash-check, cache, parse, and hot-reload the catalog using its existing pricing
service.

The pricing lookup order changes only for recognized GPT-5.6 family variants.
`gpt-5.6-sol-*`, `gpt-5.6-terra-*`, and `gpt-5.6-luna-*` first contribute their
canonical family model as a deterministic lookup candidate. This candidate is
checked before `generateOpenAIModelVariants` can reduce the request to the bare
`gpt-5.6` entry. Unknown GPT-5.6 names retain the existing fallback behavior.

## Data Flow

```text
LiteLLM catalog
    -> filter and ordinary sync merge
    -> existing cache auto-fill and custom models
    -> whole-entry managed replacements
    -> aliases copied from final source entries
    -> deterministic JSON
    -> SHA-256 of exact JSON bytes
    -> specialpointcentral/model-price-repo raw files
    -> sub2api remote download and hash verification
    -> existing fallback/override parsing pipeline
    -> BillingService and ModelPricingResolver
```

## Error Handling and Safety

- Missing `price_overrides.json` is an error in the maintained fork build. This
  prevents an accidental rename or checkout omission from reverting managed
  models to upstream prices.
- Malformed JSON, a non-object root, an empty model key, a non-object entry, or
  an entry without a finite, non-negative recognized base token-price field
  fails the build.
- Failed downloads leave the existing generated output untouched.
- JSON and hash files are produced from the same in-memory serialized bytes.
- A second rebuild against unchanged inputs must report no catalog change.
- The generated catalog is inspected after publication through its raw GitHub
  URLs before `sub2api` defaults are considered usable.
- `sub2api` keeps its embedded catalog and static fallbacks for remote failures,
  but those layers are not treated as proof that the live remote price is right.

## Testing Strategy

Implementation follows test-driven development.

### Pricing fork tests

Python standard-library tests cover:

- managed entries replace synchronized entries exactly;
- unrelated entries remain unchanged;
- managed model keys do not also appear in `config.json.custom_models`;
- `codex-auto-review` exactly matches the final managed `gpt-5.6-luna` entry;
- upstream-only `*_above_272k_tokens` fields disappear from replaced entries;
- missing, malformed, and structurally invalid managed data fails closed;
- deterministic serialization and exact SHA-256 generation;
- an upstream fixture containing the reduced Luna/Terra prices produces the
  site-owned prices;
- two identical rebuilds are idempotent and the second reports `CHANGED=false`.

### `sub2api` tests

Go tests cover:

- both default URLs point to the maintained fork;
- the fork-format catalog parses to the expected Luna/Terra standard, priority,
  flex, batch, cache-read, and cache-creation prices;
- 272,000 tokens remain at base price and a request above the threshold applies
  the input/cache `2.0` and output `1.5` multipliers;
- representative Sol/Terra/Luna effort and dated suffixes resolve to the correct
  family even when a differently priced bare `gpt-5.6` entry exists;
- an unknown GPT-5.6 family name does not get reclassified as Luna or Terra.

Targeted tests run first. The final `sub2api` verification includes the current
backend build, unit suite, and lint configuration derived from repository CI.
Integration tests are run if the final code touches runtime behavior beyond
configuration defaults and existing catalog parsing.

## Repository and Publication Boundaries

The pricing fork and `sub2api` changes use separate commits and separate review
points:

1. Test, commit, and push the pricing fork so the raw JSON and hash URLs exist.
2. Verify the fork workflow and published artifacts.
3. Implement and commit the `sub2api` default-source change locally.
4. Verify the local `sub2api` commit and report its exact hash and test evidence.
5. Do not push `sub2api/site`, create a tag, or publish a release without a later
   explicit instruction.

No existing unrelated worktree changes are overwritten or included in either
commit.
