CREATE TABLE IF NOT EXISTS model_rate_limit_rules (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NULL REFERENCES users(id) ON DELETE CASCADE,
    model_pattern VARCHAR(255) NOT NULL,
    normalized_pattern VARCHAR(255) NOT NULL,
    concurrency_limit INTEGER NOT NULL DEFAULT 0,
    rpm_limit INTEGER NOT NULL DEFAULT 0,
    tpm_limit INTEGER NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT model_rate_limit_rules_pattern_not_empty CHECK (length(btrim(model_pattern)) > 0),
    CONSTRAINT model_rate_limit_rules_concurrency_nonnegative CHECK (concurrency_limit >= 0),
    CONSTRAINT model_rate_limit_rules_rpm_nonnegative CHECK (rpm_limit >= 0),
    CONSTRAINT model_rate_limit_rules_tpm_nonnegative CHECK (tpm_limit IS NULL OR tpm_limit >= 0)
);

CREATE UNIQUE INDEX IF NOT EXISTS model_rate_limit_rules_global_pattern_key
    ON model_rate_limit_rules (normalized_pattern)
    WHERE user_id IS NULL;

CREATE UNIQUE INDEX IF NOT EXISTS model_rate_limit_rules_user_pattern_key
    ON model_rate_limit_rules (user_id, normalized_pattern)
    WHERE user_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS model_rate_limit_rules_user_id_idx
    ON model_rate_limit_rules (user_id);
