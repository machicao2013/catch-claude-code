# claude-spy serve Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a `claude-spy serve` subcommand that starts a persistent web server serving all JSONL log files from `~/.claude-spy/logs/` — browsable via file-list page and viewable by URL path.

**Architecture:** New `ModeServe` added to `webui.Server`; serve mode registers additional routes (`/api/files`, `/:filename`) and a new file-list HTML page embedded alongside `index.html`. The viewer page (`/:filename`) reuses existing `index.html` + `app.js` with a "← 返回列表" back-link injected server-side. No caching — every request reads from disk.

**Tech Stack:** Go 1.21+, `net/http`, `embed`, existing `webui` and `recorder` packages.

---

## File Map

| File | Action | Responsibility |
|------|--------|----------------|
| `webui/server.go` | Modify | Add `ModeServe`, `logDir` field, `NewServeServer()` constructor, serve-mode route registration |
| `webui/serve_handler.go` | Create | `/api/files`, `/:filename`, `/` serve-mode handlers |
| `webui/static/list.html` | Create | File-list page HTML (reuses style.css) |
| `main.go` | Modify | Add `runServe()` function and `serve` subcommand dispatch |

---

## Task 1: Add `ModeServe` and `NewServeServer` constructor

**Files:**
- Modify: `webui/server.go`

- [ ] **Step 1: Add `ModeServe` constant and `logDir` field**

Open `webui/server.go`. After `ModeView Mode = "view"` add:

```go
ModeServe Mode = "serve"
```

In the `Server` struct, after the `filename string` field add:

```go
logDir string // serve 模式：日志根目录
```

- [ ] **Step 2: Add `NewServeServer` constructor**

After the existing `NewServer` function, add:

```go
// NewServeServer 创建 serve 模式的 Server。
// port=0 时自动选择空闲端口。
func NewServeServer(logDir string, port int) (*Server, error) {
	addr := fmt.Sprintf("0.0.0.0:%d", port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("webui listen on %s: %w", addr, err)
	}
	s := &Server{
		mode:     ModeServe,
		logDir:   logDir,
		subs:     make(map[chan []byte]struct{}),
		listener: ln,
		port:     ln.Addr().(*net.TCPAddr).Port,
	}
	mux := http.NewServeMux()
	s.registerServeRoutes(mux)
	s.httpServer = &http.Server{Handler: mux}
	return s, nil
}
```

- [ ] **Step 3: Build to verify no compile errors**

```bash
cd /data/home/jerryma/sourceCode/person_skills/catch_tools
go build ./...
```

Expected: no output (success).

- [ ] **Step 4: Commit**

```bash
git add webui/server.go
git commit -m "feat(webui): add ModeServe constant, logDir field, NewServeServer constructor"
```

---

## Task 2: Create `webui/serve_handler.go` with all serve-mode handlers

**Files:**
- Create: `webui/serve_handler.go`

- [ ] **Step 1: Write the failing test first**

Create `webui/serve_handler_test.go`:

```go
package webui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestHandleFiles_ReturnsJSON(t *testing.T) {
	dir := t.TempDir()
	// 写两个假 JSONL 文件
	f1 := filepath.Join(dir, "20240101_120000_aaaa.jsonl")
	f2 := filepath.Join(dir, "20240102_130000_bbbb.jsonl")
	os.WriteFile(f1, []byte(`{}`), 0644)
	time.Sleep(10 * time.Millisecond) // 确保 mtime 不同
	os.WriteFile(f2, []byte(`{}`), 0644)

	s := &Server{mode: ModeServe, logDir: dir, subs: make(map[chan []byte]struct{})}

	req := httptest.NewRequest("GET", "/api/files", nil)
	w := httptest.NewRecorder()
	s.handleFiles(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var files []FileInfo
	if err := json.NewDecoder(w.Body).Decode(&files); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(files))
	}
	// 按 mtime 倒序，最新的在前
	if files[0].Filename != "20240102_130000_bbbb.jsonl" {
		t.Errorf("expected newest first, got %s", files[0].Filename)
	}
}

func TestHandleFiles_LimitTo100(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 150; i++ {
		name := filepath.Join(dir, fmt.Sprintf("2024010%d_120000_%04d.jsonl", i%9+1, i))
		os.WriteFile(name, []byte(`{}`), 0644)
	}
	s := &Server{mode: ModeServe, logDir: dir, subs: make(map[chan []byte]struct{})}
	req := httptest.NewRequest("GET", "/api/files", nil)
	w := httptest.NewRecorder()
	s.handleFiles(w, req)

	var files []FileInfo
	json.NewDecoder(w.Body).Decode(&files)
	if len(files) != 100 {
		t.Errorf("expected 100 files (cap), got %d", len(files))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd /data/home/jerryma/sourceCode/person_skills/catch_tools
go test ./webui/ -run "TestHandleFiles" -v
```

