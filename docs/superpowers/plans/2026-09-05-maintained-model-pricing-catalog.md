# Maintained Model Pricing Catalog Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Publish a site-owned, automatically synchronized pricing catalog that pins complete Luna/Terra price cards, then make it the `sub2api` default and prevent GPT-5.6 family suffixes from falling through to the bare `gpt-5.6` price.

**Architecture:** The `specialpointcentral/model-price-repo` fork continues importing ordinary LiteLLM entries, then validates and wholesale-replaces locally managed entries before regenerating aliases, JSON, and SHA-256. `sub2api` points its default remote and hash URLs at that fork and adds one narrowly scoped GPT-5.6 pricing-family normalization step before the generic numeric-model fallback.

**Tech Stack:** Python 3.12 standard library (`unittest`, `unittest.mock`, JSON, SHA-256), GitHub Actions, Go 1.27.0, Viper, Testify, golangci-lint v2.13.

## Global Constraints

- Use `specialpointcentral/model-price-repo`; do not create another fork.
- Keep the fork's ten-minute LiteLLM synchronization enabled for unmanaged models.
- Initially manage only complete `gpt-5.6-luna` and `gpt-5.6-terra` entries; `gpt-5.6-sol` remains synchronized because its price already agrees.
- Managed entries replace synchronized entries wholesale and aliases are regenerated afterward.
- Invalid or missing managed-price data fails the catalog build; it must not silently publish upstream prices.
- Generate `model_prices_and_context_window.json` and `model_prices_and_context_window.sha256` from the same deterministic JSON bytes.
- The Luna base input/output prices are `$1/$6` per MTok; Terra is `$2.5/$15` per MTok.
- Both managed entries use a strict `272000` long-context threshold, input/cache multiplier `2.0`, and output multiplier `1.5`.
- Preserve user configuration that explicitly overrides `pricing.remote_url` or `pricing.hash_url`.
- Do not use deployment-local `pricing.override_file` as the primary fix.
- Do not include unrelated worktree state in any commit.
- Push the pricing fork because its raw artifacts are an implementation dependency. Keep the final `sub2api` commits local: do not push `fork/site`, create a tag, or publish a release.
- Derive validation tools from current CI: Go `1.27.0` and golangci-lint `v2.13`.

---

### Task 1: Build and publish deterministic managed entries in the pricing fork

**Files:**

- Create: `/tmp/model-price-repo-maintained/tests/test_sync_prices.py`
- Create: `/tmp/model-price-repo-maintained/price_overrides.json`
- Modify: `/tmp/model-price-repo-maintained/scripts/sync_prices.py:9-18,183-213,303-377`
- Modify: `/tmp/model-price-repo-maintained/config.json:260-355`
- Modify: `/tmp/model-price-repo-maintained/.github/workflows/sync-model-pricing.yml:20-38`
- Modify: `/tmp/model-price-repo-maintained/README.md:5-68`
- Regenerate: `/tmp/model-price-repo-maintained/model_prices_and_context_window.json`
- Regenerate: `/tmp/model-price-repo-maintained/model_prices_and_context_window.sha256`

**Interfaces:**

- Consumes: the existing `filter_upstream`, `merge_models`, `fill_cache_1hr_pricing`, `apply_custom_models`, `apply_aliases`, and `write_output` functions.
- Produces: `load_price_overrides(path: str) -> dict`, `apply_price_overrides(data: dict, overrides: dict) -> dict`, the final raw JSON catalog, and its 64-character lowercase SHA-256 file.
- Guarantees: `codex-auto-review` is copied from the final managed `gpt-5.6-luna` entry, not from the reduced upstream entry.

- [ ] **Step 1: Create a clean isolated pricing-fork worktree without touching the unfinished Kimi files**

Run:

```bash
git -C /tmp/model-price-repo fetch origin main
git -C /tmp/model-price-repo worktree add -b codex/maintained-pricing /tmp/model-price-repo-maintained origin/main
git -C /tmp/model-price-repo-maintained status --short --branch
```

Expected: `/tmp/model-price-repo-maintained` is clean at `origin/main`; the uncommitted `scripts/sync_prices.py` and `price_overrides.json` in `/tmp/model-price-repo` remain untouched.

- [ ] **Step 2: Write failing unit tests for validation, whole-entry replacement, and alias ordering**

Create `tests/test_sync_prices.py` with the following core tests. The module import uses the repository's existing script directly and adds no package dependency:

