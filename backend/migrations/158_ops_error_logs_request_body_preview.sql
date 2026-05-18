-- Reintroduce request body observability for ops error details without
-- restoring retry/replay storage.

ALTER TABLE ops_error_logs
  ADD COLUMN IF NOT EXISTS request_body_preview TEXT,
  ADD COLUMN IF NOT EXISTS request_body_preview_truncated BOOLEAN NOT NULL DEFAULT false,
  ADD COLUMN IF NOT EXISTS request_body_preview_bytes INT;

COMMENT ON COLUMN ops_error_logs.request_body_preview IS 'Sanitized, capped request body preview for ops debugging; not used for retry/replay.';
COMMENT ON COLUMN ops_error_logs.request_body_preview_truncated IS 'Whether request_body_preview was trimmed before storage.';
COMMENT ON COLUMN ops_error_logs.request_body_preview_bytes IS 'Original request body byte length before sanitization/truncation.';
