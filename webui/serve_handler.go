package webui

import (
	"claude-spy/recorder"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// FileInfo is one entry in the /api/files response.
type FileInfo struct {
	Filename string `json:"filename"`
	Size     int64  `json:"size"`
	Mtime    string `json:"mtime"` // RFC3339
}

// registerServeRoutes registers serve-mode routes.
func (s *Server) registerServeRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/files", s.handleFiles)
	mux.HandleFunc("/api/records", s.handleServeRecords)
	mux.Handle("/static/", http.FileServer(http.FS(staticFS)))
	mux.HandleFunc("/", s.handleServeRoot)
}

// handleFiles returns a JSON list of .jsonl files in logDir, sorted by mtime desc, max 100.
func (s *Server) handleFiles(w http.ResponseWriter, r *http.Request) {
	entries, err := os.ReadDir(s.logDir)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("[]"))
		return
	}

	type entry struct {
		info FileInfo
		mod  time.Time
	}
	var result []entry
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		result = append(result, entry{
			info: FileInfo{
				Filename: e.Name(),
				Size:     fi.Size(),
				Mtime:    fi.ModTime().Format(time.RFC3339),
			},
			mod: fi.ModTime(),
		})
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].mod.After(result[j].mod)
	})
	if len(result) > 100 {
		result = result[:100]
	}

	files := make([]FileInfo, len(result))
	for i, e := range result {
		files[i] = e.info
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(files)
}

// handleServeRecords handles GET /api/records?file=<filename>
func (s *Server) handleServeRecords(w http.ResponseWriter, r *http.Request) {
	filename := r.URL.Query().Get("file")
	if filename == "" || !strings.HasSuffix(filename, ".jsonl") ||
		strings.ContainsAny(filename, "/\\") {
		http.Error(w, "invalid file param", http.StatusBadRequest)
		return
	}

	path := filepath.Join(s.logDir, filename)
	var recs []recorder.Record
	err := loadJSONLFile(path, func(rec recorder.Record) {
		recs = append(recs, rec)
	})
	if err != nil {
		http.Error(w, fmt.Sprintf("file not found: %s", filename), http.StatusNotFound)
		return
	}
	if recs == nil {
		recs = []recorder.Record{}
	}

	w.Header().Set("Content-Type", "application/json")
	data, _ := json.Marshal(recs)
	gzipWrite(w, r, data)
}

// handleServeRoot routes / and /:filename
func (s *Server) handleServeRoot(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/")

	if path == "" {
		s.renderListPage(w, r, "")
		return
	}

	if !strings.HasSuffix(path, ".jsonl") || strings.ContainsAny(path, "/\\") {
		http.NotFound(w, r)
		return
	}

	fullPath := filepath.Join(s.logDir, path)
	if _, err := os.Stat(fullPath); os.IsNotExist(err) {
		s.renderListPage(w, r, path)
		return
	}

	s.renderViewerPage(w, r, path)
}

// renderListPage renders the file-list HTML page.
// If errFile is non-empty, an error notice is shown at the top.
func (s *Server) renderListPage(w http.ResponseWriter, r *http.Request, errFile string) {
	data, err := staticFS.ReadFile("static/list.html")
	if err != nil {
		http.Error(w, "list.html not found", 500)
		return
	}

	ver := fmt.Sprintf("%d", time.Now().UnixMilli())
	html := strings.ReplaceAll(string(data), "?v=1", "?v="+ver)

	if errFile != "" {
		notice := fmt.Sprintf(
			`<div id="error-notice" class="error-notice">文件 <code>%s</code> 不存在</div>`,
			errFile,
		)
		html = strings.Replace(html, `<div id="error-notice"></div>`, notice, 1)
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Write([]byte(html))
}

// renderViewerPage renders the viewer page with a back-link injected.
func (s *Server) renderViewerPage(w http.ResponseWriter, r *http.Request, filename string) {
	data, err := staticFS.ReadFile("static/index.html")
	if err != nil {
		http.Error(w, "index.html not found", 500)
		return
	}

	ver := fmt.Sprintf("%d", time.Now().UnixMilli())
	html := strings.ReplaceAll(string(data), "?v=2", "?v="+ver)

	backLink := `<a href="/" class="back-link">← 返回列表</a>`
	html = strings.Replace(html,
		`<span class="logo">claude-spy</span>`,
		`<span class="logo">claude-spy</span>`+backLink,
		1,
	)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Write([]byte(html))
}