```python
import contextlib
import hashlib
import io
import json
from pathlib import Path
import sys
import tempfile
import unittest
from unittest import mock

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / "scripts"))

import sync_prices  # noqa: E402


MANAGED_LUNA = {
    "input_cost_per_token": 1e-6,
    "output_cost_per_token": 6e-6,
    "long_context_input_token_threshold": 272000,
    "long_context_input_cost_multiplier": 2.0,
    "long_context_output_cost_multiplier": 1.5,
}


class PriceOverridesTest(unittest.TestCase):
    def write_json(self, root: Path, name: str, value) -> Path:
        path = root / name
        path.write_text(json.dumps(value) + "\n", encoding="utf-8")
        return path

    def test_load_price_overrides_rejects_missing_file(self):
        with tempfile.TemporaryDirectory() as tmp:
            with self.assertRaises(FileNotFoundError):
                sync_prices.load_price_overrides(str(Path(tmp) / "missing.json"))

    def test_load_price_overrides_rejects_malformed_json(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "malformed.json"
            path.write_text("{not-json", encoding="utf-8")
            with self.assertRaises(json.JSONDecodeError):
                sync_prices.load_price_overrides(str(path))

    def test_load_price_overrides_rejects_invalid_structures(self):
        invalid_values = [
            [],
            {"": MANAGED_LUNA},
            {"gpt-5.6-luna": "not-an-object"},
            {"gpt-5.6-luna": {"mode": "chat"}},
            {"gpt-5.6-luna": {"input_cost_per_token": True}},
            {"gpt-5.6-luna": {"input_cost_per_token": -1}},
            {"gpt-5.6-luna": {"input_cost_per_token": float("inf")}},
        ]
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            for index, value in enumerate(invalid_values):
                with self.subTest(value=value):
                    path = self.write_json(root, f"invalid-{index}.json", value)
                    with self.assertRaises(ValueError):
                        sync_prices.load_price_overrides(str(path))

    def test_apply_price_overrides_replaces_entry_without_mutating_input(self):
        original = {
            "gpt-5.6-luna": {
                "input_cost_per_token": 2e-7,
                "input_cost_per_token_above_272k_tokens": 4e-7,
            },
            "gpt-5.6-sol": {"input_cost_per_token": 5e-6},
        }
        result = sync_prices.apply_price_overrides(
            original, {"gpt-5.6-luna": MANAGED_LUNA}
        )

        self.assertEqual(MANAGED_LUNA, result["gpt-5.6-luna"])
        self.assertNotIn(
            "input_cost_per_token_above_272k_tokens",
            result["gpt-5.6-luna"],
        )
        self.assertEqual(
            {"input_cost_per_token": 5e-6}, result["gpt-5.6-sol"]
        )
        self.assertEqual(2e-7, original["gpt-5.6-luna"]["input_cost_per_token"])

    def test_main_applies_managed_entry_before_aliases(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            config = {
                "upstream_url": "https://example.invalid/prices.json",
                "output_file": "catalog.json",
                "hash_file": "catalog.sha256",
                "sync_mode": "full",
                "update_existing": True,
                "prefix_filters": ["gpt-"],
                "aliases": {
                    "codex-auto-review": {
                        "source": "gpt-5.6-luna",
                        "description": "managed Luna alias",
                    }
                },
            }
            config_path = self.write_json(root, "config.json", config)
            self.write_json(
                root, "price_overrides.json", {"gpt-5.6-luna": MANAGED_LUNA}
            )
            upstream = {
                "gpt-5.6-luna": {
                    "input_cost_per_token": 2e-7,
                    "output_cost_per_token": 1.2e-6,
                    "input_cost_per_token_above_272k_tokens": 4e-7,
                }
            }

            argv = [
                "sync_prices.py",
                "--config",
                config_path.name,
                "--repo-root",
                str(root),
            ]
            with mock.patch.object(sync_prices, "fetch_upstream", return_value=upstream):
                with mock.patch.object(sys, "argv", argv):
                    with contextlib.redirect_stdout(io.StringIO()):
                        sync_prices.main()

            catalog = json.loads((root / "catalog.json").read_text(encoding="utf-8"))
            self.assertEqual(MANAGED_LUNA, catalog["gpt-5.6-luna"])
            self.assertEqual(MANAGED_LUNA, catalog["codex-auto-review"])


if __name__ == "__main__":
    unittest.main()
```

- [ ] **Step 3: Run the tests and verify the new interface is absent**

Run:

```bash
cd /tmp/model-price-repo-maintained
python3 -m unittest discover -s tests -p 'test_*.py' -v
```

Expected: FAIL with `AttributeError` for `load_price_overrides` or `apply_price_overrides`; this demonstrates the tests are exercising behavior not present in `origin/main`.

- [ ] **Step 4: Implement strict managed-entry loading and pure replacement**

In `scripts/sync_prices.py`, add `import math` and implement:

```python
BASE_TOKEN_PRICE_FIELDS = (
    "input_cost_per_token",
    "output_cost_per_token",
    "cache_read_input_token_cost",
    "cache_creation_input_token_cost",
)


def _is_valid_base_token_price(value) -> bool:
    return (
        isinstance(value, (int, float))
        and not isinstance(value, bool)
        and math.isfinite(value)
        and value >= 0
    )


def load_price_overrides(path: str) -> dict:
    """Load and validate the required site-managed model entries."""
    if not os.path.isfile(path):
        raise FileNotFoundError(f"Price override file not found: {path}")
    with open(path, "r", encoding="utf-8") as f:
        overrides = json.load(f)
    if not isinstance(overrides, dict):
        raise ValueError("Price overrides root must be a JSON object")

    validated = {}
    for model, entry in overrides.items():
        if not isinstance(model, str) or not model.strip():
            raise ValueError("Price override model names must be non-empty strings")
        if not isinstance(entry, dict):
            raise ValueError(f"Price override for {model!r} must be a JSON object")
        if not any(
            _is_valid_base_token_price(entry.get(field))
            for field in BASE_TOKEN_PRICE_FIELDS
        ):
            raise ValueError(
                f"Price override for {model!r} has no valid base token price"
            )
        validated[model.strip()] = entry
    return validated


def apply_price_overrides(data: dict, overrides: dict) -> dict:
    """Return a catalog with managed entries replaced wholesale."""
    merged = dict(data)
    for model, entry in overrides.items():
        merged[model] = copy.deepcopy(entry)
        log.info("Price override '%s' pinned.", model)
    return merged
```

Update `main()` so the transformation order is exactly:

```python
# 6. Auto-fill cache 1hr pricing
cache_1hr_count = fill_cache_1hr_pricing(merged, config)

# 7. Custom models
custom = config.get("custom_models", {})
if custom:
    merged = apply_custom_models(merged, custom)

# 8. Site-managed entries
overrides = load_price_overrides(
    os.path.join(repo_root, "price_overrides.json")
)
merged = apply_price_overrides(merged, overrides)

# 9. Aliases must copy the final source entries
aliases = config.get("aliases", {})
if aliases:
    merged = apply_aliases(merged, aliases)

# 10. Write output
changed, new_hash = write_output(merged, output_path, hash_path, old_hash)
```

Keep the report counters aligned with those names and log `len(overrides)` as `Overrides`. Remove the old pre-custom-model alias block so aliases are applied exactly once.

- [ ] **Step 5: Run the focused unit tests and verify green**

Run:

```bash
cd /tmp/model-price-repo-maintained
python3 -m unittest discover -s tests -p 'test_*.py' -v
```

Expected: all five tests PASS.

- [ ] **Step 6: Add a failing repository-artifact contract test**

Extend `PriceOverridesTest` with:

```python
    def test_repository_artifacts_match_managed_price_cards(self):
        overrides = sync_prices.load_price_overrides(
            str(ROOT / "price_overrides.json")
        )
        catalog_bytes = (ROOT / "model_prices_and_context_window.json").read_bytes()
        catalog = json.loads(catalog_bytes)
        stored_hash = (
            ROOT / "model_prices_and_context_window.sha256"
        ).read_text(encoding="utf-8").strip()

        config = json.loads((ROOT / "config.json").read_text(encoding="utf-8"))
        expected = {
            "gpt-5.6-luna": {
                "input_cost_per_token": 1e-6,
                "input_cost_per_token_batches": 5e-7,
                "input_cost_per_token_flex": 5e-7,
                "input_cost_per_token_priority": 2e-6,
                "output_cost_per_token": 6e-6,
                "output_cost_per_token_batches": 3e-6,
                "output_cost_per_token_flex": 3e-6,
                "output_cost_per_token_priority": 1.2e-5,
                "cache_creation_input_token_cost": 1.25e-6,
                "cache_creation_input_token_cost_batches": 6.25e-7,
                "cache_creation_input_token_cost_flex": 6.25e-7,
                "cache_creation_input_token_cost_priority": 2.5e-6,
                "cache_read_input_token_cost": 1e-7,
                "cache_read_input_token_cost_flex": 5e-8,
                "cache_read_input_token_cost_priority": 2e-7,
            },
            "gpt-5.6-terra": {
                "input_cost_per_token": 2.5e-6,
                "input_cost_per_token_batches": 1.25e-6,
                "input_cost_per_token_flex": 1.25e-6,
                "input_cost_per_token_priority": 5e-6,
                "output_cost_per_token": 1.5e-5,
                "output_cost_per_token_batches": 7.5e-6,
                "output_cost_per_token_flex": 7.5e-6,
                "output_cost_per_token_priority": 3e-5,
                "cache_creation_input_token_cost": 3.125e-6,
                "cache_creation_input_token_cost_batches": 1.5625e-6,
                "cache_creation_input_token_cost_flex": 1.5625e-6,
                "cache_creation_input_token_cost_priority": 6.25e-6,
                "cache_read_input_token_cost": 2.5e-7,
                "cache_read_input_token_cost_flex": 1.25e-7,
                "cache_read_input_token_cost_priority": 5e-7,
            },
        }
        self.assertEqual(set(expected), set(overrides))
        self.assertTrue(
            set(overrides).isdisjoint(config.get("custom_models", {}))
        )
        for model, prices in expected.items():
            self.assertEqual(overrides[model], catalog[model])
            for field, value in prices.items():
                self.assertEqual(value, catalog[model][field])
            self.assertEqual(
                272000,
                catalog[model]["long_context_input_token_threshold"],
            )
            self.assertEqual(
                2.0, catalog[model]["long_context_input_cost_multiplier"]
            )
            self.assertEqual(
                1.5, catalog[model]["long_context_output_cost_multiplier"]
            )
            self.assertFalse(any("_above_" in key for key in catalog[model]))

        self.assertEqual(overrides["gpt-5.6-luna"], catalog["codex-auto-review"])
        self.assertEqual(hashlib.sha256(catalog_bytes).hexdigest(), stored_hash)
```

