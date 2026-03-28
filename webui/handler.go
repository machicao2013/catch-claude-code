package webui

import (
	"bufio"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"

	"claude-spy/recorder"
)

// InfoResponse 是 /api/info 的响应体。
type InfoResponse struct {
	Mode     string `json:"mode"`     // "live" 或 "view"
	Filename string `json:"filename"` // 日志文件名（显示用）
	Total    int    `json:"total"`    // 总记录数
}

// RecordSummary 是记录的摘要信息（不含完整 request/response body）。
type RecordSummary struct {
	ID          string `json:"id"`
	Timestamp   string `json:"timestamp"`
	DurationMs  int64  `json:"duration_ms"`
	Model       string `json:"model"`
	MsgCount    int    `json:"msg_count"`
	SysLen      int    `json:"sys_len"`
	StopReason  string `json:"stop_reason"`
	InTokens    int64  `json:"in_tokens"`
	OutTokens   int64  `json:"out_tokens"`
	CacheRead   int64  `json:"cache_read"`
	CacheCreate int64  `json:"cache_create"`
}

func extractSummary(rec recorder.Record) RecordSummary {
	s := RecordSummary{
		ID:         rec.ID,
		Timestamp:  rec.Timestamp,
		DurationMs: rec.DurationMs,
	}

	// 从 request body 提取 model、message count、system prompt 长度
	var reqBody struct {
		Model    string            `json:"model"`
		Messages []json.RawMessage `json:"messages"`
		System   json.RawMessage   `json:"system"`
	}
	json.Unmarshal(rec.Request.Body, &reqBody)
	s.Model = reqBody.Model
	s.MsgCount = len(reqBody.Messages)
	s.SysLen = len(reqBody.System)

	// 从 response body 提取 usage 和 stop_reason
	var respBody struct {
		StopReason string `json:"stop_reason"`
		Usage      struct {
			InputTokens  int64 `json:"input_tokens"`
			OutputTokens int64 `json:"output_tokens"`
			CacheRead    int64 `json:"cache_read_input_tokens"`
			CacheCreate  int64 `json:"cache_creation_input_tokens"`
		} `json:"usage"`
	}
	json.Unmarshal(rec.Response.Body, &respBody)
	s.StopReason = respBody.StopReason
	s.InTokens = respBody.Usage.InputTokens
	s.OutTokens = respBody.Usage.OutputTokens
	s.CacheRead = respBody.Usage.CacheRead
	s.CacheCreate = respBody.Usage.CacheCreate

	return s
}

// gzipResponseWriter wraps http.ResponseWriter with gzip compression.
func gzipWrite(w http.ResponseWriter, r *http.Request, data []byte) {
	if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		w.Write(data)
		return
	}
	w.Header().Set("Content-Encoding", "gzip")
	gz := gzip.NewWriter(w)
	gz.Write(data)
	gz.Close()
}

func (s *Server) handleInfo(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	total := len(s.records)
	s.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(InfoResponse{
		Mode:     string(s.mode),
		Filename: s.filename,
		Total:    total,
	})
}

// handleRecordsList 返回所有记录的摘要列表（轻量，几十 KB）。
// GET /api/records
func (s *Server) handleRecordsList(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	summaries := make([]RecordSummary, len(s.records))
	for i, rec := range s.records {
		summaries[i] = extractSummary(rec)
	}
	s.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	data, _ := json.Marshal(summaries)
	gzipWrite(w, r, data)
}

// handleRecordDetail 返回单条记录的完整数据。
// GET /api/records/<id>
func (s *Server) handleRecordDetail(w http.ResponseWriter, r *http.Request) {
	// 从 URL 路径提取 ID: /api/records/req_001
	path := r.URL.Path
	id := strings.TrimPrefix(path, "/api/records/")
	if id == "" || id == path {
		http.Error(w, "missing record id", http.StatusBadRequest)
		return
	}

	s.mu.RLock()
	var found *recorder.Record
	for i := range s.records {
		if s.records[i].ID == id {
			rec := s.records[i]
			found = &rec
			break
		}
	}
	s.mu.RUnlock()

	if found == nil {
		http.Error(w, "record not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	data, _ := json.Marshal(found)
	gzipWrite(w, r, data)
}

// handleRecords 根据路径分发到列表或详情。
func (s *Server) handleRecords(w http.ResponseWriter, r *http.Request) {
	// /api/records → 列表（摘要）
	// /api/records/req_001 → 详情（完整）
	path := strings.TrimPrefix(r.URL.Path, "/api/records")
	if path == "" || path == "/" {
		s.handleRecordsList(w, r)
	} else {
		s.handleRecordDetail(w, r)
	}
}

func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	// 发送初始注释防止代理超时
	io.WriteString(w, ": connected\n\n")
	flusher.Flush()

	ch := s.subscribe()
	defer s.unsubscribe(ch)

	for {
		select {
		case <-r.Context().Done():
			return
		case msg := <-ch:
			w.Write(msg)
			flusher.Flush()
		}
	}
}

// loadJSONLFile 逐行读取 JSONL 文件，将每条记录传给回调。
func loadJSONLFile(path string, fn func(recorder.Record)) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open jsonl file: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 64*1024*1024)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var rec recorder.Record
		if err := json.Unmarshal(line, &rec); err != nil {
			log.Printf("webui: skip corrupt JSONL line %d: %v", lineNum, err)
			continue
		}
		fn(rec)
	}
	return scanner.Err()
}

// byIndex 按索引查找记录（SSE 推送时使用）。
func (s *Server) recordByIndex(idx int) (recorder.Record, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if idx < 0 || idx >= len(s.records) {
		return recorder.Record{}, false
	}
	return s.records[idx], true
}

// recordCount 返回当前记录数。
func (s *Server) recordCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.records)
}

// parseIntParam 从 query string 中读取 int 参数。
func parseIntParam(r *http.Request, name string, defaultVal int) int {
	v := r.URL.Query().Get(name)
	if v == "" {
		return defaultVal
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return defaultVal
	}
	return n
}
