package service

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/http/cookiejar"
	"regexp"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
	"golang.org/x/crypto/sha3"
)

const (
	openAIChatGPTWebPrepareURL          = "https://chatgpt.com/backend-api/f/conversation/prepare"
	openAIChatGPTWebConversationURL     = "https://chatgpt.com/backend-api/f/conversation"
	openAIChatGPTWebChatRequirementsURL = "https://chatgpt.com/backend-api/sentinel/chat-requirements"
	openAIChatGPTWebDefaultPowScript    = "https://chatgpt.com/backend-api/sentinel/sdk.js"
	openAIChatGPTWebDefaultUserAgent    = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/143.0.0.0 Safari/537.36 Edg/143.0.0.0"
	openAIChatGPTWebSSEReadLimit        = 8 << 20
)

type openAIChatGPTWebReference struct {
	FileID      string
	FileName    string
	FileSize    int
	MimeType    string
	Width       int
	Height      int
	ContentType string
}

type openAIChatGPTWebRequirements struct {
	Token     string
	Proof     string
	Turnstile string
	SO        string
}

type openAIChatGPTWebClient struct {
	svc        *OpenAIGatewayService
	account    *Account
	token      string
	userAgent  string
	deviceID   string
	sessionID  string
	proxyURL   string
	scripts    []string
	dataBuild  string
	lastHeader http.Header
	jar        *cookiejar.Jar
}

