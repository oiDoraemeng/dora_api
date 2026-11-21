package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type keywordRoutingSettingsRepo struct {
	service.SettingRepository
	raw string
}

func (r *keywordRoutingSettingsRepo) GetValue(context.Context, string) (string, error) {
	return r.raw, nil
}
func (r *keywordRoutingSettingsRepo) Set(_ context.Context, _, raw string) error {
	r.raw = raw
	return nil
}
func TestKeywordRoutingAdminConfigAndUnavailableStats(t *testing.T) {
	h := &AccountHandler{}
	h.SetKeywordRoutingSettings(service.NewSettingService(&keywordRoutingSettingsRepo{}, nil))
	router := gin.New()
	router.GET("/keyword-routing", h.GetKeywordRoutingConfig)
	router.PUT("/keyword-routing", h.SetKeywordRoutingConfig)
	router.GET("/stats", h.GetKeywordRoutingTodayStats)
	cases := []struct {
		method, path, body string
		status             int
		contains           string
	}{
		{http.MethodGet, "/keyword-routing", "", 200, `"keywords":[]`},
		{http.MethodPut, "/keyword-routing", `{"keywords":[" HELLO ","hello","画图"]}`, 200, `"keywords":["HELLO","画图"]`},
		{http.MethodGet, "/keyword-routing", "", 200, `"keywords":["HELLO","画图"]`},
		{http.MethodPut, "/keyword-routing", `{"keywords":null}`, 400, "array of strings"},
		{http.MethodPut, "/keyword-routing", `{"keywords":[12]}`, 400, "array of strings"},
		{http.MethodPut, "/keyword-routing", `{"keywords":[null]}`, 400, "array of strings"},
		{http.MethodGet, "/stats?group_id=bad", "", 400, "Invalid group_id"},
		{http.MethodGet, "/stats", "", 503, "statistics unavailable"},
	}
	for _, tc := range cases {
		request := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		request.Header.Set("Content-Type", "application/json")
		writer := httptest.NewRecorder()
		router.ServeHTTP(writer, request)
		require.Equal(t, tc.status, writer.Code)
		require.Contains(t, writer.Body.String(), tc.contains)
	}
}