Expected: FAIL — `s.handleFiles undefined`, `FileInfo undefined`.

- [ ] **Step 3: Create `webui/serve_handler.go`**

```go
package webui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// FileInfo は /api/files の 1 エントリ。
type FileInfo struct {
	Filename string `json:"filename"`
	Size     int64  `json:"size"`
	Mtime    string `json:"mtime"` // RFC3339
}

// registerServeRoutes は serve モード専用のルートを登録する。
func (s *Server) registerServeRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/files", s.handleFiles)
	mux.HandleFunc("/api/records", s.handleServeRecords)
	mux.Handle("/static/", http.FileServer(http.FS(staticFS)))
	mux.HandleFunc("/", s.handleServeRoot)
}

// handleFiles は ~/.claude-spy/logs/ のファイルリストを返す。
// GET /api/files
func (s *Server) handleFiles(w http.ResponseWriter, r *http.Request) {
	entries, err := os.ReadDir(s.logDir)
	if err != nil {
		// ディレクトリが存在しない場合は空リストを返す
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

	// 修改時刻の降順でソート
	sort.Slice(result, func(i, j int) bool {
		return result[i].mod.After(result[j].mod)
	})

	// 最大 100 件
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

// handleServeRecords はファイル名を query param で受け取り records を返す。
// GET /api/records?file=<filename>
func (s *Server) handleServeRecords(w http.ResponseWriter, r *http.Request) {
	filename := r.URL.Query().Get("file")
	if filename == "" || !strings.HasSuffix(filename, ".jsonl") ||
		strings.ContainsAny(filename, "/\\") {
		http.Error(w, "invalid file param", http.StatusBadRequest)
		return
	}

	path := filepath.Join(s.logDir, filename)
	var records []interface{}
	err := loadJSONLFile(path, func(rec interface{}) {
		records = append(records, rec)
	})
	if err != nil {
		http.Error(w, fmt.Sprintf("file not found: %s", filename), http.StatusNotFound)
		return
	}
	if records == nil {
		records = []interface{}{}
	}

	w.Header().Set("Content-Type", "application/json")
	data, _ := json.Marshal(records)
	gzipWrite(w, r, data)
}

// handleServeRoot は / と /:filename を処理する。
func (s *Server) handleServeRoot(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/")

	// ルート: ファイルリストページを表示
	if path == "" {
		s.renderListPage(w, r, "")
		return
	}

	// ファイル名のバリデーション
	if !strings.HasSuffix(path, ".jsonl") || strings.ContainsAny(path, "/\\") {
		http.NotFound(w, r)
		return
	}

	// ファイルの存在確認
	fullPath := filepath.Join(s.logDir, path)
	if _, err := os.Stat(fullPath); os.IsNotExist(err) {
		s.renderListPage(w, r, path)
		return
	}

	// viewer ページを返す（index.html に戻るリンクを注入）
	s.renderViewerPage(w, r, path)
}

// renderListPage はファイルリスト HTML ページを返す。
// errFile が空でない場合、ページ上部にエラー通知を表示する。
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

// renderViewerPage は既存の index.html に戻るリンクを注入して返す。
func (s *Server) renderViewerPage(w http.ResponseWriter, r *http.Request, filename string) {
	data, err := staticFS.ReadFile("static/index.html")
	if err != nil {
		http.Error(w, "index.html not found", 500)
		return
	}

	ver := fmt.Sprintf("%d", time.Now().UnixMilli())
	html := strings.ReplaceAll(string(data), "?v=2", "?v="+ver)

	// ロゴの直後に「← 返回列表」リンクを注入
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
```

- [ ] **Step 4: Fix `loadJSONLFile` call — it uses `recorder.Record`, not `interface{}`**

`loadJSONLFile` in `handler.go` already accepts `func(recorder.Record)`. Update `handleServeRecords` to use the correct type:

Replace the `handleServeRecords` body's load section with:

```go
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
```

Also add `"claude-spy/recorder"` to the imports.

- [ ] **Step 5: Run tests**

```bash
cd /data/home/jerryma/sourceCode/person_skills/catch_tools
go test ./webui/ -run "TestHandleFiles" -v
```

Expected: both `TestHandleFiles_ReturnsJSON` and `TestHandleFiles_LimitTo100` PASS.

- [ ] **Step 6: Build**

```bash
go build ./...
```

Expected: no output.

- [ ] **Step 7: Commit**

```bash
git add webui/serve_handler.go webui/serve_handler_test.go
git commit -m "feat(webui): add serve-mode handlers (files list, records, root routing)"
```

---

## Task 3: Create `webui/static/list.html` file-list page

**Files:**
- Create: `webui/static/list.html`

- [ ] **Step 1: Create list.html**

```html
<!DOCTYPE html>
<html lang="zh-CN" data-theme="obsidian">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>claude-spy — 日志列表</title>
  <link rel="preconnect" href="https://fonts.googleapis.com">
  <link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
  <link href="https://fonts.googleapis.com/css2?family=JetBrains+Mono:wght@400;500;700;800&display=swap" rel="stylesheet">
  <link rel="stylesheet" href="static/style.css?v=1">
  <script>
    (function(){
      var t = localStorage.getItem('claude-spy-theme');
      if (t && ['obsidian','daylight','claude','phosphor'].includes(t)) {
        document.documentElement.setAttribute('data-theme', t);
      }
    })();
  </script>
  <style>
    .file-table { width: 100%; border-collapse: collapse; margin-top: 1rem; }
    .file-table th, .file-table td { padding: 0.6rem 1rem; text-align: left; border-bottom: 1px solid var(--border); }
    .file-table th { color: var(--text-muted); font-size: 0.8rem; text-transform: uppercase; letter-spacing: 0.05em; }
    .file-table tr:hover td { background: var(--bg-hover); cursor: pointer; }
    .file-table a { color: var(--text-primary); text-decoration: none; }
    .file-table a:hover { color: var(--accent); }
    .file-size { color: var(--text-muted); font-size: 0.85rem; }
    .file-time { color: var(--text-muted); font-size: 0.85rem; white-space: nowrap; }
    .error-notice { background: var(--error-bg, #3a1a1a); color: var(--error-text, #f87171);
      border: 1px solid var(--error-border, #7f1d1d); border-radius: 6px;
      padding: 0.6rem 1rem; margin-bottom: 1rem; font-size: 0.9rem; }
    .empty-state { text-align: center; padding: 4rem; color: var(--text-muted); }
    .list-header { display: flex; align-items: center; justify-content: space-between; margin-bottom: 0.5rem; }
    .list-title { font-size: 1rem; color: var(--text-muted); }
  </style>
</head>
<body>
  <header id="topbar">
    <div class="topbar-left">
      <span class="logo">claude-spy</span>
      <span class="filename">日志列表</span>
    </div>
    <div class="topbar-center"></div>
    <div class="topbar-right">
      <button id="theme-toggle" class="theme-btn" title="切换主题 (T)">◐</button>
    </div>
  </header>

  <main style="padding: 1.5rem 2rem; max-width: 960px; margin: 0 auto;">
    <div id="error-notice"></div>
    <div class="list-header">
      <span class="list-title" id="file-count"></span>
    </div>
    <div id="file-list"></div>
  </main>

  <script>
    // Theme
    const THEMES = ['obsidian', 'daylight', 'claude', 'phosphor'];
    const THEME_ICONS = { obsidian: '🌙', daylight: '☀️', claude: '🧡', phosphor: '⚡' };
    function cycleTheme() {
      const cur = document.documentElement.getAttribute('data-theme') || 'obsidian';
      const next = THEMES[(THEMES.indexOf(cur) + 1) % THEMES.length];
      document.documentElement.setAttribute('data-theme', next);
      localStorage.setItem('claude-spy-theme', next);
      document.getElementById('theme-toggle').textContent = THEME_ICONS[next] || '◐';
    }
    document.getElementById('theme-toggle').addEventListener('click', cycleTheme);
    // 初期アイコンを設定
    (function() {
      const t = document.documentElement.getAttribute('data-theme') || 'obsidian';
      document.getElementById('theme-toggle').textContent = THEME_ICONS[t] || '◐';
    })();

    // ファイル名から読みやすい日時を生成: 20240421_123456_abcd.jsonl → 2024-04-21 12:34:56
    function parseFilename(name) {
      const m = name.match(/^(\d{4})(\d{2})(\d{2})_(\d{2})(\d{2})(\d{2})/);
      if (!m) return name;
      return `${m[1]}-${m[2]}-${m[3]} ${m[4]}:${m[5]}:${m[6]}`;
    }

    function fmtSize(bytes) {
      if (bytes < 1024) return bytes + ' B';
      if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + ' KB';
      return (bytes / 1024 / 1024).toFixed(1) + ' MB';
    }

    async function loadFiles() {
      const resp = await fetch('api/files');
      if (!resp.ok) throw new Error('api/files failed');
      const files = await resp.json();
      const container = document.getElementById('file-list');
      const countEl = document.getElementById('file-count');

      if (!files || files.length === 0) {
        container.innerHTML = '<div class="empty-state">暂无日志文件</div>';
        countEl.textContent = '';
        return;
      }

      countEl.textContent = `共 ${files.length} 个文件`;

      const table = document.createElement('table');
      table.className = 'file-table';
      table.innerHTML = `
        <thead><tr>
          <th>时间</th>
          <th>文件名</th>
          <th>大小</th>
        </tr></thead>
        <tbody id="file-tbody"></tbody>`;
      container.appendChild(table);

      const tbody = table.querySelector('#file-tbody');
      for (const f of files) {
        const tr = document.createElement('tr');
        tr.innerHTML = `
          <td class="file-time">${parseFilename(f.filename)}</td>
          <td><a href="/${encodeURIComponent(f.filename)}">${f.filename}</a></td>
          <td class="file-size">${fmtSize(f.size)}</td>`;
        tr.addEventListener('click', () => { location.href = '/' + encodeURIComponent(f.filename); });
        tbody.appendChild(tr);
      }
    }

    loadFiles().catch(e => {
      document.getElementById('file-list').innerHTML =
        `<div class="empty-state">加载失败: ${e.message}</div>`;
    });
  </script>
</body>
</html>
```