func (s *OpenAIGatewayService) forwardOpenAIImagesOAuthChatGPTWeb(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	parsed *OpenAIImagesRequest,
	channelMappedModel string,
) (*OpenAIForwardResult, error) {
	startTime := time.Now()
	requestModel := strings.TrimSpace(parsed.Model)
	if mapped := strings.TrimSpace(channelMappedModel); mapped != "" {
		requestModel = mapped
	}
	if requestModel == "" {
		requestModel = "gpt-image-2"
	}
	if err := validateOpenAIImagesModel(requestModel); err != nil {
		return nil, err
	}

	upstreamCtx, releaseUpstreamCtx := detachStreamUpstreamContext(ctx, parsed.Stream)
	defer releaseUpstreamCtx()

	token, _, err := s.GetAccessToken(upstreamCtx, account)
	if err != nil {
		return nil, err
	}

	client := s.newOpenAIChatGPTWebClient(account, token)
	results := make([]openAIResponsesImageResult, 0, parsed.N)
	count := parsed.N
	if count <= 0 {
		count = 1
	}
	for index := 0; index < count; index++ {
		result, err := client.generate(upstreamCtx, parsed, requestModel)
		if err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	if len(results) == 0 {
		return nil, fmt.Errorf("upstream did not return image output")
	}

	createdAt := time.Now().Unix()
	firstMeta := results[0]
	body, err := buildOpenAIImagesAPIResponse(results, createdAt, nil, firstMeta, parsed.ResponseFormat)
	if err != nil {
		return nil, err
	}

	if parsed.Stream {
		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("Connection", "keep-alive")
		c.Status(http.StatusOK)
		flusher, ok := c.Writer.(http.Flusher)
		if !ok {
			return nil, fmt.Errorf("streaming is not supported by response writer")
		}
		eventName := openAIImagesStreamPrefix(parsed) + ".completed"
		for _, img := range results {
			payload := buildOpenAIImagesStreamCompletedPayload(eventName, img, parsed.ResponseFormat, createdAt, nil)
			if err := s.writeOpenAIImagesStreamEvent(c, flusher, eventName, payload); err != nil {
				return &OpenAIForwardResult{
					Model:           requestModel,
					UpstreamModel:   requestModel,
					Stream:          parsed.Stream,
					ResponseHeaders: client.lastHeader.Clone(),
					Duration:        time.Since(startTime),
					ImageCount:      len(results),
					ImageSize:       parsed.SizeTier,
				}, err
			}
		}
		_, _ = io.WriteString(c.Writer, "data: [DONE]\n\n")
		flusher.Flush()
	} else {
		c.Data(http.StatusOK, "application/json; charset=utf-8", body)
	}

	return &OpenAIForwardResult{
		RequestID:       strings.TrimSpace(client.lastHeader.Get("x-request-id")),
		Usage:           OpenAIUsage{},
		Model:           requestModel,
		UpstreamModel:   requestModel,
		Stream:          parsed.Stream,
		ResponseHeaders: client.lastHeader.Clone(),
		Duration:        time.Since(startTime),
		ImageCount:      len(results),
		ImageSize:       parsed.SizeTier,
	}, nil
}

func (s *OpenAIGatewayService) newOpenAIChatGPTWebClient(account *Account, token string) *openAIChatGPTWebClient {
	userAgent := strings.TrimSpace(account.GetOpenAIUserAgent())
	if userAgent == "" {
		userAgent = openAIChatGPTWebDefaultUserAgent
	}
	deviceID := strings.TrimSpace(account.GetOpenAIDeviceID())
	if deviceID == "" {
		deviceID = uuid.NewString()
	}
	sessionID := strings.TrimSpace(account.GetOpenAISessionID())
	if sessionID == "" {
		sessionID = uuid.NewString()
	}
	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	jar, _ := cookiejar.New(nil)
	return &openAIChatGPTWebClient{
		svc:       s,
		account:   account,
		token:     token,
		userAgent: userAgent,
		deviceID:  deviceID,
		sessionID: sessionID,
		proxyURL:  proxyURL,
		jar:       jar,
	}
}

func (c *openAIChatGPTWebClient) generate(ctx context.Context, parsed *OpenAIImagesRequest, model string) (openAIResponsesImageResult, error) {
	references, err := c.prepareReferences(ctx, parsed)
	if err != nil {
		return openAIResponsesImageResult{}, err
	}
	prompt := openAIChatGPTWebPromptWithSize(parsed.Prompt, parsed.Size)
	if err := c.bootstrap(ctx); err != nil {
		return openAIResponsesImageResult{}, err
	}
	requirements, err := c.chatRequirements(ctx)
	if err != nil {
		return openAIResponsesImageResult{}, err
	}
	conduitToken, err := c.prepareConversation(ctx, prompt, model, requirements)
	if err != nil {
		return openAIResponsesImageResult{}, err
	}
	sseBody, err := c.startConversation(ctx, prompt, model, requirements, conduitToken, references)
	if err != nil {
		return openAIResponsesImageResult{}, err
	}
	state := parseOpenAIChatGPTWebSSEState(sseBody)
	if len(state.fileIDs) == 0 && len(state.sedimentIDs) == 0 && state.conversationID != "" {
		fileIDs, sedimentIDs, pollErr := c.pollImageResults(ctx, state.conversationID, 120*time.Second)
		if pollErr != nil {
			return openAIResponsesImageResult{}, pollErr
		}
		state.fileIDs = appendUniqueStrings(state.fileIDs, fileIDs...)
		state.sedimentIDs = appendUniqueStrings(state.sedimentIDs, sedimentIDs...)
	}
	if len(state.fileIDs) == 0 && len(state.sedimentIDs) == 0 {
		if strings.TrimSpace(state.message) != "" {
			return openAIResponsesImageResult{}, fmt.Errorf("%s", state.message)
		}
		return openAIResponsesImageResult{}, fmt.Errorf("upstream did not return image output")
	}
	urls, err := c.resolveImageURLs(ctx, state.conversationID, state.fileIDs, state.sedimentIDs)
	if err != nil {
		return openAIResponsesImageResult{}, err
	}
	if len(urls) == 0 {
		return openAIResponsesImageResult{}, fmt.Errorf("upstream image download url is empty")
	}
	var (
		imageBytes  []byte
		contentType string
		lastErr     error
	)
	for _, url := range urls {
		imageBytes, contentType, err = c.downloadImage(ctx, url)
		if err == nil {
			lastErr = nil
			break
		}
		lastErr = err
	}
	if lastErr != nil {
		return openAIResponsesImageResult{}, lastErr
	}
	return openAIResponsesImageResult{
		Result:        base64.StdEncoding.EncodeToString(imageBytes),
		RevisedPrompt: strings.TrimSpace(parsed.Prompt),
		OutputFormat:  openAIChatGPTWebOutputFormat(contentType),
		Size:          strings.TrimSpace(parsed.Size),
		Background:    strings.TrimSpace(parsed.Background),
		Quality:       strings.TrimSpace(parsed.Quality),
		Model:         model,
	}, nil
}

func (c *openAIChatGPTWebClient) prepareReferences(ctx context.Context, parsed *OpenAIImagesRequest) ([]openAIChatGPTWebReference, error) {
	if parsed == nil || !parsed.IsEdits() {
		return nil, nil
	}
	references := make([]openAIChatGPTWebReference, 0, len(parsed.Uploads)+len(parsed.InputImageURLs)+1)
	for index, upload := range parsed.Uploads {
		fileName := strings.TrimSpace(upload.FileName)
		if fileName == "" {
			fileName = fmt.Sprintf("image_%d.png", index+1)
		}
		ref, err := c.uploadImage(ctx, upload.Data, fileName, upload.ContentType, upload.Width, upload.Height)
		if err != nil {
			return nil, err
		}
		references = append(references, ref)
	}
	for index, imageURL := range parsed.InputImageURLs {
		data, contentType, err := c.resolveInputImage(ctx, imageURL)
		if err != nil {
			return nil, err
		}
		ref, err := c.uploadImage(ctx, data, fmt.Sprintf("image_%d%s", index+1, openAIChatGPTWebExtension(contentType)), contentType, 0, 0)
		if err != nil {
			return nil, err
		}
		references = append(references, ref)
	}
	if parsed.MaskUpload != nil && len(parsed.MaskUpload.Data) > 0 {
		ref, err := c.uploadImage(ctx, parsed.MaskUpload.Data, parsed.MaskUpload.FileName, parsed.MaskUpload.ContentType, parsed.MaskUpload.Width, parsed.MaskUpload.Height)
		if err != nil {
			return nil, err
		}
		references = append(references, ref)
	}
	if maskURL := strings.TrimSpace(parsed.MaskImageURL); maskURL != "" {
		data, contentType, err := c.resolveInputImage(ctx, maskURL)
		if err != nil {
			return nil, err
		}
		ref, err := c.uploadImage(ctx, data, "mask"+openAIChatGPTWebExtension(contentType), contentType, 0, 0)
		if err != nil {
			return nil, err
		}
		references = append(references, ref)
	}
	if len(references) == 0 {
		return nil, fmt.Errorf("image input is required")
	}
	return references, nil
}

func (c *openAIChatGPTWebClient) resolveInputImage(ctx context.Context, raw string) ([]byte, string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, "", fmt.Errorf("image url is empty")
	}
	if normalized := normalizeOpenAIImageBase64(raw); normalized != "" {
		data, err := base64.StdEncoding.DecodeString(normalized)
		if err != nil {
			return nil, "", err
		}
		contentType := "image/png"
		if strings.HasPrefix(strings.ToLower(raw), "data:") {
			if semi := strings.Index(raw, ";"); semi > len("data:") {
				contentType = raw[len("data:"):semi]
			}
		}
		return data, contentType, nil
	}
	return c.downloadImage(ctx, raw)
}

