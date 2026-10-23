package handler

import (
	"bytes"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/modelresponse"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const requestedResponseModelKey = "_gateway_requested_response_model"

// requestedModelWriter is installed at the shared gateway boundary, covering
// raw passthroughs, protocol conversions and channel/account mapping alike.
// It never reads the request body, so multipart and native Gemini routes keep
// their normal parsing. The handler records the public name before mapping.
type requestedModelWriter struct {
	gin.ResponseWriter
	c       *gin.Context
	model   string
	mode    string
	pending []byte
	err     error
}

func (w *requestedModelWriter) prepare() {
	if w.mode != "" {
		return
	}
	w.model = w.c.GetString(requestedResponseModelKey)
	if w.model == "" || w.Status() < 200 || w.Status() >= 300 || w.Header().Get("Content-Encoding") != "" {
		w.mode = "pass"
		return
	}
	contentType := strings.ToLower(strings.TrimSpace(strings.SplitN(w.Header().Get("Content-Type"), ";", 2)[0]))
	switch {
	case contentType == "text/event-stream":
		w.mode = "sse"
	case contentType == "application/json" || strings.HasSuffix(contentType, "+json"):
		w.mode = "json"
	default:
		w.mode = "pass"
	}
	if w.mode != "pass" {
		// Mapping may change the length. Remove upstream entity metadata before
		// headers are committed, including explicit WriteHeaderNow/Flush calls.
		w.Header().Del("Content-Length")
		w.Header().Del("ETag")
		w.Header().Del("Content-MD5")
	}
}

func (w *requestedModelWriter) WriteHeaderNow() {
	w.prepare()
	w.ResponseWriter.WriteHeaderNow()
}

func (w *requestedModelWriter) Write(p []byte) (int, error) {
	w.prepare()
	if w.mode == "pass" {
		return w.ResponseWriter.Write(p)
	}
	if w.err != nil {
		return 0, w.err
	}
	// Preserve gin's Written semantics even while a fragmented JSON document
	// or SSE event is waiting for its remaining bytes.
	w.ResponseWriter.WriteHeaderNow()
	if w.mode == "json" && len(w.pending) == 0 && len(bytes.TrimSpace(p)) == 0 {
		// Image generation uses JSON whitespace keepalives while awaiting its
		// result. Forward them immediately so idle proxies stay connected.
		return w.ResponseWriter.Write(p)
	}
	if w.mode == "json" && len(w.pending) == 0 && gjson.ValidBytes(p) {
		_, w.err = w.ResponseWriter.Write(modelresponse.Rewrite(p, w.model))
	} else {
		w.pending = append(w.pending, p...)
		if w.mode == "json" {
			if gjson.ValidBytes(w.pending) {
				_, w.err = w.ResponseWriter.Write(modelresponse.Rewrite(w.pending, w.model))
				w.pending = nil
			}
		} else {
			w.writeSSEEvents()
		}
	}
	if w.err != nil {
		return 0, w.err
	}
	return len(p), nil
}

func (w *requestedModelWriter) WriteString(s string) (int, error) {
	return w.Write([]byte(s))
}

func (w *requestedModelWriter) Flush() {
	w.WriteHeaderNow()
	w.ResponseWriter.Flush()
}

func (w *requestedModelWriter) writeSSEEvents() {
	// Recognize both LF and CRLF, including delimiters split across writes.
	for w.err == nil {
		end := bytes.Index(w.pending, []byte("\n\n"))
		width := 2
		if crlf := bytes.Index(w.pending, []byte("\r\n\r\n")); crlf >= 0 && (end < 0 || crlf < end) {
			end, width = crlf, 4
		}
		if end < 0 {
			return
		}
		end += width
		_, w.err = w.ResponseWriter.Write(modelresponse.RewriteSSE(w.pending[:end], w.model))
		w.pending = w.pending[end:]
		if len(w.pending) == 0 {
			w.pending = nil
		}
	}
}

func (w *requestedModelWriter) finish() {
	if len(w.pending) == 0 || w.err != nil {
		return
	}
	payload := w.pending
	if w.mode == "sse" {
		payload = modelresponse.RewriteSSE(payload, w.model)
	} else {
		payload = modelresponse.Rewrite(payload, w.model)
	}
	_, w.err = w.ResponseWriter.Write(payload)
	w.pending = nil
}

func withRequestedModelResponse(c *gin.Context) {
	// GET includes model catalogues, file downloads and native WS upgrades.
	// WS frames use the same rewrite helper at their own client boundary.
	if c.Request == nil || c.Request.Method != http.MethodPost {
		c.Next()
		return
	}
	original := c.Writer
	w := &requestedModelWriter{ResponseWriter: original, c: c}
	c.Writer = w
	defer func() {
		w.finish()
		c.Writer = original
	}()
	c.Next()
}
