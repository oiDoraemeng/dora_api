package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// API-key relays can require the Claude Code envelope even for converted OpenAI
// requests. This opt-in is account scoped; credentials and user instructions stay
// intact, and official OAuth forwarding retains its separate normalization path.
func (a *Account) IsClaudeCodeCompatibilityEnabled() bool {
	return a != nil && a.Platform == PlatformAnthropic && a.Type == AccountTypeAPIKey &&
		a.Credentials["claude_code_compatibility"] == true
}

func prepareClaudeCodeAPIKeyCompatibilityBody(c *gin.Context, account *Account, body []byte) []byte {
	if !account.IsClaudeCodeCompatibilityEnabled() || !gjson.ValidBytes(body) {
		return body
	}
	system := gjson.GetBytes(body, "system")
	var systemValue any
	_ = json.Unmarshal([]byte(system.Raw), &systemValue)
	if !systemIncludesClaudeCodePrompt(systemValue) {
		banner, _ := json.Marshal(map[string]string{"type": "text", "text": claudeCodeSystemPrompt})
		blocks := string(banner)
		if system.IsArray() && len(system.Array()) > 0 {
			blocks += "," + strings.TrimSpace(system.Raw)[1:len(strings.TrimSpace(system.Raw))-1]
		} else if system.Type == gjson.String && system.String() != "" {
			block, _ := json.Marshal(map[string]string{"type": "text", "text": system.String()})
			blocks += "," + string(block)
		}
		if next, err := sjson.SetRawBytes(body, "system", []byte("["+blocks+"]")); err == nil {
			body = next
		}
	}
	if ParseMetadataUserID(gjson.GetBytes(body, "metadata.user_id").String()) == nil {
		// Derive identifiers without forwarding downstream credentials. Keep a
		// session stable across appended turns, and separate different API users.
		client := gjson.GetBytes(body, "metadata.user_id").String()
		if c != nil && c.Request != nil {
			client += "\x00" + c.GetHeader("x-api-key") + "\x00" + c.GetHeader("Authorization")
		}
		deviceHash := sha256.Sum256([]byte(strconv.FormatInt(account.ID, 10) + "\x00" + client))
		device := hex.EncodeToString(deviceHash[:])
		session := generateSessionUUID(buildStableSessionSeed(account.ID, device, extractFirstUserText(body)))
		uid := FormatMetadataUserID(device, "", session, ExtractCLIVersion(claude.DefaultUserAgent()))
		if next, err := sjson.SetBytes(body, "metadata.user_id", uid); err == nil {
			body = next
		}
	}
	return body
}

func claudeCodeAPIKeyCompatibilityBeta(account *Account, beta string) string {
	if !account.IsClaudeCodeCompatibilityEnabled() || anthropicBetaTokensContains(beta, claude.BetaClaudeCode) {
		return beta
	}
	if strings.TrimSpace(beta) == "" {
		return claude.BetaClaudeCode
	}
	return beta + "," + claude.BetaClaudeCode
}

func applyClaudeCodeAPIKeyCompatibilityHeaders(req *http.Request, account *Account, body []byte) {
	if !account.IsClaudeCodeCompatibilityEnabled() || req == nil {
		return
	}
	applyClaudeCodeMimicHeaders(req, gjson.GetBytes(body, "stream").Bool(), claude.DefaultUserAgent())
	setHeaderRaw(req.Header, "anthropic-beta", claudeCodeAPIKeyCompatibilityBeta(account, getHeaderRaw(req.Header, "anthropic-beta")))
	if uid := ParseMetadataUserID(gjson.GetBytes(body, "metadata.user_id").String()); uid != nil {
		setHeaderRaw(req.Header, "X-Claude-Code-Session-Id", uid.SessionID)
	}
}