func (c *openAIChatGPTWebClient) bootstrap(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, openAIChatGPTStartURL, nil)
	if err != nil {
		return err
	}
	for key, value := range c.bootstrapHeaders() {
		req.Header.Set(key, value)
	}
	resp, err := c.do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		c.useDefaultPowResources()
		logger.LegacyPrintf(
			"service.openai_gateway",
			"[OpenAI ChatGPT Web] bootstrap returned status=%d; continuing with default sentinel PoW resources account_id=%d",
			resp.StatusCode,
			c.account.ID,
		)
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	c.scripts, c.dataBuild = parseOpenAIChatGPTWebPowResources(string(body))
	if len(c.scripts) == 0 {
		c.useDefaultPowResources()
	}
	return nil
}

func (c *openAIChatGPTWebClient) useDefaultPowResources() {
	c.scripts = []string{openAIChatGPTWebDefaultPowScript}
	c.dataBuild = ""
}

func (c *openAIChatGPTWebClient) chatRequirements(ctx context.Context) (openAIChatGPTWebRequirements, error) {
	body, _ := json.Marshal(map[string]string{
		"p": buildOpenAIChatGPTWebLegacyRequirementsToken(c.userAgent, c.scripts, c.dataBuild),
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, openAIChatGPTWebChatRequirementsURL, bytes.NewReader(body))
	if err != nil {
		return openAIChatGPTWebRequirements{}, err
	}
	c.applyCommonHeaders(req, "/backend-api/sentinel/chat-requirements")
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.do(req)
	if err != nil {
		return openAIChatGPTWebRequirements{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return openAIChatGPTWebRequirements{}, openAIChatGPTWebHTTPError(resp, "chat requirements failed")
	}
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return openAIChatGPTWebRequirements{}, err
	}
	token := strings.TrimSpace(gjson.GetBytes(respBody, "token").String())
	if token == "" {
		return openAIChatGPTWebRequirements{}, fmt.Errorf("missing chat requirements token")
	}
	requirements := openAIChatGPTWebRequirements{
		Token: token,
		SO:    strings.TrimSpace(gjson.GetBytes(respBody, "so_token").String()),
	}
	if gjson.GetBytes(respBody, "proofofwork.required").Bool() {
		proof, err := buildOpenAIChatGPTWebProofToken(
			gjson.GetBytes(respBody, "proofofwork.seed").String(),
			gjson.GetBytes(respBody, "proofofwork.difficulty").String(),
			c.userAgent,
			c.scripts,
			c.dataBuild,
		)
		if err != nil {
			return openAIChatGPTWebRequirements{}, err
		}
		requirements.Proof = proof
	}
	if gjson.GetBytes(respBody, "arkose.required").Bool() {
		return openAIChatGPTWebRequirements{}, fmt.Errorf("chat requirements requires arkose token")
	}
	return requirements, nil
}

func (c *openAIChatGPTWebClient) prepareConversation(ctx context.Context, prompt string, model string, requirements openAIChatGPTWebRequirements) (string, error) {
	payload := map[string]any{
		"action":                "next",
		"fork_from_shared_post": false,
		"parent_message_id":     uuid.NewString(),
		"model":                 openAIChatGPTWebImageModelSlug(model),
		"client_prepare_state":  "success",
		"timezone_offset_min":   -480,
		"timezone":              "Asia/Shanghai",
		"conversation_mode":     map[string]any{"kind": "primary_assistant"},
		"system_hints":          []string{"picture_v2"},
		"partial_query": map[string]any{
			"id":      uuid.NewString(),
			"author":  map[string]any{"role": "user"},
			"content": map[string]any{"content_type": "text", "parts": []any{prompt}},
		},
		"supports_buffering":       true,
		"supported_encodings":      []string{"v1"},
		"client_contextual_info":   map[string]any{"app_name": "chatgpt.com"},
		"enable_message_followups": true,
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, openAIChatGPTWebPrepareURL, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	c.applyImageHeaders(req, "/backend-api/f/conversation/prepare", requirements, "", "*/*")
	resp, err := c.do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", openAIChatGPTWebHTTPError(resp, "prepare image conversation failed")
	}
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(gjson.GetBytes(respBody, "conduit_token").String()), nil
}

func (c *openAIChatGPTWebClient) startConversation(
	ctx context.Context,
	prompt string,
	model string,
	requirements openAIChatGPTWebRequirements,
	conduitToken string,
	references []openAIChatGPTWebReference,
) ([]byte, error) {
	parts := make([]any, 0, len(references)+1)
	attachments := make([]any, 0, len(references))
	for _, ref := range references {
		parts = append(parts, map[string]any{
			"content_type":  "image_asset_pointer",
			"asset_pointer": "file-service://" + ref.FileID,
			"width":         ref.Width,
			"height":        ref.Height,
			"size_bytes":    ref.FileSize,
		})
		attachments = append(attachments, map[string]any{
			"id":       ref.FileID,
			"mimeType": ref.MimeType,
			"name":     ref.FileName,
			"size":     ref.FileSize,
			"width":    ref.Width,
			"height":   ref.Height,
		})
	}
	parts = append(parts, prompt)
	contentType := "text"
	if len(references) > 0 {
		contentType = "multimodal_text"
	}
	metadata := map[string]any{
		"developer_mode_connector_ids": []any{},
		"selected_github_repos":        []any{},
		"selected_all_github_repos":    false,
		"system_hints":                 []string{"picture_v2"},
		"serialization_metadata":       map[string]any{"custom_symbol_offsets": []any{}},
	}
	if len(attachments) > 0 {
		metadata["attachments"] = attachments
	}
	payload := map[string]any{
		"action": "next",
		"messages": []any{map[string]any{
			"id":          uuid.NewString(),
			"author":      map[string]any{"role": "user"},
			"create_time": time.Now().UnixMilli(),
			"content":     map[string]any{"content_type": contentType, "parts": parts},
			"metadata":    metadata,
		}},
		"parent_message_id":                    uuid.NewString(),
		"model":                                openAIChatGPTWebImageModelSlug(model),
		"client_prepare_state":                 "sent",
		"timezone_offset_min":                  -480,
		"timezone":                             "Asia/Shanghai",
		"conversation_mode":                    map[string]any{"kind": "primary_assistant"},
		"enable_message_followups":             true,
		"system_hints":                         []string{"picture_v2"},
		"supports_buffering":                   true,
		"supported_encodings":                  []string{"v1"},
		"client_contextual_info":               map[string]any{"app_name": "chatgpt.com", "is_dark_mode": false, "page_height": 1072, "page_width": 1724, "pixel_ratio": 1.2, "screen_height": 1440, "screen_width": 2560, "time_since_loaded": 1200},
		"paragen_cot_summary_display_override": "allow",
		"force_parallel_switch":                "auto",
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, openAIChatGPTWebConversationURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	c.applyImageHeaders(req, "/backend-api/f/conversation", requirements, conduitToken, "text/event-stream")
	resp, err := c.do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, openAIChatGPTWebHTTPError(resp, "start image conversation failed")
	}
	return io.ReadAll(io.LimitReader(resp.Body, openAIChatGPTWebSSEReadLimit))
}

func (c *openAIChatGPTWebClient) uploadImage(ctx context.Context, data []byte, fileName string, contentType string, width int, height int) (openAIChatGPTWebReference, error) {
	if len(data) == 0 {
		return openAIChatGPTWebReference{}, fmt.Errorf("image file is empty")
	}
	if contentType == "" {
		contentType = http.DetectContentType(data)
	}
	if fileName == "" {
		fileName = "image" + openAIChatGPTWebExtension(contentType)
	}
	metaPayload := map[string]any{
		"file_name": fileName,
		"file_size": len(data),
		"use_case":  "multimodal",
		"width":     width,
		"height":    height,
	}
	body, _ := json.Marshal(metaPayload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, openAIChatGPTFilesURL, bytes.NewReader(body))
	if err != nil {
		return openAIChatGPTWebReference{}, err
	}
	c.applyCommonHeaders(req, "/backend-api/files")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := c.do(req)
	if err != nil {
		return openAIChatGPTWebReference{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer func() { _ = resp.Body.Close() }()
		return openAIChatGPTWebReference{}, openAIChatGPTWebHTTPError(resp, "create image upload failed")
	}
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	_ = resp.Body.Close()
	if err != nil {
		return openAIChatGPTWebReference{}, err
	}
	fileID := strings.TrimSpace(gjson.GetBytes(respBody, "file_id").String())
	uploadURL := strings.TrimSpace(gjson.GetBytes(respBody, "upload_url").String())
	if fileID == "" || uploadURL == "" {
		return openAIChatGPTWebReference{}, fmt.Errorf("image upload metadata missing file_id or upload_url")
	}

	uploadReq, err := http.NewRequestWithContext(ctx, http.MethodPut, uploadURL, bytes.NewReader(data))
	if err != nil {
		return openAIChatGPTWebReference{}, err
	}
	uploadReq.Header.Set("Content-Type", contentType)
	uploadReq.Header.Set("x-ms-blob-type", "BlockBlob")
	uploadReq.Header.Set("x-ms-version", "2020-04-08")
	uploadReq.Header.Set("Origin", openAIChatGPTStartURL[:len(openAIChatGPTStartURL)-1])
	uploadReq.Header.Set("Referer", openAIChatGPTStartURL)
	uploadReq.Header.Set("User-Agent", c.userAgent)
	uploadResp, err := c.do(uploadReq)
	if err != nil {
		return openAIChatGPTWebReference{}, err
	}
	defer func() { _ = uploadResp.Body.Close() }()
	if uploadResp.StatusCode < 200 || uploadResp.StatusCode >= 300 {
		return openAIChatGPTWebReference{}, openAIChatGPTWebHTTPError(uploadResp, "upload image bytes failed")
	}

	doneReq, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("%s/%s/uploaded", openAIChatGPTFilesURL, fileID), strings.NewReader("{}"))
	if err != nil {
		return openAIChatGPTWebReference{}, err
	}
	c.applyCommonHeaders(doneReq, "/backend-api/files/"+fileID+"/uploaded")
	doneReq.Header.Set("Content-Type", "application/json")
	doneReq.Header.Set("Accept", "application/json")
	doneResp, err := c.do(doneReq)
	if err != nil {
		return openAIChatGPTWebReference{}, err
	}
	defer func() { _ = doneResp.Body.Close() }()
	if doneResp.StatusCode < 200 || doneResp.StatusCode >= 300 {
		return openAIChatGPTWebReference{}, openAIChatGPTWebHTTPError(doneResp, "finalize image upload failed")
	}
	return openAIChatGPTWebReference{
		FileID:      fileID,
		FileName:    fileName,
		FileSize:    len(data),
		MimeType:    contentType,
		Width:       width,
		Height:      height,
		ContentType: contentType,
	}, nil
}

func (c *openAIChatGPTWebClient) pollImageResults(ctx context.Context, conversationID string, timeout time.Duration) ([]string, []string, error) {
	deadline := time.Now().Add(timeout)
	for {
		fileIDs, sedimentIDs, err := c.fetchConversationImageIDs(ctx, conversationID)
		if err != nil {
			return nil, nil, err
		}
		if len(fileIDs) > 0 || len(sedimentIDs) > 0 {
			return fileIDs, sedimentIDs, nil
		}
		if time.Now().After(deadline) {
			return nil, nil, fmt.Errorf("image generation timed out")
		}
		timer := time.NewTimer(4 * time.Second)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return nil, nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func (c *openAIChatGPTWebClient) fetchConversationImageIDs(ctx context.Context, conversationID string) ([]string, []string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://chatgpt.com/backend-api/conversation/"+conversationID, nil)
	if err != nil {
		return nil, nil, err
	}
	c.applyCommonHeaders(req, "/backend-api/conversation/"+conversationID)
	req.Header.Set("Accept", "application/json")
	resp, err := c.do(req)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, nil, openAIChatGPTWebHTTPError(resp, "fetch image conversation failed")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, nil, err
	}
	state := parseOpenAIChatGPTWebConversationJSON(body)
	return state.fileIDs, state.sedimentIDs, nil
}

func (c *openAIChatGPTWebClient) resolveImageURLs(ctx context.Context, conversationID string, fileIDs []string, sedimentIDs []string) ([]string, error) {
	urls := make([]string, 0, len(fileIDs)+len(sedimentIDs))
	fileDownloadFailed := false
	for _, fileID := range fileIDs {
		fileID = strings.TrimSpace(strings.TrimPrefix(fileID, "file-service://"))
		if fileID == "" || fileID == "file_upload" {
			continue
		}
		url, err := c.fetchDownloadURL(ctx, fmt.Sprintf("%s/%s/download", openAIChatGPTFilesURL, fileID), "/backend-api/files/"+fileID+"/download")
		if err == nil && url != "" {
			urls = append(urls, url)
			continue
		}
		fileDownloadFailed = true
		if conversationID != "" {
			path := fmt.Sprintf("/backend-api/conversation/%s/attachment/%s/download", conversationID, fileID)
			if url, err := c.fetchDownloadURL(ctx, "https://chatgpt.com"+path, path); err == nil && url != "" {
				urls = append(urls, url)
			}
		}
	}
	if len(urls) > 0 || conversationID == "" || (!fileDownloadFailed && len(fileIDs) > 0 && len(sedimentIDs) == 0) {
		return urls, nil
	}
	for _, sedimentID := range sedimentIDs {
		sedimentID = strings.TrimSpace(strings.TrimPrefix(sedimentID, "sediment://"))
		if sedimentID == "" {
			continue
		}
		path := fmt.Sprintf("/backend-api/conversation/%s/attachment/%s/download", conversationID, sedimentID)
		url, err := c.fetchDownloadURL(ctx, "https://chatgpt.com"+path, path)
		if err == nil && url != "" {
			urls = append(urls, url)
		}
	}
	return urls, nil
}

func (c *openAIChatGPTWebClient) fetchDownloadURL(ctx context.Context, rawURL string, targetPath string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	c.applyCommonHeaders(req, targetPath)
	req.Header.Set("Accept", "application/json")
	resp, err := c.do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", openAIChatGPTWebHTTPError(resp, "fetch image download url failed")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return "", err
	}
	url := strings.TrimSpace(gjson.GetBytes(body, "download_url").String())
	if url == "" {
		url = strings.TrimSpace(gjson.GetBytes(body, "url").String())
	}
	return url, nil
}

func (c *openAIChatGPTWebClient) downloadImage(ctx context.Context, rawURL string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "image/*,*/*;q=0.8")
	if req.URL != nil && strings.EqualFold(req.URL.Hostname(), "chatgpt.com") {
		for key, value := range c.commonHeaders() {
			req.Header.Set(key, value)
		}
		if c.token != "" {
			req.Header.Set("Authorization", "Bearer "+c.token)
		}
		req.Header.Set("Accept", "image/*,*/*;q=0.8")
		req.Header.Set("Sec-Fetch-Dest", "image")
		req.Header.Set("Sec-Fetch-Mode", "no-cors")
		req.Header.Set("Sec-Fetch-Site", "same-origin")
	}
	resp, err := c.do(req)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", openAIChatGPTWebHTTPError(resp, "download image bytes failed")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, openAIImageMaxDownloadBytes))
	if err != nil {
		return nil, "", err
	}
	contentType := strings.TrimSpace(resp.Header.Get("Content-Type"))
	if contentType == "" {
		contentType = http.DetectContentType(data)
	}
	return data, contentType, nil
}

