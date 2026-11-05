//go:build unit

package admin

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func bindSMTPRequest[T any](t *testing.T, body string) T {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = req

	var payload T
	require.NoError(t, c.ShouldBindJSON(&payload))
	return payload
}

func TestTargetedEmailTestCannotBypassUserBan(t *testing.T) {
	for _, lookupFails := range []bool{false, true} {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer db.Close()
		query := mock.ExpectQuery("SELECT EXISTS").WithArgs(nil, "user@example.com")
		if lookupFails {
			query.WillReturnError(errors.New("database unavailable"))
		} else {
			query.WillReturnRows(sqlmock.NewRows([]string{"blocked"}).AddRow(true))
		}
		h := &SettingHandler{targetedEmailDeliveries: service.NewTargetedEmailDeliveryService(db)}
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(`{"recipients":["User@Example.com"],"subject":"Subject","html":"Body"}`))
		c.Request.Header.Set("Content-Type", "application/json")
		h.SendTargetedEmailTest(c)
		if lookupFails {
			require.Equal(t, http.StatusInternalServerError, recorder.Code)
		} else {
			require.Equal(t, http.StatusBadRequest, recorder.Code)
			require.Contains(t, recorder.Body.String(), "TARGETED_EMAIL_BLOCKED")
		}
		// emailService is nil: reaching SMTP would panic and fail this test.
		require.NoError(t, mock.ExpectationsWereMet())
	}
}

func TestTargetedEmailUserPreferenceValidatesExplicitBoolean(t *testing.T) {
	for _, body := range []string{`{}`, `{"blocked":null}`, `{"blocked":"true"}`} {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Params = gin.Params{{Key: "id", Value: "1"}}
		c.Request = httptest.NewRequest(http.MethodPut, "/", bytes.NewBufferString(body))
		c.Request.Header.Set("Content-Type", "application/json")
		(&SettingHandler{}).SetTargetedEmailUserPreference(c)
		require.Equal(t, http.StatusBadRequest, recorder.Code)
	}
	for _, blocked := range []bool{true, false} {
		body, err := json.Marshal(map[string]bool{"blocked": blocked})
		require.NoError(t, err)
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Params = gin.Params{{Key: "id", Value: "1"}}
		c.Request = httptest.NewRequest(http.MethodPut, "/", bytes.NewReader(body))
		c.Request.Header.Set("Content-Type", "application/json")
		(&SettingHandler{}).SetTargetedEmailUserPreference(c)
		require.Equal(t, http.StatusInternalServerError, recorder.Code, "both booleans must pass binding and reach service validation")
	}
}

func TestTargetedEmailStatusesAllowEmptyDraft(t *testing.T) {
	req := bindSMTPRequest[TargetedEmailDeliveryStatusRequest](t, `{"recipients":["User@Example.com"]}`)
	require.NoError(t, validateTargetedEmailRecipients(&req.Recipients))
	require.Equal(t, []string{"user@example.com"}, req.Recipients)
}

func TestTestSMTPRequestUseTLSOmissionFallsBackToSavedSetting(t *testing.T) {
	req := bindSMTPRequest[TestSMTPRequest](t, `{}`)

	require.Nil(t, req.SMTPUseTLS)
	require.True(t, resolveSMTPUseTLS(req.SMTPUseTLS, &service.SMTPConfig{UseTLS: true}))
}

func TestTestSMTPRequestExplicitFalseOverridesSavedUseTLS(t *testing.T) {
	req := bindSMTPRequest[TestSMTPRequest](t, `{"smtp_use_tls":false}`)

	require.NotNil(t, req.SMTPUseTLS)
	require.False(t, resolveSMTPUseTLS(req.SMTPUseTLS, &service.SMTPConfig{UseTLS: true}))
}

func TestTestSMTPRequestExplicitTrueOverridesMissingSavedUseTLS(t *testing.T) {
	req := bindSMTPRequest[TestSMTPRequest](t, `{"smtp_use_tls":true}`)

	require.NotNil(t, req.SMTPUseTLS)
	require.True(t, resolveSMTPUseTLS(req.SMTPUseTLS, nil))
}

func TestSendTestEmailRequestPreservesUseTLSOmissionSemantics(t *testing.T) {
	omitted := bindSMTPRequest[SendTestEmailRequest](t, `{"email":"admin@example.com"}`)
	explicitFalse := bindSMTPRequest[SendTestEmailRequest](t, `{"email":"admin@example.com","smtp_use_tls":false}`)
	saved := &service.SMTPConfig{UseTLS: true}

	require.True(t, resolveSMTPUseTLS(omitted.SMTPUseTLS, saved))
	require.False(t, resolveSMTPUseTLS(explicitFalse.SMTPUseTLS, saved))
}

func TestTargetedEmailBatchRejectsInvalidQueueRequests(t *testing.T) {
	for _, interval := range []any{0, -1, 1441, 1.5, "5"} {
		body, err := json.Marshal(map[string]any{
			"recipients": []string{"user@example.com"}, "subject": "Subject", "html": "<p>Body</p>", "interval_minutes": interval,
		})
		require.NoError(t, err)
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
		c.Request.Header.Set("Content-Type", "application/json")
		(&SettingHandler{}).SendTargetedEmailBatch(c)
		require.Equal(t, http.StatusBadRequest, recorder.Code, "interval=%v", interval)
	}
	for _, body := range []string{
		`{"recipients":[],"subject":"Subject","html":"Body"}`,
		`{"recipients":["bad-address"],"subject":"Subject","html":"Body"}`,
		`{"recipients":["user@example.com"],"subject":"","html":"Body"}`,
	} {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(body))
		c.Request.Header.Set("Content-Type", "application/json")
		(&SettingHandler{}).SendTargetedEmailBatch(c)
		require.Equal(t, http.StatusBadRequest, recorder.Code)
	}
}
