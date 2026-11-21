package admin

import (
	"context"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *AccountHandler) SetKeywordRoutingSettings(settings *service.SettingService) {
	if h != nil {
		h.keywordRoutingSettings = settings
	}
}
func (h *AccountHandler) GetKeywordRoutingConfig(c *gin.Context) {
	if h.keywordRoutingSettings == nil {
		response.Error(c, 503, "Keyword routing unavailable")
		return
	}
	config, err := h.keywordRoutingSettings.GetKeywordRoutingConfig(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, config)
}
func (h *AccountHandler) SetKeywordRoutingConfig(c *gin.Context) {
	if h.keywordRoutingSettings == nil {
		response.Error(c, 503, "Keyword routing unavailable")
		return
	}
	var input struct {
		Keywords []any `json:"keywords"`
	}
	if err := c.ShouldBindJSON(&input); err != nil || input.Keywords == nil {
		response.BadRequest(c, "keywords must be an array of strings")
		return
	}
	config := service.KeywordRoutingConfig{Keywords: make([]string, 0, len(input.Keywords))}
	for _, value := range input.Keywords {
		word, ok := value.(string)
		if !ok {
			response.BadRequest(c, "keywords must be an array of strings")
			return
		}
		config.Keywords = append(config.Keywords, word)
	}
	config, err := h.keywordRoutingSettings.SetKeywordRoutingConfig(c.Request.Context(), config)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, config)
}
func (h *AccountHandler) GetKeywordRoutingTodayStats(c *gin.Context) {
	var groupID *int64
	if raw, exists := c.GetQuery("group_id"); exists {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id < 0 {
			response.BadRequest(c, "Invalid group_id")
			return
		}
		groupID = &id
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), time.Second)
	defer cancel()
	stats, err := h.keywordRoutingSettings.GetKeywordRoutingTodayStats(ctx, groupID)
	if err != nil {
		response.Error(c, 503, "Keyword routing statistics unavailable")
		return
	}
	response.Success(c, stats)
}