func (c *openAIChatGPTWebClient) do(req *http.Request) (*http.Response, error) {
	if req == nil {
		return nil, fmt.Errorf("nil request")
	}
	if c.jar != nil && req.URL != nil {
		for _, cookie := range c.jar.Cookies(req.URL) {
			req.AddCookie(cookie)
		}
	}
	var resp *http.Response
	var err error
	if c.svc != nil && c.svc.httpUpstream != nil {
		profile := c.tlsProfile(req)
		if profile != nil {
			resp, err = c.svc.httpUpstream.DoWithTLS(req, c.proxyURL, c.account.ID, c.account.Concurrency, profile)
		} else {
			resp, err = c.svc.httpUpstream.Do(req, c.proxyURL, c.account.ID, c.account.Concurrency)
		}
	} else {
		resp, err = http.DefaultClient.Do(req)
	}
	if resp != nil {
		c.lastHeader = resp.Header.Clone()
		if c.jar != nil && resp.Request != nil && resp.Request.URL != nil {
			c.jar.SetCookies(resp.Request.URL, resp.Cookies())
		} else if c.jar != nil && req.URL != nil {
			c.jar.SetCookies(req.URL, resp.Cookies())
		}
	}
	return resp, err
}

func (c *openAIChatGPTWebClient) tlsProfile(req *http.Request) *tlsfingerprint.Profile {
	if c == nil {
		return nil
	}
	if req == nil || req.URL == nil || !strings.EqualFold(req.URL.Hostname(), "chatgpt.com") {
		return nil
	}
	if c.svc != nil && c.svc.tlsFPProfileService != nil {
		if profile := c.svc.tlsFPProfileService.ResolveTLSProfile(c.account); profile != nil {
			return profile
		}
	}
	// ChatGPT Web's Sentinel endpoint is sensitive to Go's default TLS fingerprint.
	// Use the built-in uTLS profile even when the account has not explicitly enabled one.
	return &tlsfingerprint.Profile{Name: "Built-in Default (ChatGPT Web)"}
}

