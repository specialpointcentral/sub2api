package service

import (
	"encoding/json"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const billingHeaderPrefix = "x-anthropic-billing-header:"

func stripBillingHeaderFromParsed(account *Account, parsed *ParsedRequest) {
	if account == nil || parsed == nil || !account.ShouldStripBillingHeader() || len(parsed.Body) == 0 {
		return
	}
	stripped, changed := stripBillingHeaderSystemText(parsed.Body)
	if !changed {
		return
	}
	next, err := ParseGatewayRequest(stripped, domainProtocolForAccount(account))
	if err != nil {
		return
	}
	next.GroupID = parsed.GroupID
	next.SessionContext = parsed.SessionContext
	next.OnUpstreamAccepted = parsed.OnUpstreamAccepted
	*parsed = *next
}

func domainProtocolForAccount(account *Account) string {
	if account == nil {
		return ""
	}
	return account.Platform
}

func stripBillingHeaderSystemText(body []byte) ([]byte, bool) {
	system := gjson.GetBytes(body, "system")
	if !system.Exists() {
		return body, false
	}

	switch system.Type {
	case gjson.String:
		cleaned, changed := stripBillingHeaderLines(system.String())
		if !changed {
			return body, false
		}
		if strings.TrimSpace(cleaned) == "" {
			next, err := sjson.DeleteBytes(body, "system")
			return updatedOrOriginal(body, next, err), err == nil
		}
		next, err := sjson.SetBytes(body, "system", cleaned)
		return updatedOrOriginal(body, next, err), err == nil
	default:
		if !system.IsArray() {
			return body, false
		}
		var blocks []map[string]any
		if err := json.Unmarshal(sliceRawFromBody(body, system), &blocks); err != nil {
			return body, false
		}
		filtered := blocks[:0]
		changed := false
		for _, block := range blocks {
			text, ok := block["text"].(string)
			if ok {
				if cleaned, lineChanged := stripBillingHeaderLines(text); lineChanged {
					changed = true
					if strings.TrimSpace(cleaned) == "" {
						continue
					}
					block = cloneStringAnyMap(block)
					block["text"] = cleaned
				}
			}
			filtered = append(filtered, block)
		}
		if !changed {
			return body, false
		}
		if len(filtered) == 0 {
			next, err := sjson.DeleteBytes(body, "system")
			return updatedOrOriginal(body, next, err), err == nil
		}
		raw, err := json.Marshal(filtered)
		if err != nil {
			return body, false
		}
		next, err := sjson.SetRawBytes(body, "system", raw)
		return updatedOrOriginal(body, next, err), err == nil
	}
}

func stripBillingHeaderLines(text string) (string, bool) {
	if !strings.Contains(text, billingHeaderPrefix) {
		return text, false
	}
	lines := strings.Split(text, "\n")
	out := lines[:0]
	changed := false
	for _, line := range lines {
		if isBillingHeaderText(line) {
			changed = true
			continue
		}
		out = append(out, line)
	}
	if !changed {
		return text, false
	}
	return strings.TrimLeft(strings.Join(out, "\n"), "\n"), true
}

func isBillingHeaderText(text string) bool {
	return strings.HasPrefix(strings.TrimSpace(text), billingHeaderPrefix)
}

func cloneStringAnyMap(src map[string]any) map[string]any {
	dst := make(map[string]any, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func updatedOrOriginal(original, next []byte, err error) []byte {
	if err != nil {
		return original
	}
	return next
}