- [ ] **Step 2: Add `back-link` style to `style.css`**

Open `webui/static/style.css`. At the end of the file, append:

```css
/* serve mode — back link in topbar */
.back-link {
  font-size: 0.8rem;
  color: var(--text-muted);
  text-decoration: none;
  margin-left: 0.75rem;
  padding: 2px 8px;
  border: 1px solid var(--border);
  border-radius: 4px;
  transition: color 0.15s, border-color 0.15s;
}
.back-link:hover {
  color: var(--accent);
  border-color: var(--accent);
}
```

- [ ] **Step 3: Build**

```bash
cd /data/home/jerryma/sourceCode/person_skills/catch_tools
go build ./...
```

Expected: no output.

- [ ] **Step 4: Commit**

```bash
git add webui/static/list.html webui/static/style.css
git commit -m "feat(webui): add file-list page (list.html) and back-link style"
```

---

## Task 4: Add viewer page support — `/api/info` and `/api/records` in serve mode

**Files:**
- Modify: `webui/serve_handler.go`

The existing `app.js` calls `api/info` and `api/records` on load. In serve mode, `api/records` must accept `?file=<filename>` (already implemented in Task 2). But `api/info` also needs to work and return `mode: "view"` so the existing badge logic shows "回顾".

We also need `app.js` to know which file to fetch. The approach: inject the filename into the viewer page as a JS global variable.

- [ ] **Step 1: Write failing test for `handleServeInfo`**

Add to `webui/serve_handler_test.go`:

```go
func TestHandleServeInfo(t *testing.T) {
	s := &Server{mode: ModeServe, logDir: "/tmp", subs: make(map[chan []byte]struct{})}
	req := httptest.NewRequest("GET", "/api/info?file=test.jsonl", nil)
	w := httptest.NewRecorder()
	s.handleServeInfo(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var info InfoResponse
	json.NewDecoder(w.Body).Decode(&info)
	if info.Mode != "view" {
		t.Errorf("expected mode=view, got %s", info.Mode)
	}
	if info.Filename != "test.jsonl" {
		t.Errorf("expected filename=test.jsonl, got %s", info.Filename)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./webui/ -run "TestHandleServeInfo" -v
```