func (c *openAIChatGPTWebClient) applyCommonHeaders(req *http.Request, targetPath string) {
	headers := c.commonHeaders()
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	req.Header.Set("X-OpenAI-Target-Path", targetPath)
	req.Header.Set("X-OpenAI-Target-Route", targetPath)
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
}

func (c *openAIChatGPTWebClient) applyImageHeaders(req *http.Request, targetPath string, requirements openAIChatGPTWebRequirements, conduitToken string, accept string) {
	c.applyCommonHeaders(req, targetPath)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", accept)
	req.Header.Set("OpenAI-Sentinel-Chat-Requirements-Token", requirements.Token)
	if requirements.Proof != "" {
		req.Header.Set("OpenAI-Sentinel-Proof-Token", requirements.Proof)
	}
	if requirements.Turnstile != "" {
		req.Header.Set("OpenAI-Sentinel-Turnstile-Token", requirements.Turnstile)
	}
	if requirements.SO != "" {
		req.Header.Set("OpenAI-Sentinel-SO-Token", requirements.SO)
	}
	if conduitToken != "" {
		req.Header.Set("X-Conduit-Token", conduitToken)
	}
	if accept == "text/event-stream" {
		req.Header.Set("X-Oai-Turn-Trace-Id", uuid.NewString())
	}
}