- [ ] **Step 7: Run the artifact test and verify it fails for the right reason**

Run:

```bash
cd /tmp/model-price-repo-maintained
python3 -m unittest tests.test_sync_prices.PriceOverridesTest.test_repository_artifacts_match_managed_price_cards -v
```

Expected: FAIL because `price_overrides.json` does not exist yet. If the test module path is not importable without `tests/__init__.py`, run the equivalent discovery command and confirm the same missing-file failure:

```bash
python3 -m unittest discover -s tests -p 'test_*.py' -v
```

- [ ] **Step 8: Generate the two complete managed entries from the reviewed `sub2api` embedded catalog**

Use the current reviewed site catalog as a mechanical data source. This copies the complete entries, including endpoints and capability metadata, while excluding every upstream-only `*_above_*` field because the embedded entries express the reviewed long-context multipliers directly:

```bash
cd /tmp/model-price-repo-maintained
python3 -c 'import json; source=json.load(open("/home/huqi/sub2api/backend/resources/model-pricing/model_prices_and_context_window.json", encoding="utf-8")); managed={key: source[key] for key in ("gpt-5.6-luna", "gpt-5.6-terra")}; open("price_overrides.json", "w", encoding="utf-8").write(json.dumps(managed, sort_keys=True, indent=2) + "\n")'
```

Inspect all managed price dimensions before rebuilding:

```bash
python3 -c 'import json; data=json.load(open("price_overrides.json")); print(json.dumps(data, indent=2, sort_keys=True))'
```

Expected: Luna has `1e-6/6e-6` input/output, Terra has `2.5e-6/1.5e-5`, both have threshold `272000`, multipliers `2.0/1.5`, and neither entry contains a key with `_above_`.

Remove the complete `gpt-5.6-luna` and `gpt-5.6-terra` members from
`config.json.custom_models` using `apply_patch`. Leave `gpt-5.6-sol`,
`codex-auto-review`, and every unrelated custom model unchanged. Validate the
single-source rule:

```bash
python3 -c 'import json; config=json.load(open("config.json")); managed=json.load(open("price_overrides.json")); assert set(managed).isdisjoint(config.get("custom_models", {})); print(sorted(managed))'
```

Expected: `['gpt-5.6-luna', 'gpt-5.6-terra']`.

- [ ] **Step 9: Rebuild the generated catalog and hash**

Run:

```bash
cd /tmp/model-price-repo-maintained
./rebuild.sh
```

Expected: the LiteLLM catalog downloads successfully, the report includes `Overrides: 2`, and both output files are regenerated.

- [ ] **Step 10: Verify all tests and rebuild idempotence**

Run:

```bash
cd /tmp/model-price-repo-maintained
python3 -m unittest discover -s tests -p 'test_*.py' -v
python3 scripts/sync_prices.py --config config.json --repo-root .
```

Expected: all tests PASS; the second sync prints `CHANGED=false`; the stored hash equals the SHA-256 of the exact JSON bytes.

- [ ] **Step 11: Run tests in the GitHub workflow before synchronization**

Insert this step after Python setup and before `Run sync script` in `.github/workflows/sync-model-pricing.yml`:

```yaml
      - name: Test pricing synchronization
        run: python3 -m unittest discover -s tests -p 'test_*.py' -v
```

