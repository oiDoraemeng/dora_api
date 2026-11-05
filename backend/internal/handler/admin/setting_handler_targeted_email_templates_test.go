//go:build unit

package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type targetedEmailTemplateRepoStub struct {
	*settingHandlerRepoStub
	readErr, writeErr error
}

func (s *targetedEmailTemplateRepoStub) GetValue(ctx context.Context, key string) (string, error) {
	if s.readErr != nil {
		return "", s.readErr
	}
	value, ok := s.values[key]
	if !ok {
		return "", service.ErrSettingNotFound
	}
	return value, nil
}

func (s *targetedEmailTemplateRepoStub) Set(ctx context.Context, key, value string) error {
	if s.writeErr != nil {
		return s.writeErr
	}
	s.values[key] = value
	return nil
}

func targetedTemplateHandler(repo *targetedEmailTemplateRepoStub) *SettingHandler {
	return &SettingHandler{settingService: service.NewSettingService(repo, &config.Config{})}
}

func targetedTemplateRequest(t *testing.T, h *SettingHandler, method string, templates []TargetedEmailTemplate) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(TargetedEmailTemplatesRequest{Templates: templates})
	require.NoError(t, err)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(method, "/", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	if method == http.MethodPut {
		h.SaveTargetedEmailTemplates(c)
	} else {
		h.GetTargetedEmailTemplates(c)
	}
	return recorder
}

func targetedTemplateResponse(t *testing.T, recorder *httptest.ResponseRecorder) []TargetedEmailTemplate {
	t.Helper()
	var result struct {
		Data struct {
			Templates []TargetedEmailTemplate `json:"templates"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &result))
	return result.Data.Templates
}

func exampleTargetedTemplates() []TargetedEmailTemplate {
	return []TargetedEmailTemplate{
		{Key: " thanks ", Subject: " 感谢主题 ", Body: " <p>已编辑的感谢正文</p> "},
		{Key: "offer", Subject: "优惠主题", Body: "<p>优惠正文</p>"},
		{Key: "promotion", Subject: "推广主题", Body: "<p>推广正文</p>"},
		{Key: "notice", Subject: "通知主题", Body: "<p>通知正文</p>"},
	}
}

func TestTargetedEmailTemplatesSaveReloadAndEdit(t *testing.T) {
	repo := &targetedEmailTemplateRepoStub{settingHandlerRepoStub: &settingHandlerRepoStub{values: map[string]string{}}}
	first := targetedTemplateHandler(repo)
	saved := targetedTemplateRequest(t, first, http.MethodPut, exampleTargetedTemplates())
	require.Equal(t, http.StatusOK, saved.Code)
	// Use a new settings service/handler, so no local template state is reused.
	reloaded := targetedTemplateRequest(t, targetedTemplateHandler(repo), http.MethodGet, nil)
	require.Equal(t, http.StatusOK, reloaded.Code)
	items := targetedTemplateResponse(t, reloaded)
	require.Len(t, items, 4)
	require.Equal(t, "thanks", items[0].Key)
	require.Equal(t, "感谢主题", items[0].Subject)
	require.Equal(t, "<p>已编辑的感谢正文</p>", items[0].Body)
	items[0].Subject = strings.Repeat("谢", 200)
	items[0].Body = "<html><body>再次修改后的正文</body></html>"
	updated := targetedTemplateRequest(t, targetedTemplateHandler(repo), http.MethodPut, items)
	require.Equal(t, http.StatusOK, updated.Code, updated.Body.String())
	reloaded = targetedTemplateRequest(t, targetedTemplateHandler(repo), http.MethodGet, nil)
	require.Equal(t, items, targetedTemplateResponse(t, reloaded))
}

func TestTargetedEmailTemplateReadAndWriteFailuresRemainVisible(t *testing.T) {
	repo := &targetedEmailTemplateRepoStub{settingHandlerRepoStub: &settingHandlerRepoStub{values: map[string]string{}}}
	h := targetedTemplateHandler(repo)
	missing := targetedTemplateRequest(t, h, http.MethodGet, nil)
	require.Equal(t, http.StatusOK, missing.Code)
	require.Empty(t, targetedTemplateResponse(t, missing))
	repo.readErr = errors.New("database unavailable")
	require.Equal(t, http.StatusInternalServerError, targetedTemplateRequest(t, h, http.MethodGet, nil).Code)
	repo.readErr = nil
	require.Equal(t, http.StatusOK, targetedTemplateRequest(t, h, http.MethodPut, exampleTargetedTemplates()).Code)
	previous := targetedTemplateResponse(t, targetedTemplateRequest(t, h, http.MethodGet, nil))
	repo.writeErr = errors.New("write failed")
	changed := exampleTargetedTemplates()
	changed[0].Subject = "未保存的修改"
	require.Equal(t, http.StatusInternalServerError, targetedTemplateRequest(t, h, http.MethodPut, changed).Code)
	require.Equal(t, previous, targetedTemplateResponse(t, targetedTemplateRequest(t, h, http.MethodGet, nil)))
}

func TestTargetedEmailTemplatesRejectInvalidData(t *testing.T) {
	repo := &targetedEmailTemplateRepoStub{settingHandlerRepoStub: &settingHandlerRepoStub{values: map[string]string{}}}
	for _, change := range []func([]TargetedEmailTemplate){
		func(items []TargetedEmailTemplate) { items[1].Key = "thanks" },
		func(items []TargetedEmailTemplate) { items[0].Key = "unknown" },
		func(items []TargetedEmailTemplate) { items[0].Subject = " " },
		func(items []TargetedEmailTemplate) { items[0].Body = " " },
		func(items []TargetedEmailTemplate) { items[0].Subject = strings.Repeat("谢", 201) },
	} {
		items := exampleTargetedTemplates()
		change(items)
		require.Equal(t, http.StatusBadRequest, targetedTemplateRequest(t, targetedTemplateHandler(repo), http.MethodPut, items).Code)
	}
	require.Empty(t, repo.values)
}