func (c *openAIChatGPTWebClient) bootstrapHeaders() map[string]string {
	return map[string]string{
		"User-Agent":                  c.userAgent,
		"Accept":                      "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8",
		"Accept-Language":             "zh-CN,zh;q=0.9,en;q=0.8,en-US;q=0.7",
		"Sec-Ch-Ua":                   `"Microsoft Edge";v="143", "Chromium";v="143", "Not A(Brand";v="24"`,
		"Sec-Ch-Ua-Mobile":            "?0",
		"Sec-Ch-Ua-Platform":          `"Windows"`,
		"Sec-Ch-Ua-Arch":              `"x86"`,
		"Sec-Ch-Ua-Bitness":           `"64"`,
		"Sec-Ch-Ua-Full-Version":      `"143.0.3650.96"`,
		"Sec-Ch-Ua-Full-Version-List": `"Microsoft Edge";v="143.0.3650.96", "Chromium";v="143.0.7499.147", "Not A(Brand";v="24.0.0.0"`,
		"Sec-Ch-Ua-Platform-Version":  `"19.0.0"`,
		"Sec-Fetch-Dest":              "document",
		"Sec-Fetch-Mode":              "navigate",
		"Sec-Fetch-Site":              "none",
		"Sec-Fetch-User":              "?1",
		"Upgrade-Insecure-Requests":   "1",
	}
}

func (c *openAIChatGPTWebClient) commonHeaders() map[string]string {
	return map[string]string{
		"User-Agent":                  c.userAgent,
		"Origin":                      "https://chatgpt.com",
		"Referer":                     openAIChatGPTStartURL,
		"Accept-Language":             "zh-CN,zh;q=0.9,en;q=0.8,en-US;q=0.7",
		"Cache-Control":               "no-cache",
		"Pragma":                      "no-cache",
		"Priority":                    "u=1, i",
		"Sec-Ch-Ua":                   `"Microsoft Edge";v="143", "Chromium";v="143", "Not A(Brand";v="24"`,
		"Sec-Ch-Ua-Arch":              `"x86"`,
		"Sec-Ch-Ua-Bitness":           `"64"`,
		"Sec-Ch-Ua-Full-Version":      `"143.0.3650.96"`,
		"Sec-Ch-Ua-Full-Version-List": `"Microsoft Edge";v="143.0.3650.96", "Chromium";v="143.0.7499.147", "Not A(Brand";v="24.0.0.0"`,
		"Sec-Ch-Ua-Mobile":            "?0",
		"Sec-Ch-Ua-Model":             `""`,
		"Sec-Ch-Ua-Platform":          `"Windows"`,
		"Sec-Ch-Ua-Platform-Version":  `"19.0.0"`,
		"Sec-Fetch-Dest":              "empty",
		"Sec-Fetch-Mode":              "cors",
		"Sec-Fetch-Site":              "same-origin",
		"OAI-Device-Id":               c.deviceID,
		"OAI-Session-Id":              c.sessionID,
		"OAI-Language":                "zh-CN",
		"OAI-Client-Version":          "prod-be885abbfcfe7b1f511e88b3003d9ee44757fbad",
		"OAI-Client-Build-Number":     "5955942",
	}
}