This guarantees a malformed managed catalog or alias-order regression prevents the bot from committing generated output.

- [ ] **Step 12: Document managed-entry ownership and rebuild procedure**

Update the README's pipeline to state this exact order:

```text
fetch/filter/merge -> cache auto-fill -> custom models -> managed whole-entry replacement -> aliases -> JSON/SHA-256
```

Document that `price_overrides.json` opts complete model entries out of upstream per-entry updates, that aliases copy final entries, and that maintainers must run:

```bash
python3 -m unittest discover -s tests -p 'test_*.py' -v
./rebuild.sh
python3 scripts/sync_prices.py --config config.json --repo-root .
```

The README must identify Luna and Terra as the initial managed models and explain that the final command must report `CHANGED=false`.

- [ ] **Step 13: Commit the complete pricing-fork deliverable**

Run:

```bash
cd /tmp/model-price-repo-maintained
git diff --check
git status --short
git add tests/test_sync_prices.py price_overrides.json scripts/sync_prices.py config.json .github/workflows/sync-model-pricing.yml README.md model_prices_and_context_window.json model_prices_and_context_window.sha256
git commit -m "fix: pin site-managed GPT-5.6 pricing"
```

Expected: one commit contains only the listed pricing-fork files, and the worktree is clean afterward.

### Task 2: Publish and independently verify the pricing fork

**Files:**

- Read-only verification: `/tmp/model-price-repo-maintained/model_prices_and_context_window.json`
- Read-only verification: `/tmp/model-price-repo-maintained/model_prices_and_context_window.sha256`
- Remote target: `specialpointcentral/model-price-repo`, branch `main`

**Interfaces:**

- Consumes: the tested Task 1 commit.
- Produces: publicly readable canonical JSON and SHA-256 URLs used by Task 3.

- [ ] **Step 1: Refresh the remote lease and verify a fast-forward publication**

Run:

```bash
git -C /tmp/model-price-repo-maintained fetch origin main
git -C /tmp/model-price-repo-maintained merge-base --is-ancestor origin/main HEAD
git -C /tmp/model-price-repo-maintained status --short --branch
```

Expected: the ancestry command exits 0 and the worktree is clean. If the scheduled workflow advanced `origin/main`, rebase the single local commit, regenerate artifacts, and rerun tests before pushing:

```bash
git -C /tmp/model-price-repo-maintained rebase origin/main
cd /tmp/model-price-repo-maintained
./rebuild.sh
python3 -m unittest discover -s tests -p 'test_*.py' -v
git add model_prices_and_context_window.json model_prices_and_context_window.sha256
GIT_EDITOR=true git rebase --continue
```

- [ ] **Step 2: Push normally to the fork's `main` branch**

Run:

```bash
git -C /tmp/model-price-repo-maintained push origin HEAD:main
```

Expected: a fast-forward push succeeds. Do not force-push.

- [ ] **Step 3: Wait for the fork workflow and verify it does not erase managed prices**

Run:

```bash
gh run list --repo specialpointcentral/model-price-repo --workflow sync-model-pricing.yml --limit 3
gh run watch --repo specialpointcentral/model-price-repo --exit-status
```

Expected: the relevant workflow finishes successfully. If the scheduled run makes a bot commit, its catalog still passes the managed-price tests.

- [ ] **Step 4: Read back both remote artifacts and verify exact content and hash**

Run:

```bash
curl -fsSL https://raw.githubusercontent.com/specialpointcentral/model-price-repo/main/model_prices_and_context_window.json -o /tmp/site-model-pricing.json
curl -fsSL https://raw.githubusercontent.com/specialpointcentral/model-price-repo/main/model_prices_and_context_window.sha256 -o /tmp/site-model-pricing.sha256
python3 -c 'import hashlib,json; body=open("/tmp/site-model-pricing.json","rb").read(); stored=open("/tmp/site-model-pricing.sha256").read().strip(); data=json.loads(body); assert hashlib.sha256(body).hexdigest()==stored; assert data["gpt-5.6-luna"]["input_cost_per_token"]==1e-6; assert data["gpt-5.6-luna"]["output_cost_per_token"]==6e-6; assert data["gpt-5.6-terra"]["input_cost_per_token"]==2.5e-6; assert data["gpt-5.6-terra"]["output_cost_per_token"]==1.5e-5; assert data["codex-auto-review"]==data["gpt-5.6-luna"]; assert not any("_above_" in key for model in ("gpt-5.6-luna","gpt-5.6-terra") for key in data[model]); print(stored)'
```

Expected: the script prints the verified 64-character hash and exits 0.

### Task 3: Switch `sub2api` defaults to the maintained fork

