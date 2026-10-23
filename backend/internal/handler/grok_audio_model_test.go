package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGrokRealtimeRequiresExplicitModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	apiKey := &service.APIKey{Group: &service.Group{Platform: service.PlatformGrok}}
	router := gin.New()
	router.GET("/realtime", func(c *gin.Context) {
		c.Set(string(middleware2.ContextKeyAPIKey), apiKey)
		(&OpenAIGatewayHandler{}).GrokRealtime(c)
	})
	request := httptest.NewRequest(http.MethodGet, "/realtime", nil)
	request.Header.Set("Connection", "Upgrade")
	request.Header.Set("Upgrade", "websocket")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Contains(t, recorder.Body.String(), "model is required")
}