type openAIChatGPTWebConversationState struct {
	conversationID string
	fileIDs        []string
	sedimentIDs    []string
	message        string
}

func parseOpenAIChatGPTWebSSEState(body []byte) openAIChatGPTWebConversationState {
	state := openAIChatGPTWebConversationState{}
	forEachOpenAISSEDataPayload(string(body), func(payload []byte) {
		if !gjson.ValidBytes(payload) {
			mergeOpenAIChatGPTWebIDsFromRaw(&state, payload, false)
			return
		}
		mergeOpenAIChatGPTWebConversationID(&state, payload)
		if gjson.GetBytes(payload, "type").String() == "moderation" && gjson.GetBytes(payload, "moderation_response.blocked").Bool() {
			if state.message == "" {
				state.message = "Image generation was rejected by upstream policy."
			}
		}
		if msg := openAIChatGPTWebAssistantText(payload); msg != "" {
			state.message = msg
		}
		if openAIChatGPTWebIsImageToolEvent(payload) {
			mergeOpenAIChatGPTWebIDsFromRaw(&state, payload, true)
		}
	})
	return state
}

func parseOpenAIChatGPTWebConversationJSON(body []byte) openAIChatGPTWebConversationState {
	state := openAIChatGPTWebConversationState{}
	if !gjson.ValidBytes(body) {
		return state
	}
	mapping := gjson.GetBytes(body, "mapping")
	mapping.ForEach(func(_, node gjson.Result) bool {
		message := node.Get("message")
		if !message.Exists() {
			return true
		}
		if message.Get("author.role").String() != "tool" || message.Get("metadata.async_task_type").String() != "image_gen" {
			return true
		}
		mergeOpenAIChatGPTWebIDsFromRaw(&state, []byte(message.Raw), true)
		return true
	})
	return state
}

func mergeOpenAIChatGPTWebConversationID(state *openAIChatGPTWebConversationState, payload []byte) {
	if state == nil || state.conversationID != "" {
		return
	}
	for _, path := range []string{"conversation_id", "v.conversation_id"} {
		if value := strings.TrimSpace(gjson.GetBytes(payload, path).String()); value != "" {
			state.conversationID = value
			return
		}
	}
	if match := regexp.MustCompile(`"conversation_id"\s*:\s*"([^"]+)"`).FindSubmatch(payload); len(match) == 2 {
		state.conversationID = string(match[1])
	}
}

func mergeOpenAIChatGPTWebIDsFromRaw(state *openAIChatGPTWebConversationState, payload []byte, requireTool bool) {
	if state == nil {
		return
	}
	if !requireTool {
		mergeOpenAIChatGPTWebConversationID(state, payload)
	}
	raw := string(payload)
	for _, match := range regexp.MustCompile(`file-service://([A-Za-z0-9_-]+)`).FindAllStringSubmatch(raw, -1) {
		if len(match) == 2 {
			state.fileIDs = appendUniqueStrings(state.fileIDs, match[1])
		}
	}
	for _, match := range regexp.MustCompile(`sediment://([A-Za-z0-9_-]+)`).FindAllStringSubmatch(raw, -1) {
		if len(match) == 2 {
			state.sedimentIDs = appendUniqueStrings(state.sedimentIDs, match[1])
		}
	}
}

func openAIChatGPTWebIsImageToolEvent(payload []byte) bool {
	for _, prefix := range []string{"message", "v.message"} {
		message := gjson.GetBytes(payload, prefix)
		if !message.Exists() {
			continue
		}
		if message.Get("author.role").String() == "tool" && message.Get("metadata.async_task_type").String() == "image_gen" {
			return true
		}
	}
	return false
}

func openAIChatGPTWebAssistantText(payload []byte) string {
	for _, prefix := range []string{"message", "v.message"} {
		message := gjson.GetBytes(payload, prefix)
		if !message.Exists() || message.Get("author.role").String() != "assistant" {
			continue
		}
		parts := message.Get("content.parts")
		if !parts.IsArray() {
			continue
		}
		out := ""
		for _, part := range parts.Array() {
			if part.Type == gjson.String {
				out += part.String()
			}
		}
		if strings.TrimSpace(out) != "" {
			return strings.TrimSpace(out)
		}
	}
	if gjson.GetBytes(payload, "p").String() == "/message/content/parts/0" {
		return strings.TrimSpace(gjson.GetBytes(payload, "v").String())
	}
	return ""
}

func appendUniqueStrings(values []string, candidates ...string) []string {
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		exists := false
		for _, value := range values {
			if value == candidate {
				exists = true
				break
			}
		}
		if !exists {
			values = append(values, candidate)
		}
	}
	return values
}

func openAIChatGPTWebImageModelSlug(model string) string {
	switch strings.TrimSpace(model) {
	case "gpt-image-2":
		return "gpt-5-3"
	case "codex-gpt-image-2":
		return "codex-gpt-image-2"
	default:
		return "auto"
	}
}

