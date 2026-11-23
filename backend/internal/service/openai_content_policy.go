package service

import (
	"net/http"
	"strings"

	"github.com/tidwall/gjson"
)

// A moderation refusal describes the submitted content, not the credential.
// Inspect error fields only: echoed request data must not mask account failures.
func isOpenAIContentPolicyRejection(statusCode int, body []byte) bool {
	if statusCode != http.StatusForbidden || len(body) == 0 {
		return false
	}
	if isOpenAIHTTPUpstreamAccessStateError(statusCode, "", body) {
		return false
	}
	contentCode := false
	for _, path := range []string{
		"error.code", "error.type", "error.reason", "response.error.code", "response.error.type",
		"detail.code", "detail.type", "code", "type",
	} {
		marker := strings.ToLower(strings.TrimSpace(gjson.GetBytes(body, path).String()))
		if isOpenAIUpstreamAccessStateCode(marker) {
			return false
		}
		switch marker {
		case "invalid_api_key", "authentication_error", "account_suspended", "account_disabled",
			"subscription_required", "entitlement_required", "not_entitled", "plan_required":
			return false
		case "content_policy_violation", "content_policy_rejection", "content_policy",
			"content_filter", "content_filter_error", "content_moderation", "content_moderation_error":
			contentCode = true
		}
	}
	message := strings.TrimSpace(extractUpstreamErrorMessage(body))
	if message == "" && !gjson.ValidBytes(body) {
		message = string(body)
	}
	lower := strings.ToLower(message)
	for _, phrase := range []string{
		"account suspended", "account has been suspended", "account disabled", "account has been disabled",
		"workspace suspended", "workspace has been suspended", "workspace disabled", "workspace has been disabled",
		"subscription required", "entitlement required", "not entitled", "invalid api key",
	} {
		if strings.Contains(lower, phrase) {
			return false
		}
	}
	if contentCode {
		return true
	}
	for _, phrase := range []string{
		"request rejected by the content moderation policy", "request rejected by content moderation",
		"request blocked by content moderation", "content policy violation", "content policy rejection",
		"prompt violates content policy", "input violates content policy",
	} {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return false
}

func isOpenAIAccountContentPolicyRejection(account *Account, statusCode int, body []byte) bool {
	return account != nil && (account.Platform == PlatformOpenAI || account.IsMultiProtocolAPIKey()) &&
		isOpenAIContentPolicyRejection(statusCode, body)
}
