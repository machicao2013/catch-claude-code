package proxy

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"claude-spy/display"
	"claude-spy/recorder"
)

// WebUIPusher 是 webui.Server 暴露给 proxy 的最小接口。
// 定义在 proxy 包内避免循环依赖（webui 导入 recorder，proxy 也导入 recorder）。
type WebUIPusher interface {
	Push(rec recorder.Record)
}

type Handler struct {
	targetURL string
	client    *http.Client
	recorder  recorder.Recorder
	printer   *display.Printer
	summary   *display.Summary
	saveSSE   bool
	webui     WebUIPusher // 可为 nil
	reqCount  atomic.Int64
}

func NewHandler(targetURL string, rec recorder.Recorder, printer *display.Printer, summary *display.Summary, saveSSE bool, webui WebUIPusher) *Handler {
	return &Handler{
		targetURL: strings.TrimRight(targetURL, "/"),
		client:    &http.Client{Timeout: 10 * time.Minute},
		recorder:  rec,
		printer:   printer,
		summary:   summary,
		saveSSE:   saveSSE,
		webui:     webui,
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	isMessages := r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/v1/messages")
	if !isMessages {
		h.forwardSimple(w, r)
		return
	}
	h.handleMessages(w, r)
}

// cleanPath extracts the API path (e.g. "/v1/messages") from possibly padded paths.
// The patched binary may produce paths like "/xxxx/v1/messages".
func cleanPath(rawPath string) string {
	if idx := strings.Index(rawPath, "/v1/"); idx >= 0 {
		return rawPath[idx:]
	}
	return rawPath
}

func (h *Handler) forwardSimple(w http.ResponseWriter, r *http.Request) {
	proxyReq, err := http.NewRequestWithContext(r.Context(), r.Method, h.targetURL+cleanPath(r.URL.Path), r.Body)
	if err != nil {
		http.Error(w, "proxy error", http.StatusBadGateway)
		return
	}
	copyHeaders(proxyReq.Header, r.Header)

	resp, err := h.client.Do(proxyReq)
	if err != nil {
		http.Error(w, "upstream error", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	copyHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

func (h *Handler) handleMessages(w http.ResponseWriter, r *http.Request) {
	reqNum := int(h.reqCount.Add(1))
	startTime := time.Now()

	reqBody, err := io.ReadAll(r.Body)
	if err != nil {
		h.printer.PrintError(fmt.Sprintf("read request body: %v", err))
		http.Error(w, "read body error", http.StatusBadGateway)
		return
	}

	h.printReqSummary(reqNum, reqBody)

	proxyReq, err := http.NewRequestWithContext(r.Context(), "POST", h.targetURL+cleanPath(r.URL.Path), bytes.NewReader(reqBody))
	if err != nil {
		http.Error(w, "proxy error", http.StatusBadGateway)
		return
	}
	copyHeaders(proxyReq.Header, r.Header)

	resp, err := h.client.Do(proxyReq)
	if err != nil {
		h.printer.PrintError(fmt.Sprintf("upstream error: %v", err))
		http.Error(w, "upstream error", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// 401 时打印详细诊断日志
	if resp.StatusCode == http.StatusUnauthorized {
		errBody, _ := io.ReadAll(resp.Body)
		h.printer.PrintError(fmt.Sprintf(
			"[401 Unauthorized] upstream=%s path=%s\n  -- Request Headers --\n%s\n  -- Response Headers --\n%s\n  -- Response Body --\n%s",
			h.targetURL,
			r.URL.Path,
			formatHeaders(proxyReq.Header),
			formatHeaders(resp.Header),
			string(errBody),
		))
		copyHeaders(w.Header(), resp.Header)
		w.WriteHeader(resp.StatusCode)
		w.Write(errBody)
		return
	}

	copyHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)

	isSSE := strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream")
	var respBody []byte

	if isSSE {
		respBody, err = h.streamSSE(w, resp.Body)
	} else {
		respBody, err = h.forwardAndCapture(w, resp.Body)
	}
	if err != nil {
		h.printer.PrintError(fmt.Sprintf("read response: %v", err))
	}

	durationMs := time.Since(startTime).Milliseconds()
	h.recordAndSummarize(reqNum, reqBody, respBody, r, resp.StatusCode, resp.Header, durationMs, isSSE)
}

func (h *Handler) streamSSE(w http.ResponseWriter, body io.Reader) ([]byte, error) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return h.forwardAndCapture(w, body)
	}

	var buf bytes.Buffer
	reader := io.TeeReader(body, &buf)

	chunk := make([]byte, 4096)
	for {
		n, err := reader.Read(chunk)
		if n > 0 {
			w.Write(chunk[:n])
			flusher.Flush()
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return buf.Bytes(), err
		}
	}
	return buf.Bytes(), nil
}

func (h *Handler) forwardAndCapture(w http.ResponseWriter, body io.Reader) ([]byte, error) {
	var buf bytes.Buffer
	reader := io.TeeReader(body, &buf)
	io.Copy(w, reader)
	return buf.Bytes(), nil
}

func (h *Handler) printReqSummary(reqNum int, reqBody []byte) {
	var parsed struct {
		Model    string            `json:"model"`
		System   json.RawMessage   `json:"system"`
		Messages []json.RawMessage `json:"messages"`
		Tools    []json.RawMessage `json:"tools"`
	}
	json.Unmarshal(reqBody, &parsed)

	msgCounts := map[string]int{}
	for _, m := range parsed.Messages {
		var msg struct {
			Role string `json:"role"`
		}
		json.Unmarshal(m, &msg)
		msgCounts[msg.Role]++
	}

	var toolNames []string
	for _, t := range parsed.Tools {
		var tool struct {
			Name string `json:"name"`
		}
		json.Unmarshal(t, &tool)
		if tool.Name != "" {
			toolNames = append(toolNames, tool.Name)
		}
	}

	h.printer.PrintRequestSummary(reqNum, display.RequestSummary{
		Model:       parsed.Model,
		SystemLen:   len(parsed.System),
		MsgCounts:   msgCounts,
		ToolNames:   toolNames,
		EstInTokens: int64(len(reqBody) / 4),
	})
}

func (h *Handler) recordAndSummarize(reqNum int, reqBody, respBody []byte, r *http.Request, status int, respHeaders http.Header, durationMs int64, isSSE bool) {
	var finalRespBody json.RawMessage
	var stopReason string
	var inTokens, outTokens, cacheCreate, cacheRead int64
	var outputDesc string

	if isSSE {
		assembled, err := ReassembleSSEResponse(respBody)
		if err == nil {
			data, _ := json.Marshal(assembled)
			finalRespBody = data
			stopReason = assembled.StopReason
			inTokens = assembled.Usage.InputTokens
			outTokens = assembled.Usage.OutputTokens
			cacheCreate = assembled.Usage.CacheCreationTokens
			cacheRead = assembled.Usage.CacheReadTokens
			outputDesc = describeContent(assembled.Content)
		} else {
			finalRespBody = respBody
		}
	} else {
		finalRespBody = respBody
		var msg struct {
			StopReason string `json:"stop_reason"`
			Usage      struct {
				// Anthropic 格式
				InputTokens  int64 `json:"input_tokens"`
				OutputTokens int64 `json:"output_tokens"`
				// OpenAI / GLM 兼容格式
				PromptTokens     int64 `json:"prompt_tokens"`
				CompletionTokens int64 `json:"completion_tokens"`
			} `json:"usage"`
		}
		json.Unmarshal(respBody, &msg)
		stopReason = msg.StopReason
		inTokens = msg.Usage.InputTokens
		if inTokens == 0 {
			inTokens = msg.Usage.PromptTokens
		}
		outTokens = msg.Usage.OutputTokens
		if outTokens == 0 {
			outTokens = msg.Usage.CompletionTokens
		}
	}

	h.printer.PrintResponseSummary(reqNum, display.ResponseSummary{
		DurationMs:  durationMs,
		OutputDesc:  outputDesc,
		StopReason:  stopReason,
		InTokens:    inTokens,
		OutTokens:   outTokens,
		CacheCreate: cacheCreate,
		CacheRead:   cacheRead,
	})

	h.summary.Add(display.RequestStats{
		InputTokens:  inTokens,
		OutputTokens: outTokens,
		CacheRead:    cacheRead,
		CacheCreate:  cacheCreate,
		DurationMs:   durationMs,
	})

	reqHeaders := map[string]string{}
	for k := range r.Header {
		reqHeaders[k] = r.Header.Get(k)
	}
	respH := map[string]string{}
	for k := range respHeaders {
		respH[k] = respHeaders.Get(k)
	}

	rec := recorder.Record{
		ID:         fmt.Sprintf("req_%03d", reqNum),
		Timestamp:  time.Now().UTC().Format(time.RFC3339Nano),
		DurationMs: durationMs,
		Request: recorder.RequestData{
			Method:  "POST",
			Path:    r.URL.Path,
			Headers: recorder.MaskHeaders(reqHeaders),
			Body:    toRawJSON(reqBody),
		},
		Response: recorder.ResponseData{
			Status:  status,
			Headers: respH,
			Body:    toRawJSON(finalRespBody),
		},
	}

	if err := h.recorder.Write(rec); err != nil {
		h.printer.PrintError(fmt.Sprintf("write record: %v", err))
	}

	if h.webui != nil {
		h.webui.Push(rec)
	}
}

func describeContent(blocks []ContentBlock) string {
	var parts []string
	for _, b := range blocks {
		switch b.Type {
		case "text":
			parts = append(parts, fmt.Sprintf("text(%d chars)", len(b.Text)))
		case "tool_use":
			parts = append(parts, fmt.Sprintf("tool_use(%s)", b.Name))
		default:
			parts = append(parts, b.Type)
		}
	}
	return strings.Join(parts, " + ")
}

func formatHeaders(h http.Header) string {
	var sb strings.Builder
	for k, vv := range h {
		// 脱敏：Authorization / x-api-key 只显示前8位
		for _, v := range vv {
			if strings.EqualFold(k, "Authorization") || strings.EqualFold(k, "x-api-key") {
				if len(v) > 8 {
					v = v[:8] + "..."
				}
			}
			sb.WriteString(fmt.Sprintf("    %s: %s\n", k, v))
		}
	}
	return sb.String()
}

func copyHeaders(dst, src http.Header) {
	for k, vv := range src {
		for _, v := range vv {
			dst.Add(k, v)
		}
	}
}

// toRawJSON 将字节转换为合法的 json.RawMessage。
// 如果是 gzip 压缩数据则先解压；如果仍非合法 JSON 则返回带错误描述的 JSON 字符串，避免 marshal 失败。
func toRawJSON(data []byte) json.RawMessage {
	// 检测 gzip 魔数 (0x1f 0x8b)
	if len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b {
		r, err := gzip.NewReader(bytes.NewReader(data))
		if err == nil {
			decompressed, err := io.ReadAll(r)
			r.Close()
			if err == nil {
				data = decompressed
			}
		}
	}
	if json.Valid(data) {
		return json.RawMessage(data)
	}
	// 非 JSON 内容（如二进制）存为描述字符串，保证整条记录可以正常 marshal
	msg, _ := json.Marshal(fmt.Sprintf("<non-json body, %d bytes>", len(data)))
	return json.RawMessage(msg)
}