func openAIChatGPTWebPromptWithSize(prompt string, size string) string {
	prompt = strings.TrimSpace(prompt)
	size = strings.TrimSpace(size)
	if size == "" {
		return prompt
	}
	return prompt + "\n\n输出图片，宽高比或尺寸为 " + size + "。"
}

func parseOpenAIChatGPTWebPowResources(html string) ([]string, string) {
	scriptRe := regexp.MustCompile(`<script[^>]+src=["']([^"']+)["']`)
	matches := scriptRe.FindAllStringSubmatch(html, -1)
	scripts := make([]string, 0, len(matches))
	dataBuild := ""
	buildRe := regexp.MustCompile(`c/[^/]*/_`)
	for _, match := range matches {
		if len(match) < 2 || strings.TrimSpace(match[1]) == "" {
			continue
		}
		src := strings.TrimSpace(match[1])
		scripts = append(scripts, src)
		if dataBuild == "" {
			dataBuild = buildRe.FindString(src)
		}
	}
	if dataBuild == "" {
		if match := regexp.MustCompile(`<html[^>]*data-build=["']([^"']*)["']`).FindStringSubmatch(html); len(match) == 2 {
			dataBuild = match[1]
		}
	}
	return scripts, dataBuild
}

func buildOpenAIChatGPTWebLegacyRequirementsToken(userAgent string, scripts []string, dataBuild string) string {
	seed := fmt.Sprintf("%d", time.Now().UnixNano())
	config := buildOpenAIChatGPTWebPowConfig(userAgent, scripts, dataBuild)
	answer, _ := generateOpenAIChatGPTWebPow(seed, "0fffff", config, 500000)
	return "gAAAAAC" + answer
}

func buildOpenAIChatGPTWebProofToken(seed string, difficulty string, userAgent string, scripts []string, dataBuild string) (string, error) {
	config := buildOpenAIChatGPTWebPowConfig(userAgent, scripts, dataBuild)
	answer, solved := generateOpenAIChatGPTWebPow(seed, difficulty, config, 500000)
	if !solved {
		return "", fmt.Errorf("failed to solve proof token")
	}
	return "gAAAAAB" + answer, nil
}

func buildOpenAIChatGPTWebPowConfig(userAgent string, scripts []string, dataBuild string) []any {
	script := openAIChatGPTWebDefaultPowScript
	if len(scripts) > 0 && strings.TrimSpace(scripts[0]) != "" {
		script = strings.TrimSpace(scripts[0])
	}
	return []any{
		3000,
		time.Now().Format("Mon Jan 02 2006 15:04:05") + " GMT-0500 (Eastern Standard Time)",
		4294705152,
		0,
		userAgent,
		script,
		dataBuild,
		"en-US",
		"en-US,es-US,en,es",
		0,
		"webdriver∭false",
		"location",
		"window",
		float64(time.Now().UnixNano()) / 1e6,
		uuid.NewString(),
		"",
		16,
		float64(time.Now().UnixNano()) / 1e6,
	}
}

func generateOpenAIChatGPTWebPow(seed string, difficulty string, config []any, limit int) (string, bool) {
	target, err := hex.DecodeString(strings.TrimSpace(difficulty))
	if err != nil || len(target) == 0 {
		return "", false
	}
	seedBytes := []byte(seed)
	for i := 0; i < limit; i++ {
		next := append([]any(nil), config...)
		if len(next) > 3 {
			next[3] = i
		}
		if len(next) > 9 {
			next[9] = i >> 1
		}
		raw, _ := json.Marshal(next)
		encoded := make([]byte, base64.StdEncoding.EncodedLen(len(raw)))
		base64.StdEncoding.Encode(encoded, raw)
		hash := sha3.Sum512(append(seedBytes, encoded...))
		if bytes.Compare(hash[:len(target)], target) <= 0 {
			return string(encoded), true
		}
	}
	fallbackSeed := make([]byte, 12)
	_, _ = rand.Read(fallbackSeed)
	raw, _ := json.Marshal(hex.EncodeToString(fallbackSeed) + seed)
	return base64.StdEncoding.EncodeToString(raw), false
}

func openAIChatGPTWebHTTPError(resp *http.Response, fallback string) error {
	if resp == nil {
		return fmt.Errorf("%s", fallback)
	}
	body := []byte(nil)
	if resp.Body != nil {
		body, _ = io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	}
	msg := strings.TrimSpace(extractUpstreamErrorMessage(body))
	if msg == "" {
		msg = fallback
	}
	return fmt.Errorf("%s: status %d", msg, resp.StatusCode)
}

func openAIChatGPTWebOutputFormat(contentType string) string {
	mediaType, _, err := mime.ParseMediaType(strings.TrimSpace(contentType))
	if err == nil {
		contentType = mediaType
	}
	switch strings.ToLower(strings.TrimSpace(contentType)) {
	case "image/jpeg", "image/jpg":
		return "jpeg"
	case "image/webp":
		return "webp"
	default:
		return "png"
	}
}

func openAIChatGPTWebExtension(contentType string) string {
	switch openAIChatGPTWebOutputFormat(contentType) {
	case "jpeg":
		return ".jpg"
	case "webp":
		return ".webp"
	default:
		return ".png"
	}
}

func logOpenAIChatGPTWebDebug(message string, fields ...zap.Field) {
	logger := zap.L()
	if logger != nil {
		logger.Debug(message, fields...)
	}
}