Expected: FAIL — `s.handleServeInfo undefined`.

- [ ] **Step 3: Add `handleServeInfo` to `serve_handler.go`**

Add after `handleFiles`:

```go
// handleServeInfo は serve モードの /api/info ハンドラ。
// app.js が呼び出す。file クエリパラメータからファイル名を取得する。
func (s *Server) handleServeInfo(w http.ResponseWriter, r *http.Request) {
	filename := r.URL.Query().Get("file")
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(InfoResponse{
		Mode:     "view",
		Filename: filename,
		Total:    0, // serve モードでは総数は不要
	})
}
```

- [ ] **Step 4: Update `registerServeRoutes` to include `/api/info`**

In `serve_handler.go`, update `registerServeRoutes`:

```go
func (s *Server) registerServeRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/files", s.handleFiles)
	mux.HandleFunc("/api/info", s.handleServeInfo)
	mux.HandleFunc("/api/records", s.handleServeRecords)
	mux.Handle("/static/", http.FileServer(http.FS(staticFS)))
	mux.HandleFunc("/", s.handleServeRoot)
}
```

- [ ] **Step 5: Inject filename JS global into viewer page**

`app.js` calls `api/records` (no params) and `api/info`. In serve mode we need it to call `api/records?file=<filename>` and `api/info?file=<filename>`. The cleanest approach: inject a tiny JS snippet before `app.js` loads, patching the fetch URL.

Update `renderViewerPage` in `serve_handler.go`. Replace the existing function body with:

```go
func (s *Server) renderViewerPage(w http.ResponseWriter, r *http.Request, filename string) {
	data, err := staticFS.ReadFile("static/index.html")
	if err != nil {
		http.Error(w, "index.html not found", 500)
		return
	}

	ver := fmt.Sprintf("%d", time.Now().UnixMilli())
	html := strings.ReplaceAll(string(data), "?v=2", "?v="+ver)

	// 戻るリンクを注入
	backLink := `<a href="/" class="back-link">← 返回列表</a>`
	html = strings.Replace(html,
		`<span class="logo">claude-spy</span>`,
		`<span class="logo">claude-spy</span>`+backLink,
		1,
	)

	// serve モード用の JS グローバルを注入:
	// app.js の fetch('api/records') → fetch('api/records?file=<name>')
	// app.js の fetch('api/info')    → fetch('api/info?file=<name>')
	serveScript := fmt.Sprintf(`<script>
window.__SERVE_FILE__ = %q;
const _origFetch = window.fetch;
window.fetch = function(url, opts) {
  if (url === 'api/records') return _origFetch('api/records?file=' + encodeURIComponent(window.__SERVE_FILE__), opts);
  if (url === 'api/info')    return _origFetch('api/info?file='    + encodeURIComponent(window.__SERVE_FILE__), opts);
  return _origFetch(url, opts);
};
</script>`, filename)

	html = strings.Replace(html, `<script src="static/app.js`, serveScript+`<script src="static/app.js`, 1)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Write([]byte(html))
}
```

- [ ] **Step 6: Run tests**

```bash
go test ./webui/ -run "TestHandleServeInfo" -v
```

Expected: PASS.

- [ ] **Step 7: Build**

```bash
go build ./...
```

Expected: no output.

- [ ] **Step 8: Commit**

```bash
git add webui/serve_handler.go webui/serve_handler_test.go
git commit -m "feat(webui): add handleServeInfo and fetch-patching for serve-mode viewer"
```

---

## Task 5: Add `runServe` to `main.go`

**Files:**
- Modify: `main.go`

- [ ] **Step 1: Write failing test for argument parsing**

Create `serve_test.go` at the project root (alongside `main_test.go`):

```go
package main

import (
	"testing"
)

func TestParseServeArgs_Defaults(t *testing.T) {
	cfg := parseServeArgs([]string{})
	if cfg.port != 8888 {
		t.Errorf("default port: want 8888, got %d", cfg.port)
	}
	if cfg.logDir == "" {
		t.Error("logDir should not be empty")
	}
}

func TestParseServeArgs_Custom(t *testing.T) {
	cfg := parseServeArgs([]string{"--port", "7777", "--log-dir", "/tmp/logs"})
	if cfg.port != 7777 {
		t.Errorf("want 7777, got %d", cfg.port)
	}
	if cfg.logDir != "/tmp/logs" {
		t.Errorf("want /tmp/logs, got %s", cfg.logDir)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test . -run "TestParseServeArgs" -v
```

