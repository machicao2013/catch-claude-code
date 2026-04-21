package webui

import (
	"claude-spy/recorder"
	"encoding/json"
	"fmt"
	"html"
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
	mux.HandleFunc("/api/info", s.handleServeInfo)
	mux.HandleFunc("/api/records/", s.handleServeRecordDetail)
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

// handleServeInfo handles /api/info in serve mode.
// Returns mode="view" and filename from the ?file= query param.
func (s *Server) handleServeInfo(w http.ResponseWriter, r *http.Request) {
	filename := r.URL.Query().Get("file")
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(InfoResponse{
		Mode:     "view",
		Filename: filename,
		Total:    0,
	})
}

// handleServeRecords handles GET /api/records?file=<filename>
func (s *Server) handleServeRecords(w http.ResponseWriter, r *http.Request) {
	filename := r.URL.Query().Get("file")
	if filename == "" || !strings.HasSuffix(filename, ".jsonl") {
		http.Error(w, "invalid file param", http.StatusBadRequest)
		return
	}
	absPath := filepath.Join(s.logDir, filename)
	rel, err := filepath.Rel(s.logDir, absPath)
	if err != nil || strings.HasPrefix(rel, "..") {
		http.Error(w, "invalid file param", http.StatusBadRequest)
		return
	}
	var recs []recorder.Record
	loadErr := loadJSONLFile(absPath, func(rec recorder.Record) {
		recs = append(recs, rec)
	})
	if loadErr != nil {
		http.Error(w, fmt.Sprintf("file not found: %s", filename), http.StatusNotFound)
		return
	}

	summaries := make([]RecordSummary, len(recs))
	for i, rec := range recs {
		summaries[i] = extractSummary(rec)
	}

	w.Header().Set("Content-Type", "application/json")
	data, _ := json.Marshal(summaries)
	gzipWrite(w, r, data)
}

// handleServeRecordDetail handles GET /api/records/<id>?file=<filename> in serve mode.
func (s *Server) handleServeRecordDetail(w http.ResponseWriter, r *http.Request) {
	filename := r.URL.Query().Get("file")
	if filename == "" || !strings.HasSuffix(filename, ".jsonl") {
		http.Error(w, "invalid file param", http.StatusBadRequest)
		return
	}
	absPath := filepath.Join(s.logDir, filename)
	rel, err := filepath.Rel(s.logDir, absPath)
	if err != nil || strings.HasPrefix(rel, "..") {
		http.Error(w, "invalid file param", http.StatusBadRequest)
		return
	}

	id := strings.TrimPrefix(r.URL.Path, "/api/records/")
	if id == "" {
		http.Error(w, "missing record id", http.StatusBadRequest)
		return
	}

	var found *recorder.Record
	loadJSONLFile(absPath, func(rec recorder.Record) {
		if found == nil && rec.ID == id {
			cp := rec
			found = &cp
		}
	})
	if found == nil {
		http.Error(w, "record not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	data, _ := json.Marshal(found)
	gzipWrite(w, r, data)
}

// handleServeRoot routes / and /:filename
func (s *Server) handleServeRoot(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/")

	if path == "" {
		s.renderListPage(w, r, "")
		return
	}

	if !strings.HasSuffix(path, ".jsonl") {
		http.NotFound(w, r)
		return
	}
	fullPath := filepath.Join(s.logDir, path)
	rel, relErr := filepath.Rel(s.logDir, fullPath)
	if relErr != nil || strings.HasPrefix(rel, "..") {
		http.NotFound(w, r)
		return
	}
	if _, statErr := os.Stat(fullPath); statErr != nil {
		// treat any stat error (not-exist, permission, etc.) as not-found
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
	page := strings.ReplaceAll(string(data), "?v=1", "?v="+ver)

	if errFile != "" {
		notice := fmt.Sprintf(
			`<div id="error-notice" class="error-notice">文件 <code>%s</code> 不存在</div>`,
			html.EscapeString(errFile),
		)
		page = strings.Replace(page, `<div id="error-notice"></div>`, notice, 1)
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Write([]byte(page))
}

// renderViewerPage renders the viewer page for a specific file.
// Injects:
//   - a "← 返回列表" back-link into the topbar
//   - a JS snippet that patches window.fetch so app.js API calls
//     include the correct ?file= query param
func (s *Server) renderViewerPage(w http.ResponseWriter, r *http.Request, filename string) {
	data, err := staticFS.ReadFile("static/index.html")
	if err != nil {
		http.Error(w, "index.html not found", 500)
		return
	}

	ver := fmt.Sprintf("%d", time.Now().UnixMilli())
	page := strings.ReplaceAll(string(data), "?v=2", "?v="+ver)

	// Inject back-link after the logo span
	backLink := `<a href="./" class="back-link">← 返回列表</a>`
	page = strings.Replace(page,
		`<span class="logo">claude-spy</span>`,
		`<span class="logo">claude-spy</span>`+backLink,
		1,
	)

	// Inject fetch-patching script so app.js API calls include ?file=<filename>
	serveScript := fmt.Sprintf(`<script>
window.__SERVE_FILE__ = %q;
const _origFetch = window.fetch;
window.fetch = function(url, opts) {
  if (url === 'api/records') return _origFetch('api/records?file=' + encodeURIComponent(window.__SERVE_FILE__), opts);
  if (url === 'api/info')    return _origFetch('api/info?file='    + encodeURIComponent(window.__SERVE_FILE__), opts);
  if (url.startsWith('api/records/')) return _origFetch(url + '?file=' + encodeURIComponent(window.__SERVE_FILE__), opts);
  return _origFetch(url, opts);
};
</script>`, filename)

	page = strings.Replace(page, `<script src="static/app.js`, serveScript+`<script src="static/app.js`, 1)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Write([]byte(page))
}
