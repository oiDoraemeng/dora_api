package admin

import (
	"encoding/json"
	"errors"
	"html"
	"net/mail"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// TestSMTPRequest 测试SMTP连接请求
type TestSMTPRequest struct {
	SMTPHost     string `json:"smtp_host"`
	SMTPPort     int    `json:"smtp_port"`
	SMTPUsername string `json:"smtp_username"`
	SMTPPassword string `json:"smtp_password"`
	SMTPUseTLS   *bool  `json:"smtp_use_tls"`
}

func resolveSMTPUseTLS(requested *bool, savedConfig *service.SMTPConfig) bool {
	if requested != nil {
		return *requested
	}
	return savedConfig != nil && savedConfig.UseTLS
}

// TestSMTPConnection 测试SMTP连接
// POST /api/v1/admin/settings/test-smtp
func (h *SettingHandler) TestSMTPConnection(c *gin.Context) {
	var req TestSMTPRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	req.SMTPHost = strings.TrimSpace(req.SMTPHost)
	req.SMTPUsername = strings.TrimSpace(req.SMTPUsername)

	var savedConfig *service.SMTPConfig
	if cfg, err := h.emailService.GetSMTPConfig(c.Request.Context()); err == nil && cfg != nil {
		savedConfig = cfg
	}

	if req.SMTPHost == "" && savedConfig != nil {
		req.SMTPHost = savedConfig.Host
	}
	if req.SMTPPort <= 0 {
		if savedConfig != nil && savedConfig.Port > 0 {
			req.SMTPPort = savedConfig.Port
		} else {
			req.SMTPPort = 587
		}
	}
	if req.SMTPUsername == "" && savedConfig != nil {
		req.SMTPUsername = savedConfig.Username
	}
	password := strings.TrimSpace(req.SMTPPassword)
	if password == "" && savedConfig != nil {
		password = savedConfig.Password
	}
	if req.SMTPHost == "" {
		response.BadRequest(c, "SMTP host is required")
		return
	}

	config := &service.SMTPConfig{
		Host:     req.SMTPHost,
		Port:     req.SMTPPort,
		Username: req.SMTPUsername,
		Password: password,
		UseTLS:   resolveSMTPUseTLS(req.SMTPUseTLS, savedConfig),
	}

	err := h.emailService.TestSMTPConnectionWithConfig(config)
	if err != nil {
		response.BadRequest(c, "SMTP connection test failed: "+err.Error())
		return
	}

	response.Success(c, gin.H{"message": "SMTP connection successful"})
}

// SendTestEmailRequest 发送测试邮件请求
type SendTestEmailRequest struct {
	Email        string `json:"email" binding:"required,email"`
	SMTPHost     string `json:"smtp_host"`
	SMTPPort     int    `json:"smtp_port"`
	SMTPUsername string `json:"smtp_username"`
	SMTPPassword string `json:"smtp_password"`
	SMTPFrom     string `json:"smtp_from_email"`
	SMTPFromName string `json:"smtp_from_name"`
	SMTPUseTLS   *bool  `json:"smtp_use_tls"`
}

const maxTargetedEmailRecipients = 500

// TargetedEmailRequest is the administrator-authored email sent from the
// targeted delivery tool. Recipients are deliberately supplied by the admin UI
// so a selection can span multiple pages of the user list.
type TargetedEmailRequest struct {
	Recipients []string `json:"recipients" binding:"required,min=1,max=500"`
	Subject    string   `json:"subject" binding:"required,max=200"`
	HTML       string   `json:"html" binding:"required,max=200000"`
}

type TargetedEmailResult struct {
	Sent    int      `json:"sent"`
	Failed  int      `json:"failed"`
	Skipped int      `json:"skipped"`
	Errors  []string `json:"errors,omitempty"`
}

type TargetedEmailDeliveryStatusRequest struct {
	Recipients []string `json:"recipients" binding:"required,min=1,max=500"`
	Subject    string   `json:"subject" binding:"max=200"`
	HTML       string   `json:"html" binding:"max=200000"`
}
type TargetedEmailDeliveryStatus struct {
	LastSentAt  *string `json:"last_sent_at,omitempty"`
	SentCurrent bool    `json:"sent_current"`
	Blocked     bool    `json:"blocked"`
}

type TargetedEmailTemplate struct {
	Key     string `json:"key"`
	Subject string `json:"subject"`
	Body    string `json:"body"`
}
type TargetedEmailTemplatesRequest struct {
	Templates []TargetedEmailTemplate `json:"templates" binding:"required,len=4"`
}

func (h *SettingHandler) GetTargetedEmailTemplates(c *gin.Context) {
	raw, err := h.settingService.GetTargetedEmailTemplates(c.Request.Context())
	if err != nil && !errors.Is(err, service.ErrSettingNotFound) {
		response.InternalError(c, "Failed to load targeted email templates")
		return
	}
	if strings.TrimSpace(raw) == "" {
		response.Success(c, gin.H{"templates": []TargetedEmailTemplate{}})
		return
	}
	var templates []TargetedEmailTemplate
	if err := json.Unmarshal([]byte(raw), &templates); err != nil || !normalizeTargetedEmailTemplates(templates) {
		response.InternalError(c, "Invalid targeted email template setting")
		return
	}
	response.Success(c, gin.H{"templates": templates})
}

func (h *SettingHandler) SaveTargetedEmailTemplates(c *gin.Context) {
	var req TargetedEmailTemplatesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	if !normalizeTargetedEmailTemplates(req.Templates) {
		response.BadRequest(c, "Invalid template: each template requires a unique key, a subject of at most 200 characters and HTML of at most 50000 characters")
		return
	}
	raw, err := json.Marshal(req.Templates)
	if err != nil || h.settingService.SaveTargetedEmailTemplates(c.Request.Context(), string(raw)) != nil {
		response.InternalError(c, "Failed to save targeted email templates")
		return
	}
	response.Success(c, gin.H{"templates": req.Templates})
}

func normalizeTargetedEmailTemplates(templates []TargetedEmailTemplate) bool {
	if len(templates) != 4 {
		return false
	}
	keys := map[string]bool{"thanks": true, "offer": true, "promotion": true, "notice": true}
	seen := make(map[string]bool, len(templates))
	for index := range templates {
		item := &templates[index]
		item.Key = strings.TrimSpace(item.Key)
		item.Subject = strings.TrimSpace(item.Subject)
		item.Body = strings.TrimSpace(item.Body)
		if !keys[item.Key] || seen[item.Key] || item.Subject == "" || utf8.RuneCountInString(item.Subject) > 200 || item.Body == "" || utf8.RuneCountInString(item.Body) > 50000 {
			return false
		}
		seen[item.Key] = true
	}
	return true
}

// SendTestEmail 发送测试邮件
// POST /api/v1/admin/settings/send-test-email
func (h *SettingHandler) SendTestEmail(c *gin.Context) {
	var req SendTestEmailRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	req.SMTPHost = strings.TrimSpace(req.SMTPHost)
	req.SMTPUsername = strings.TrimSpace(req.SMTPUsername)
	req.SMTPFrom = strings.TrimSpace(req.SMTPFrom)
	req.SMTPFromName = strings.TrimSpace(req.SMTPFromName)

	var savedConfig *service.SMTPConfig
	if cfg, err := h.emailService.GetSMTPConfig(c.Request.Context()); err == nil && cfg != nil {
		savedConfig = cfg
	}

	if req.SMTPHost == "" && savedConfig != nil {
		req.SMTPHost = savedConfig.Host
	}
	if req.SMTPPort <= 0 {
		if savedConfig != nil && savedConfig.Port > 0 {
			req.SMTPPort = savedConfig.Port
		} else {
			req.SMTPPort = 587
		}
	}
	if req.SMTPUsername == "" && savedConfig != nil {
		req.SMTPUsername = savedConfig.Username
	}
	password := strings.TrimSpace(req.SMTPPassword)
	if password == "" && savedConfig != nil {
		password = savedConfig.Password
	}
	if req.SMTPFrom == "" && savedConfig != nil {
		req.SMTPFrom = savedConfig.From
	}
	if req.SMTPFromName == "" && savedConfig != nil {
		req.SMTPFromName = savedConfig.FromName
	}
	if req.SMTPHost == "" {
		response.BadRequest(c, "SMTP host is required")
		return
	}

	config := &service.SMTPConfig{
		Host:     req.SMTPHost,
		Port:     req.SMTPPort,
		Username: req.SMTPUsername,
		Password: password,
		From:     req.SMTPFrom,
		FromName: req.SMTPFromName,
		UseTLS:   resolveSMTPUseTLS(req.SMTPUseTLS, savedConfig),
	}

	siteName := h.settingService.GetSiteName(c.Request.Context())
	subject := "[" + siteName + "] Test Email"
	body := `
<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <style>
        body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; background-color: #f5f5f5; margin: 0; padding: 20px; }
        .container { max-width: 600px; margin: 0 auto; background-color: #ffffff; border-radius: 8px; overflow: hidden; box-shadow: 0 2px 8px rgba(0,0,0,0.1); }
        .header { background: linear-gradient(135deg, #667eea 0%, #764ba2 100%); color: white; padding: 30px; text-align: center; }
        .content { padding: 40px 30px; text-align: center; }
        .success { color: #10b981; font-size: 48px; margin-bottom: 20px; }
        .footer { background-color: #f8f9fa; padding: 20px; text-align: center; color: #999; font-size: 12px; }
    </style>
</head>
<body>
    <div class="container">
        <div class="header">
            <h1>` + html.EscapeString(siteName) + `</h1>
        </div>
        <div class="content">
            <div class="success">✓</div>
            <h2>Email Configuration Successful!</h2>
            <p>This is a test email to verify your SMTP settings are working correctly.</p>
        </div>
        <div class="footer">
            <p>This is an automated test message.</p>
        </div>
    </div>
</body>
</html>
`

	if err := h.emailService.SendEmailWithConfig(config, req.Email, subject, body); err != nil {
		response.BadRequest(c, "Failed to send test email: "+err.Error())
		return
	}

	response.Success(c, gin.H{"message": "Test email sent successfully"})
}

// SendTargetedEmailTest sends the current targeted email draft to exactly one
// recipient. It uses the saved SMTP configuration rather than form-local SMTP
// settings, so the result matches a subsequent batch send.
// POST /api/v1/admin/settings/targeted-email/test
func (h *SettingHandler) SendTargetedEmailTest(c *gin.Context) {
	var req TargetedEmailRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	if len(req.Recipients) != 1 {
		response.BadRequest(c, "test send requires exactly one recipient")
		return
	}
	if err := validateTargetedEmailRequest(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if h.targetedEmailDeliveries != nil {
		blocked, err := h.targetedEmailDeliveries.IsRecipientBlocked(c.Request.Context(), req.Recipients[0])
		if err != nil {
			response.InternalError(c, "Failed to load email preference")
			return
		}
		if blocked {
			response.ErrorFrom(c, service.ErrTargetedEmailBlocked)
			return
		}
	}
	if err := h.emailService.SendEmail(c.Request.Context(), req.Recipients[0], req.Subject, req.HTML); err != nil {
		response.BadRequest(c, "Failed to send test email: "+err.Error())
		return
	}
	response.Success(c, TargetedEmailResult{Sent: 1})
}

// SendTargetedEmailBatch queues a durable campaign; SMTP runs outside the HTTP request.
func (h *SettingHandler) SendTargetedEmailBatch(c *gin.Context) {
	var req struct {
		TargetedEmailRequest
		IntervalMinutes *int `json:"interval_minutes"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	if err := validateTargetedEmailRequest(&req.TargetedEmailRequest); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	interval := service.DefaultTargetedEmailIntervalMinutes
	if req.IntervalMinutes != nil {
		interval = *req.IntervalMinutes
	}
	if interval < 1 || interval > 1440 {
		response.BadRequest(c, "发送间隔须为 1–1440 分钟的整数")
		return
	}
	if h.targetedEmailDeliveries == nil {
		response.InternalError(c, "targeted email queue is not configured")
		return
	}
	if _, err := h.emailService.GetSMTPConfig(c.Request.Context()); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	job, err := h.targetedEmailDeliveries.CreateJob(c.Request.Context(), req.Recipients, req.Subject, req.HTML, interval)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, job)
}

func (h *SettingHandler) GetTargetedEmailJob(c *gin.Context) {
	if h.targetedEmailDeliveries == nil {
		response.InternalError(c, "targeted email queue is not configured")
		return
	}
	job, err := h.targetedEmailDeliveries.GetJob(c.Request.Context(), 0)
	if err != nil {
		response.InternalError(c, "Failed to load email job")
		return
	}
	response.Success(c, gin.H{"job": job})
}

func (h *SettingHandler) CancelTargetedEmailJob(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid job ID")
		return
	}
	if h.targetedEmailDeliveries == nil {
		response.InternalError(c, "targeted email queue is not configured")
		return
	}
	if err := h.targetedEmailDeliveries.CancelJob(c.Request.Context(), id); err != nil {
		response.InternalError(c, "Failed to cancel email job")
		return
	}
	response.Success(c, gin.H{"message": "任务已取消，正在发送的邮件可能仍会完成"})
}

func (h *SettingHandler) SetTargetedEmailUserPreference(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid user ID")
		return
	}
	var req struct {
		Blocked *bool `json:"blocked" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	if h.targetedEmailDeliveries == nil {
		response.InternalError(c, "targeted email queue is not configured")
		return
	}
	if err := h.targetedEmailDeliveries.SetUserBlocked(c.Request.Context(), id, *req.Blocked); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"user_id": id, "blocked": *req.Blocked})
}

// GetTargetedEmailDeliveryStatuses returns prior successful targeted deliveries for the current draft.
// POST /api/v1/admin/settings/targeted-email/status
func (h *SettingHandler) GetTargetedEmailDeliveryStatuses(c *gin.Context) {
	var req TargetedEmailDeliveryStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	if err := validateTargetedEmailRecipients(&req.Recipients); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if h.targetedEmailDeliveries == nil {
		response.Success(c, gin.H{"statuses": map[string]TargetedEmailDeliveryStatus{}})
		return
	}
	statuses, err := h.targetedEmailDeliveries.Statuses(c.Request.Context(), req.Recipients, service.TargetedEmailContentHash(req.Subject, req.HTML))
	if err != nil {
		response.InternalError(c, "Failed to load delivery history")
		return
	}
	items := make(map[string]TargetedEmailDeliveryStatus, len(statuses))
	for email, status := range statuses {
		item := TargetedEmailDeliveryStatus{SentCurrent: status.SentCurrent, Blocked: status.Blocked}
		if status.LastSentAt != nil {
			value := status.LastSentAt.Format(time.RFC3339)
			item.LastSentAt = &value
		}
		items[email] = item
	}
	response.Success(c, gin.H{"statuses": items})
}

func validateTargetedEmailRequest(req *TargetedEmailRequest) error {
	req.Subject = strings.TrimSpace(req.Subject)
	req.HTML = strings.TrimSpace(req.HTML)
	if req.Subject == "" || req.HTML == "" {
		return service.ErrEmailNotConfigured
	}
	return validateTargetedEmailRecipients(&req.Recipients)
}

func validateTargetedEmailRecipients(values *[]string) error {
	seen := make(map[string]struct{}, len(*values))
	recipients := make([]string, 0, len(*values))
	for _, raw := range *values {
		recipient := strings.TrimSpace(strings.ToLower(raw))
		parsed, err := mail.ParseAddress(recipient)
		if err != nil || parsed.Address != recipient {
			return service.ErrInvalidVerifyCode
		}
		if _, ok := seen[recipient]; ok {
			continue
		}
		seen[recipient] = struct{}{}
		recipients = append(recipients, recipient)
	}
	if len(recipients) == 0 || len(recipients) > maxTargetedEmailRecipients {
		return service.ErrInvalidVerifyCode
	}
	*values = recipients
	return nil
}

// ListEmailTemplates returns all editable notification email templates.
// GET /api/v1/admin/settings/email-templates
func (h *SettingHandler) ListEmailTemplates(c *gin.Context) {
	if h.notificationEmailService == nil {
		response.InternalError(c, "notification email service is not configured")
		return
	}
	events := h.notificationEmailService.ListEventInfos()
	templates, err := h.notificationEmailService.ListTemplates(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, dto.EmailTemplateListResponse{
		Events:       emailTemplateEventOptionsToDTO(events),
		Locales:      h.notificationEmailService.SupportedLocales(),
		Templates:    emailTemplateSummariesToDTO(templates),
		Placeholders: emailTemplatePlaceholderUnion(events),
	})
}

// GetEmailTemplate returns one editable notification email template.
// GET /api/v1/admin/settings/email-templates/:event/:locale
func (h *SettingHandler) GetEmailTemplate(c *gin.Context) {
	if h.notificationEmailService == nil {
		response.InternalError(c, "notification email service is not configured")
		return
	}
	tmpl, err := h.notificationEmailService.GetTemplate(c.Request.Context(), c.Param("event"), c.Param("locale"))
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.Success(c, emailTemplateDetailToDTO(tmpl))
}

// UpdateEmailTemplate saves an override for one event/locale template.
// PUT /api/v1/admin/settings/email-templates/:event/:locale
func (h *SettingHandler) UpdateEmailTemplate(c *gin.Context) {
	if h.notificationEmailService == nil {
		response.InternalError(c, "notification email service is not configured")
		return
	}
	var req dto.UpdateEmailTemplateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	tmpl, err := h.notificationEmailService.UpdateTemplate(c.Request.Context(), c.Param("event"), c.Param("locale"), req.Subject, req.HTML)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.Success(c, emailTemplateDetailToDTO(tmpl))
}

// RestoreOfficialEmailTemplate removes an override and returns the built-in template.
// POST /api/v1/admin/settings/email-templates/:event/:locale/restore-official
func (h *SettingHandler) RestoreOfficialEmailTemplate(c *gin.Context) {
	if h.notificationEmailService == nil {
		response.InternalError(c, "notification email service is not configured")
		return
	}
	tmpl, err := h.notificationEmailService.RestoreOfficialTemplate(c.Request.Context(), c.Param("event"), c.Param("locale"))
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.Success(c, emailTemplateDetailToDTO(tmpl))
}

// PreviewEmailTemplate renders a template with safe sample variables without saving it.
// POST /api/v1/admin/settings/email-templates/preview
func (h *SettingHandler) PreviewEmailTemplate(c *gin.Context) {
	if h.notificationEmailService == nil {
		response.InternalError(c, "notification email service is not configured")
		return
	}
	var req dto.PreviewEmailTemplateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	preview, err := h.notificationEmailService.PreviewTemplate(c.Request.Context(), service.NotificationEmailPreviewInput{
		Event:     req.Event,
		Locale:    req.Locale,
		Subject:   req.Subject,
		HTML:      req.HTML,
		Variables: req.Variables,
	})
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.Success(c, dto.EmailTemplatePreviewResponse{Subject: preview.Subject, HTML: preview.HTML})
}

func emailTemplateEventOptionsToDTO(events []service.NotificationEmailEventInfo) []dto.EmailTemplateEventOption {
	items := make([]dto.EmailTemplateEventOption, 0, len(events))
	for _, event := range events {
		items = append(items, dto.EmailTemplateEventOption{
			Value:       event.Event,
			Label:       event.Label,
			Description: event.Description,
			Category:    event.Category,
			Optional:    event.Optional,
		})
	}
	return items
}

func emailTemplateSummariesToDTO(templates []service.NotificationEmailTemplate) []dto.EmailTemplateSummary {
	items := make([]dto.EmailTemplateSummary, 0, len(templates))
	for _, tmpl := range templates {
		items = append(items, dto.EmailTemplateSummary{
			Event:     tmpl.Event,
			Locale:    tmpl.Locale,
			Subject:   tmpl.Subject,
			IsCustom:  tmpl.IsCustom,
			UpdatedAt: emailTemplateUpdatedAt(tmpl),
		})
	}
	return items
}

func emailTemplateDetailToDTO(tmpl service.NotificationEmailTemplate) dto.EmailTemplateDetail {
	return dto.EmailTemplateDetail{
		Event:        tmpl.Event,
		Locale:       tmpl.Locale,
		Subject:      tmpl.Subject,
		HTML:         tmpl.HTML,
		IsCustom:     tmpl.IsCustom,
		UpdatedAt:    emailTemplateUpdatedAt(tmpl),
		Placeholders: tmpl.Placeholders,
	}
}

func emailTemplateUpdatedAt(tmpl service.NotificationEmailTemplate) string {
	if tmpl.UpdatedAt == nil {
		return ""
	}
	return tmpl.UpdatedAt.Format("2006-01-02T15:04:05Z07:00")
}

func emailTemplatePlaceholderUnion(events []service.NotificationEmailEventInfo) []string {
	seen := make(map[string]struct{})
	placeholders := make([]string, 0)
	for _, event := range events {
		for _, placeholder := range event.Placeholders {
			if _, ok := seen[placeholder]; ok {
				continue
			}
			seen[placeholder] = struct{}{}
			placeholders = append(placeholders, placeholder)
		}
	}
	return placeholders
}
