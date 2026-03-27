package webui

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"

	"claude-spy/recorder"
)

// InfoResponse 是 /api/info 的响应体。
type InfoResponse struct {
	Mode     string `json:"mode"`     // "live" 或 "view"
	Filename string `json:"filename"` // 日志文件名（显示用）
}

func (s *Server) handleInfo(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(InfoResponse{
		Mode:     string(s.mode),
		Filename: s.filename,
	})
}

func (s *Server) handleRecords(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	recs := make([]recorder.Record, len(s.records))
	copy(recs, s.records)
	s.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(recs)
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
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var rec recorder.Record
		if err := json.Unmarshal(line, &rec); err != nil {
			log.Printf("webui: skip corrupt JSONL line: %v", err)
			continue
		}
		fn(rec)
	}
	return scanner.Err()
}