Expected: FAIL — `parseServeArgs undefined`.

- [ ] **Step 3: Add `serveConfig`, `parseServeArgs`, and `runServe` to `main.go`**

Add the following block before the `runView` function in `main.go`:

```go
type serveConfig struct {
	port   int
	logDir string
}

func parseServeArgs(args []string) serveConfig {
	cfg := serveConfig{
		port:   8888,
		logDir: defaultLogDir(),
	}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--port":
			if i+1 < len(args) {
				n, err := strconv.Atoi(args[i+1])
				if err != nil || n < 0 || n > 65535 {
					fmt.Fprintf(os.Stderr, "Error: invalid --port value %q\n", args[i+1])
					os.Exit(1)
				}
				cfg.port = n
				i++
			}
		case "--log-dir":
			if i+1 < len(args) {
				cfg.logDir = args[i+1]
				i++
			}
		}
	}
	return cfg
}

func runServe(args []string) int {
	cfg := parseServeArgs(args)

	ws, err := webui.NewServeServer(cfg.logDir, cfg.port)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: start serve UI: %v\n", err)
		return 1
	}
	ws.Start()
	fmt.Fprintf(os.Stderr, "[claude-spy] Serving logs from %s\n", cfg.logDir)
	fmt.Fprintf(os.Stderr, "[claude-spy] Web UI: http://localhost:%d\n", ws.Port())
	fmt.Fprintf(os.Stderr, "[claude-spy] Press Ctrl+C to exit\n\n")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	signal.Stop(sigCh)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ws.Shutdown(ctx)
	return 0
}
```

- [ ] **Step 4: Add `serve` dispatch in `run()`**

In `main.go`, inside `run()`, add the serve branch after the `view` branch:

```go
if len(os.Args) > 1 && os.Args[1] == "serve" {
    return runServe(os.Args[2:])
}
```

- [ ] **Step 5: Update `printUsage()` to include serve subcommand**

In `printUsage()`, add to the Subcommands section:

```
  serve [--port <n>] [--log-dir <dir>]   Browse all logs in a directory (default port: 8888)
```

- [ ] **Step 6: Run tests**

```bash
go test . -run "TestParseServeArgs" -v
```

Expected: both PASS.

- [ ] **Step 7: Build and smoke-test**

```bash
go build -o /tmp/claude-spy-test .
/tmp/claude-spy-test --help 2>&1 | grep serve
```

Expected: output contains `serve`.

- [ ] **Step 8: Commit**

```bash
git add main.go serve_test.go
git commit -m "feat: add claude-spy serve subcommand (port 8888, log-dir ~/.claude-spy/logs)"
```

---

## Task 6: End-to-end smoke test

**Files:**
- No code changes — manual verification only

- [ ] **Step 1: Build final binary**

```bash
cd /data/home/jerryma/sourceCode/person_skills/catch_tools
go build -o /tmp/claude-spy-test .
```

- [ ] **Step 2: Start serve**

```bash
/tmp/claude-spy-test serve --port 8888 --log-dir ~/.claude-spy/logs &
SERVE_PID=$!
sleep 1
```

- [ ] **Step 3: Check file list API**

```bash
curl -s http://localhost:8888/api/files | head -c 200
```

Expected: JSON array (may be `[]` if no logs exist, or a list of file objects).

- [ ] **Step 4: Check list page**

```bash
curl -s http://localhost:8888/ | grep -o '<title>[^<]*</title>'
```

Expected: `<title>claude-spy — 日志列表</title>`

- [ ] **Step 5: Check 404 for non-jsonl path**

```bash
curl -s -o /dev/null -w "%{http_code}" http://localhost:8888/etc/passwd
```

Expected: `404`

- [ ] **Step 6: Stop server and run all tests**

```bash
kill $SERVE_PID 2>/dev/null
go test ./... -v 2>&1 | tail -20
```

Expected: all tests PASS, no FAIL lines.

- [ ] **Step 7: Final commit**

```bash
git add -A
git status  # verify nothing unexpected
git commit -m "chore: verified claude-spy serve end-to-end"
```
