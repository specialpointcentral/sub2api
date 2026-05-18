package repository

import (
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func TestOpsErrorLogInsertDoesNotPersistRequestReplayFields(t *testing.T) {
	disallowedColumns := []string{
		"request_body",
		"request_headers",
		"request_body_truncated",
		"request_body_bytes",
		"is_retryable",
		"retry_count",
		"resolved_retry_id",
	}

	insertSQL := strings.ToLower(insertOpsErrorLogSQL)
	for _, column := range disallowedColumns {
		if regexp.MustCompile(`\b` + regexp.QuoteMeta(column) + `\b`).MatchString(insertSQL) {
			t.Fatalf("ops error log insert still references dropped replay column %q", column)
		}
	}

	inputType := reflect.TypeOf(service.OpsInsertErrorLogInput{})
	disallowedFields := []string{
		"RequestBodyJSON",
		"RequestBodyTruncated",
		"RequestBodyBytes",
		"RequestHeadersJSON",
		"IsRetryable",
		"RetryCount",
		"ResolvedRetryID",
	}
	for _, field := range disallowedFields {
		if _, ok := inputType.FieldByName(field); ok {
			t.Fatalf("OpsInsertErrorLogInput still carries replay field %q", field)
		}
	}
}

func TestOpsErrorLogInsertPersistsRequestBodyPreviewFields(t *testing.T) {
	insertSQL := strings.ToLower(insertOpsErrorLogSQL)
	requiredColumns := []string{
		"request_body_preview",
		"request_body_preview_truncated",
		"request_body_preview_bytes",
	}
	for _, column := range requiredColumns {
		if !regexp.MustCompile(`\b` + regexp.QuoteMeta(column) + `\b`).MatchString(insertSQL) {
			t.Fatalf("ops error log insert should include debug preview column %q", column)
		}
	}

	inputType := reflect.TypeOf(service.OpsInsertErrorLogInput{})
	requiredFields := []string{
		"RequestBodyPreview",
		"RequestBodyPreviewTruncated",
		"RequestBodyPreviewBytes",
	}
	for _, field := range requiredFields {
		if _, ok := inputType.FieldByName(field); !ok {
			t.Fatalf("OpsInsertErrorLogInput should carry debug preview field %q", field)
		}
	}
}