**Files:**

- Modify: `/tmp/sub2api-maintained-pricing/backend/internal/config/config_test.go`
- Modify: `/tmp/sub2api-maintained-pricing/backend/internal/config/config.go:2286-2288`
- Modify: `/tmp/sub2api-maintained-pricing/deploy/config.example.yaml:1068-1089`

**Interfaces:**

- Consumes: Task 2's published JSON and SHA-256 URLs.
- Produces: default `PricingConfig.RemoteURL` and `PricingConfig.HashURL` values pointing at `specialpointcentral/model-price-repo`; explicit operator configuration remains higher priority through Viper.

- [ ] **Step 1: Create an isolated `sub2api` implementation worktree from the approved-spec commit**

Run:

```bash
git -C /home/huqi/sub2api worktree add -b codex/maintained-pricing /tmp/sub2api-maintained-pricing site
git -C /tmp/sub2api-maintained-pricing status --short --branch
```

Expected: a clean worktree based on the local `site` branch containing the approved design and implementation plan.

- [ ] **Step 2: Write a failing default-source and example-config test**

Add this test to `backend/internal/config/config_test.go`:

```go
func TestLoadDefaultPricingCatalogUsesMaintainedFork(t *testing.T) {
	resetViperWithJWTSecret(t)

	cfg, err := Load()
	require.NoError(t, err)

	const rawBase = "https://raw.githubusercontent.com/specialpointcentral/model-price-repo/main/"
	require.Equal(t, rawBase+"model_prices_and_context_window.json", cfg.Pricing.RemoteURL)
	require.Equal(t, rawBase+"model_prices_and_context_window.sha256", cfg.Pricing.HashURL)

	examplePath := filepath.Join("..", "..", "..", "deploy", "config.example.yaml")
	example, err := os.ReadFile(examplePath)
	require.NoError(t, err)
	require.Contains(t, string(example), rawBase+"model_prices_and_context_window.json")
	require.Contains(t, string(example), rawBase+"model_prices_and_context_window.sha256")
	require.NotContains(t, string(example), "refs/heads/main//")
}
```

The file already imports `os`, `filepath`, and Testify `require`; do not add duplicate imports.

- [ ] **Step 3: Run the test and verify it detects the old source**

Run:

```bash
cd /tmp/sub2api-maintained-pricing/backend
gofmt -w internal/config/config_test.go
go test ./internal/config -run TestLoadDefaultPricingCatalogUsesMaintainedFork -count=1
```

Expected: FAIL because the actual default and example still reference `Wei-Shaw/model-price-repo`.

- [ ] **Step 4: Change both runtime defaults and both documented example URLs**

In `backend/internal/config/config.go`, set:

```go
viper.SetDefault("pricing.remote_url", "https://raw.githubusercontent.com/specialpointcentral/model-price-repo/main/model_prices_and_context_window.json")
viper.SetDefault("pricing.hash_url", "https://raw.githubusercontent.com/specialpointcentral/model-price-repo/main/model_prices_and_context_window.sha256")
```

In `deploy/config.example.yaml`, use the identical canonical URLs:

```yaml
  remote_url: "https://raw.githubusercontent.com/specialpointcentral/model-price-repo/main/model_prices_and_context_window.json"
  hash_url: "https://raw.githubusercontent.com/specialpointcentral/model-price-repo/main/model_prices_and_context_window.sha256"
```

Do not change `pricing.fallback_file`, `pricing.override_file`, update intervals, or URL allowlist behavior.

- [ ] **Step 5: Run the focused config package test and verify green**

Run:

```bash
cd /tmp/sub2api-maintained-pricing/backend
go test ./internal/config -run TestLoadDefaultPricingCatalogUsesMaintainedFork -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit the source switch independently**

Run:

```bash
cd /tmp/sub2api-maintained-pricing
git diff --check
git add backend/internal/config/config.go backend/internal/config/config_test.go deploy/config.example.yaml
git commit -m "fix(pricing): use maintained site pricing catalog"
```

Expected: only the three listed files are committed.

### Task 4: Resolve GPT-5.6 family variants before the bare numeric fallback

**Files:**

- Modify: `/tmp/sub2api-maintained-pricing/backend/internal/service/pricing_service_test.go`
- Modify: `/tmp/sub2api-maintained-pricing/backend/internal/service/pricing_service.go:1260-1340`

**Interfaces:**

- Consumes: `canonicalizeOpenAIModelAliasSpelling(model string) string` and `isKnownCodexModelSuffix(suffix string) bool` from the existing service package.
- Produces: `normalizeGPT56FamilyPricingModel(model string) string`, returning one of `gpt-5.6-sol`, `gpt-5.6-terra`, `gpt-5.6-luna`, or `""`.
- Ordering contract: the canonical family candidate is first in `buildModelLookupCandidates`, before `generateOpenAIModelVariants` can return bare `gpt-5.6`.

- [ ] **Step 1: Write the failing family-vs-bare regression test**

Add this test to `backend/internal/service/pricing_service_test.go`:

```go
func TestPricingServiceGPT56FamilyVariantsBeatBareGPT56(t *testing.T) {
	bare := &LiteLLMModelPricing{InputCostPerToken: 4e-6, OutputCostPerToken: 20e-6}
	sol := &LiteLLMModelPricing{InputCostPerToken: 5e-6, OutputCostPerToken: 30e-6}
	terra := &LiteLLMModelPricing{InputCostPerToken: 2.5e-6, OutputCostPerToken: 15e-6}
	luna := &LiteLLMModelPricing{InputCostPerToken: 1e-6, OutputCostPerToken: 6e-6}
	svc := &PricingService{pricingData: map[string]*LiteLLMModelPricing{
		"gpt-5.6":       bare,
		"gpt-5.6-sol":   sol,
		"gpt-5.6-terra": terra,
		"gpt-5.6-luna":  luna,
	}}

	tests := []struct {
		model string
		want  *LiteLLMModelPricing
	}{
		{model: "gpt-5.6-sol-preview", want: sol},
		{model: "gpt-5.6-terra-high", want: terra},
		{model: "gpt-5.6-luna-2026-08-01", want: luna},
		{model: "openai/gpt-5.6-luna-xhigh", want: luna},
		{model: "gpt-5.6-nebula-high", want: bare},
	}
	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			require.Same(t, tt.want, svc.GetModelPricing(tt.model))
		})
	}
}
```

- [ ] **Step 2: Run the regression test and verify family variants hit the bare entry**

Run:

```bash
cd /tmp/sub2api-maintained-pricing/backend
go test ./internal/service -run TestPricingServiceGPT56FamilyVariantsBeatBareGPT56 -count=1
```

Expected: FAIL for at least `sol-preview`, `terra-high`, or a Luna suffix because `generateOpenAIModelVariants` currently finds `gpt-5.6` before the family-specific fallback.

- [ ] **Step 3: Implement narrow recognized-family normalization**

Add this helper near `normalizeModelNameForPricing` in `pricing_service.go`:

```go
func normalizeGPT56FamilyPricingModel(model string) string {
	for _, family := range []string{
		"gpt-5.6-sol",
		"gpt-5.6-terra",
		"gpt-5.6-luna",
	} {
		if model == family {
			return family
		}
		suffix, ok := strings.CutPrefix(model, family+"-")
		if ok && (suffix == "preview" || isKnownCodexModelSuffix(suffix)) {
			return family
		}
	}
	return ""
}
```

Immediately after `canonicalizeOpenAIModelAliasSpelling` succeeds in `normalizeModelNameForPricing`, put the family check before the existing bare `gpt-5.6` and generic suffix logic:

```go
if canonical := canonicalizeOpenAIModelAliasSpelling(model); canonical != "" {
	if family := normalizeGPT56FamilyPricingModel(canonical); family != "" {
		return family
	}
	if canonical == "gpt-6" {
		return "gpt-6-astra"
	}
	// existing cases continue unchanged
}
```

Do not use `strings.Contains` for family selection; the accepted suffix set remains `preview`, known reasoning effort suffixes, and the existing strict date suffix format.

- [ ] **Step 4: Run focused and neighboring GPT-5.6 tests**

Run:

```bash
cd /tmp/sub2api-maintained-pricing/backend
gofmt -w internal/service/pricing_service.go internal/service/pricing_service_test.go
go test ./internal/service -run 'TestPricingServiceGPT56FamilyVariantsBeatBareGPT56|TestGPT56DedicatedFallbacksUseOfficialRates|TestDefaultPricingIncludesOfficialGPT56Rates|TestBillingService_GPT56UsesLongContextPricingAcrossModelsAndTiers' -count=1
```

Expected: all selected tests PASS, including the unknown-family case that still resolves through the pre-existing bare fallback.

- [ ] **Step 5: Commit the normalization fix independently**

Run:

```bash
cd /tmp/sub2api-maintained-pricing
git diff --check
git add backend/internal/service/pricing_service.go backend/internal/service/pricing_service_test.go
git commit -m "fix(pricing): resolve GPT-5.6 family variants first"
```

Expected: only the two pricing-service files are committed.

### Task 5: Run full verification and fast-forward the local `site` branch

**Files:**

- Verify: all files changed in Tasks 3 and 4
- Update local ref/worktree only: `/home/huqi/sub2api`, branch `site`

**Interfaces:**

- Consumes: the two local `sub2api` commits and the already published pricing catalog.
- Produces: a verified local `site` branch containing the design, plan, default-source switch, and family-normalization fix; no remote `sub2api` mutation.

- [ ] **Step 1: Run formatting and diff integrity checks**

Run:

```bash
cd /tmp/sub2api-maintained-pricing
gofmt -w backend/internal/config/config_test.go backend/internal/service/pricing_service.go backend/internal/service/pricing_service_test.go
git diff --check
git status --short
```

Expected: formatting makes no uncommitted changes. If it does, amend only the commit that owns the affected file after rerunning its focused test; do not create a mixed formatting commit.

- [ ] **Step 2: Run the current CI-equivalent backend build and unit suite**

Run:

```bash
docker run --rm -v /tmp/sub2api-maintained-pricing:/src -w /src/backend golang:1.27 sh -c 'go version | grep -q go1.27.0 && go build ./... && make test-unit'
```

Expected: build and unit tests exit 0.

- [ ] **Step 3: Run current CI-equivalent lint**

Run:

```bash
docker run --rm -v /tmp/sub2api-maintained-pricing:/src -w /src/backend golangci/golangci-lint:v2.13.2 golangci-lint run --timeout=30m
```

Expected: lint exits 0 with no new issues.

- [ ] **Step 4: Run the Testcontainers-backed integration suite**

Run the repository integration suite because Task 4 changes runtime pricing
lookup behavior. Use the current machine's rootless Docker socket and the
repository's compatible PostgreSQL image:

```bash
mkdir -p /tmp/sub2api-pricing-go-cache/mod /tmp/sub2api-pricing-go-cache/build
docker run --rm -v /tmp/sub2api-maintained-pricing:/src -v /tmp/sub2api-pricing-go-cache/mod:/go/pkg/mod -v /tmp/sub2api-pricing-go-cache/build:/root/.cache/go-build -v /usr/bin/docker:/usr/bin/docker -v /run/user/1510000028/docker.sock:/var/run/docker.sock --network host -e DOCKER_HOST=unix:///var/run/docker.sock -e TESTCONTAINERS_RYUK_DISABLED=true -e SUB2API_TEST_POSTGRES_IMAGE=postgres:16-alpine -w /src/backend golang:1.27 sh -c 'make test-integration'
```

Expected: the integration suite runs real Testcontainers-backed tests and exits
0; do not accept a run that silently skipped because the Docker socket was
unreachable.

- [ ] **Step 5: Reverify the live pricing artifacts after backend validation**

Run:

```bash
curl -fsSL https://raw.githubusercontent.com/specialpointcentral/model-price-repo/main/model_prices_and_context_window.json -o /tmp/site-model-pricing-final.json
curl -fsSL https://raw.githubusercontent.com/specialpointcentral/model-price-repo/main/model_prices_and_context_window.sha256 -o /tmp/site-model-pricing-final.sha256
python3 -c 'import hashlib,json; body=open("/tmp/site-model-pricing-final.json","rb").read(); data=json.loads(body); assert hashlib.sha256(body).hexdigest()==open("/tmp/site-model-pricing-final.sha256").read().strip(); assert data["gpt-5.6-luna"]["input_cost_per_token"]==1e-6; assert data["gpt-5.6-terra"]["input_cost_per_token"]==2.5e-6; assert data["codex-auto-review"]==data["gpt-5.6-luna"]'
```

Expected: exit 0; scheduled synchronization has not changed managed entries.

- [ ] **Step 6: Fast-forward only the local `site` branch**

First verify both worktrees are clean and the implementation branch is a descendant of `site`:

```bash
git -C /tmp/sub2api-maintained-pricing status --short
git -C /home/huqi/sub2api status --short
git -C /home/huqi/sub2api merge-base --is-ancestor site codex/maintained-pricing
```

Then fast-forward locally:

```bash
git -C /home/huqi/sub2api merge --ff-only codex/maintained-pricing
```

Expected: local `site` advances without a merge commit. Do not run `git push` and do not create or move a tag.

- [ ] **Step 7: Record final evidence**

Run:

```bash
git -C /home/huqi/sub2api status --short --branch
git -C /home/huqi/sub2api log -5 --oneline --decorate
git -C /tmp/model-price-repo-maintained status --short --branch
git -C /tmp/model-price-repo-maintained ls-remote origin refs/heads/main
```

Expected: `sub2api/site` is ahead of `fork/site` only by the local documentation and implementation commits; the pricing-fork remote `main` matches the tested pricing commit or its verified workflow-generated descendant; all worktrees are clean.
