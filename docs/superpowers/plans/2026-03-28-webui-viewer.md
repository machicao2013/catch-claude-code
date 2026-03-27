# Web UI 日志查看器 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为 claude-spy 添加 Web UI，支持实时模式（代理运行时浏览器同步显示）和回顾模式（指定 JSONL 文件浏览历史），以可读气泡流展示每条请求/响应详情。

**Architecture:** 新增 `webui` 包提供 HTTP server，内嵌静态前端（HTML/JS/CSS）。`proxy.Handler` 通过 `WebUIPusher` 接口（nil 安全）在记录 JSONL 后同时推送给 webui；`main.go` 扩展支持 `--web-port` 参数和 `view <file>` 子命令。

**Tech Stack:** Go 1.23（标准库，`net/http`、`embed`、`bufio`），原生 JS（无框架），CSS 深色主题，Server-Sent Events（SSE）实时推送。

---

## 文件映射

| 操作 | 路径 | 职责 |
|------|------|------|
| 新建 | `webui/server.go` | HTTP server 注册路由、SSE hub（channel + 广播）、`Push(rec)` 方法、`Start`/`Shutdown` |
| 新建 | `webui/handler.go` | `/api/records`、`/api/stream`、`/api/info` 三个 handler 函数 |
| 新建 | `webui/embed.go` | `//go:embed static` 指令，暴露 `staticFS` |
| 新建 | `webui/static/index.html` | 页面骨架：顶栏 + 主体容器 |
| 新建 | `webui/static/style.css` | 深色主题、气泡样式、折叠动画 |
| 新建 | `webui/static/app.js` | 启动逻辑、渲染函数、折叠展开交互 |
| 修改 | `proxy/handler.go` | 新增 `WebUIPusher` 接口 + `webui` 字段；`NewHandler` 增加参数；`recordAndSummarize` 末尾调用 `Push` |
| 修改 | `main.go` | 解析 `--web-port`；解析 `view <file> [--port N]` 子命令；组装 webui.Server 并传给 proxy |

---

## Task 1: webui.Server 骨架（路由 + SSE hub）

**Files:**
- Create: `webui/server.go`
- Create: `webui/embed.go`

- [ ] **Step 1: 创建 `webui/embed.go`**

```go
package webui

import "embed"

//go:embed static
var staticFS embed.FS
```

- [ ] **Step 2: 创建 `webui/server.go`**

```go
package webui

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sync"

	"claude-spy/recorder"
)

// Mode 表示 webui 运行模式
type Mode string

const (
	ModeLive Mode = "live" // 实时模式：配合代理使用
	ModeView Mode = "view" // 回顾模式：查看历史 JSONL
)

// Server 提供 Web UI 的 HTTP 服务。
type Server struct {
	mode     Mode
	filename string // 日志文件名（显示用）

	mu      sync.RWMutex
	records []recorder.Record

	// SSE 订阅者：每个连接一个 channel
	subsMu sync.Mutex
	subs   map[chan []byte]struct{}

	httpServer *http.Server
	listener   net.Listener
	port       int
}

// NewServer 创建并初始化 Server，但不启动监听。
// port=0 时自动选择空闲端口。
func NewServer(mode Mode, filename string, port int) (*Server, error) {
	addr := fmt.Sprintf("0.0.0.0:%d", port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("webui listen on %s: %w", addr, err)
	}
	s := &Server{
		mode:     mode,
		filename: filename,
		subs:     make(map[chan []byte]struct{}),
		listener: ln,
		port:     ln.Addr().(*net.TCPAddr).Port,
	}
	mux := http.NewServeMux()
	s.registerRoutes(mux)
	s.httpServer = &http.Server{Handler: mux}
	return s, nil
}

func (s *Server) Port() int { return s.port }

func (s *Server) URL() string { return fmt.Sprintf("http://localhost:%d", s.port) }

// Start 在后台 goroutine 中启动 HTTP server。
func (s *Server) Start() {
	go s.httpServer.Serve(s.listener)
}

// Shutdown 优雅关闭 HTTP server。
func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

// Push 接收新记录，存储并广播给所有 SSE 订阅者。
// 实现 proxy.WebUIPusher 接口。
func (s *Server) Push(rec recorder.Record) {
	s.mu.Lock()
	s.records = append(s.records, rec)
	s.mu.Unlock()

	data, _ := json.Marshal(rec)
	msg := []byte("event: record\ndata: " + string(data) + "\n\n")
	s.broadcast(msg)
}

func (s *Server) broadcast(msg []byte) {
	s.subsMu.Lock()
	defer s.subsMu.Unlock()
	for ch := range s.subs {
		select {
		case ch <- msg:
		default: // 订阅者消费慢时丢弃，不阻塞
		}
	}
}

func (s *Server) subscribe() chan []byte {
	ch := make(chan []byte, 32)
	s.subsMu.Lock()
	s.subs[ch] = struct{}{}
	s.subsMu.Unlock()
	return ch
}

func (s *Server) unsubscribe(ch chan []byte) {
	s.subsMu.Lock()
	delete(s.subs, ch)
	s.subsMu.Unlock()
}
```

- [ ] **Step 3: 确认编译无报错**

```bash
cd /data/home/jerryma/sourceCode/person_skills/catch_tools
go build ./webui/...
```

Expected: 无输出（编译成功）。若报 "no Go files"，检查 `static/` 目录是否存在（embed 需要目录）：`mkdir -p webui/static && touch webui/static/.gitkeep`

- [ ] **Step 4: commit**

```bash
git add webui/
git commit -m "feat(webui): add Server skeleton with SSE hub"
```

---

## Task 2: HTTP 路由与 API handlers

**Files:**
- Create: `webui/handler.go`
- Modify: `webui/server.go`（新增 `registerRoutes`、`LoadFromFile`）

- [ ] **Step 1: 在 `webui/server.go` 末尾追加 `registerRoutes` 和 `LoadFromFile`**

```go
// registerRoutes 注册所有路由。
func (s *Server) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/records", s.handleRecords)
	mux.HandleFunc("/api/stream", s.handleStream)
	mux.HandleFunc("/api/info", s.handleInfo)
	// 静态文件：index.html 及 /static/* 资源
	mux.Handle("/static/", http.FileServer(http.FS(staticFS)))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		data, err := staticFS.ReadFile("static/index.html")
		if err != nil {
			http.Error(w, "not found", 404)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(data)
	})
}

// LoadFromFile 读取 JSONL 文件并填充 records（回顾模式用）。
// loadJSONLFile 定义在同包的 handler.go 中。
func (s *Server) LoadFromFile(path string) error {
	return loadJSONLFile(path, func(rec recorder.Record) {
		s.mu.Lock()
		s.records = append(s.records, rec)
		s.mu.Unlock()
	})
}
```

- [ ] **Step 2: 创建 `webui/handler.go`**

```go
package webui

import (
	"bufio"
	"encoding/json"
	"fmt"
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
	if len(recs) == 0 {
		w.Write([]byte("[]"))
		return
	}
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
			continue // 跳过损坏行
		}
		fn(rec)
	}
	return scanner.Err()
}
```

- [ ] **Step 3: 确认编译**

```bash
go build ./webui/...
```

Expected: 无输出。

- [ ] **Step 4: commit**

```bash
git add webui/
git commit -m "feat(webui): add HTTP handlers for /api/records, /api/stream, /api/info"
```

---

## Task 3: 前端骨架（index.html + style.css）

**Files:**
- Create: `webui/static/index.html`
- Create: `webui/static/style.css`

- [ ] **Step 1: 创建 `webui/static/index.html`**

```html
<!DOCTYPE html>
<html lang="zh-CN">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>claude-spy viewer</title>
  <link rel="stylesheet" href="/static/style.css">
</head>
<body>
  <header id="topbar">
    <div class="topbar-left">
      <span class="logo">claude-spy</span>
      <span id="filename" class="filename"></span>
    </div>
    <div class="topbar-right">
      <span id="mode-badge" class="badge"></span>
      <span id="stats" class="stats"></span>
    </div>
  </header>
  <main id="records-list"></main>
  <script src="/static/app.js"></script>
</body>
</html>
```

- [ ] **Step 2: 创建 `webui/static/style.css`**

```css
*, *::before, *::after { box-sizing: border-box; margin: 0; padding: 0; }

:root {
  --bg:        #0f172a;
  --surface:   #1e293b;
  --surface2:  #0f172a;
  --border:    #334155;
  --text:      #e2e8f0;
  --muted:     #94a3b8;
  --user:      #f59e0b;
  --assistant: #a78bfa;
  --tool:      #4ade80;
  --result:    #64748b;
  --blue:      #60a5fa;
  --accent:    #3b82f6;
}

body {
  background: var(--bg);
  color: var(--text);
  font-family: ui-monospace, "SFMono-Regular", Menlo, monospace;
  font-size: 13px;
  min-height: 100vh;
}

/* ── 顶栏 ── */
#topbar {
  position: sticky; top: 0; z-index: 100;
  display: flex; align-items: center; justify-content: space-between;
  padding: 10px 20px;
  background: #0a1628;
  border-bottom: 1px solid var(--border);
}
.logo { font-weight: 700; color: var(--blue); margin-right: 12px; }
.filename { color: var(--muted); font-size: 12px; }
.topbar-right { display: flex; align-items: center; gap: 16px; }
.badge {
  font-size: 11px; padding: 2px 8px; border-radius: 999px;
  font-weight: 600; text-transform: uppercase; letter-spacing: 0.05em;
}
.badge.live { background: #14532d; color: var(--tool); }
.badge.live::before { content: "● "; }
.badge.view { background: #1e3a5f; color: var(--blue); }
.stats { color: var(--muted); font-size: 12px; }

/* ── 记录列表 ── */
#records-list { padding: 16px 20px; max-width: 960px; margin: 0 auto; }

/* ── 单条记录 ── */
.record { margin-bottom: 10px; border-radius: 8px; overflow: hidden; border: 1px solid var(--border); }

.record-header {
  display: flex; align-items: center; justify-content: space-between;
  padding: 10px 14px;
  background: var(--surface);
  cursor: pointer;
  user-select: none;
  transition: background 0.1s;
}
.record-header:hover { background: #253347; }

.record-header-left { display: flex; align-items: center; gap: 12px; }
.req-id { color: var(--blue); font-weight: 700; font-size: 12px; }
.req-time { color: var(--muted); font-size: 12px; }
.req-duration { color: var(--text); font-size: 12px; }
.req-tokens { color: var(--tool); font-size: 12px; }
.req-stop { color: var(--muted); font-size: 11px; padding: 1px 6px; background: var(--surface2); border-radius: 4px; }
.expand-icon { color: var(--muted); font-size: 11px; transition: transform 0.2s; }
.record.expanded .expand-icon { transform: rotate(180deg); }

/* ── 展开体 ── */
.record-body { display: none; padding: 12px 14px; background: var(--surface2); border-top: 1px solid var(--border); }
.record.expanded .record-body { display: block; }

/* ── 消息气泡 ── */
.msg { display: flex; gap: 8px; margin-bottom: 8px; align-items: flex-start; }
.role-tag {
  flex-shrink: 0; padding: 2px 7px; border-radius: 4px;
  font-size: 10px; font-weight: 700; text-transform: uppercase;
  letter-spacing: 0.04em; margin-top: 2px;
}
.role-user     { background: #78350f; color: var(--user); }
.role-assistant{ background: #3b0764; color: var(--assistant); }
.role-tool-use { background: #14532d; color: var(--tool); border: 1px solid var(--tool); }
.role-result   { background: #1e293b; color: var(--result); }

.msg-content {
  flex: 1; background: var(--surface); border-radius: 6px;
  padding: 7px 10px; font-size: 12px; line-height: 1.5;
  word-break: break-word; white-space: pre-wrap;
}
.msg-content.tool-use-content { border: 1px solid #166534; }
.tool-name { color: var(--tool); font-weight: 700; margin-bottom: 4px; font-size: 12px; }
.tool-param { color: var(--muted); font-size: 11px; }
.tool-param span { color: var(--text); }

/* ── 长内容折叠 ── */
.collapsible { position: relative; }
.collapsible.collapsed .collapsible-inner {
  max-height: 80px; overflow: hidden;
}
.collapsible.collapsed .collapsible-inner::after {
  content: "";
  position: absolute; bottom: 0; left: 0; right: 0;
  height: 32px;
  background: linear-gradient(transparent, var(--surface));
}
.toggle-btn {
  display: inline-block; margin-top: 4px;
  color: var(--blue); font-size: 11px; cursor: pointer; text-decoration: underline;
}

/* ── 响应区 ── */
.response-footer {
  margin-top: 10px; padding: 8px 10px;
  background: var(--surface); border-radius: 6px; border-left: 3px solid var(--blue);
  font-size: 11px; color: var(--muted);
  display: flex; gap: 16px; flex-wrap: wrap;
}
.response-footer .label { color: var(--muted); }
.response-footer .val   { color: var(--text); }
.response-footer .tok-in   { color: var(--blue); }
.response-footer .tok-out  { color: var(--tool); }
.response-footer .tok-cache{ color: var(--user); }

/* ── 滑入动画（实时模式新记录） ── */
@keyframes slideIn {
  from { opacity: 0; transform: translateY(12px); }
  to   { opacity: 1; transform: translateY(0); }
}
.record.new { animation: slideIn 0.25s ease-out; }
```

- [ ] **Step 3: 确认编译**（embed 依赖静态文件存在）

```bash
go build ./...
```

Expected: 无输出。

- [ ] **Step 4: commit**

```bash
git add webui/static/
git commit -m "feat(webui): add frontend skeleton HTML and CSS"
```

---

## Task 4: 前端 JS 逻辑（app.js）

**Files:**
- Create: `webui/static/app.js`

- [ ] **Step 1: 创建 `webui/static/app.js`**

```js
// ── 工具函数 ──────────────────────────────────────────────
function fmtTime(ts) {
  const d = new Date(ts);
  return d.toLocaleTimeString('zh-CN', { hour12: false });
}

function fmtDuration(ms) {
  return ms < 1000 ? `${ms}ms` : `${(ms / 1000).toFixed(1)}s`;
}

function fmtNum(n) {
  if (!n) return '0';
  return n.toString().replace(/\B(?=(\d{3})+(?!\d))/g, ',');
}

function escHtml(s) {
  return String(s)
    .replace(/&/g, '&amp;').replace(/</g, '&lt;')
    .replace(/>/g, '&gt;').replace(/"/g, '&quot;');
}

// ── 折叠/展开长内容 ─────────────────────────────────────────
function makeCollapsible(contentEl, threshold) {
  const lines = contentEl.innerText.split('\n');
  if (lines.length <= threshold) return;

  const wrapper = document.createElement('div');
  wrapper.className = 'collapsible collapsed';
  const inner = document.createElement('div');
  inner.className = 'collapsible-inner';
  inner.innerHTML = contentEl.innerHTML;
  const btn = document.createElement('span');
  btn.className = 'toggle-btn';
  btn.textContent = '▼ 点击展开全文';
  btn.onclick = () => {
    const collapsed = wrapper.classList.toggle('collapsed');
    btn.textContent = collapsed ? '▼ 点击展开全文' : '▲ 收起';
  };
  wrapper.appendChild(inner);
  wrapper.appendChild(btn);
  contentEl.replaceWith(wrapper);
}

// ── 渲染单条 message ──────────────────────────────────────
function renderMessage(msg) {
  const role = msg.role || 'unknown';
  const content = msg.content;

  // content 可能是字符串或数组
  const blocks = Array.isArray(content)
    ? content
    : [{ type: 'text', text: typeof content === 'string' ? content : JSON.stringify(content) }];

  const frags = [];
  for (const block of blocks) {
    if (block.type === 'text') {
      frags.push(renderTextMsg(role, block.text));
    } else if (block.type === 'tool_use') {
      frags.push(renderToolUse(block));
    } else if (block.type === 'tool_result') {
      frags.push(renderToolResult(block));
    } else {
      frags.push(renderTextMsg(role, JSON.stringify(block)));
    }
  }
  return frags.join('');
}

function renderTextMsg(role, text) {
  const tagClass = role === 'user' ? 'role-user' : 'role-assistant';
  const label = role === 'user' ? 'user' : 'asst';
  return `
    <div class="msg">
      <span class="role-tag ${tagClass}">${label}</span>
      <div class="msg-content" data-lines="${(text || '').split('\n').length}">${escHtml(text || '')}</div>
    </div>`;
}

function renderToolUse(block) {
  const inputStr = block.input ? JSON.stringify(block.input, null, 2) : '{}';
  return `
    <div class="msg">
      <span class="role-tag role-tool-use">tool</span>
      <div class="msg-content tool-use-content">
        <div class="tool-name">⚙ ${escHtml(block.name || '')}</div>
        <div class="tool-param" data-lines="${inputStr.split('\n').length}"><span>${escHtml(inputStr)}</span></div>
      </div>
    </div>`;
}

function renderToolResult(block) {
  const c = block.content;
  const text = Array.isArray(c)
    ? c.map(x => x.text || JSON.stringify(x)).join('\n')
    : (typeof c === 'string' ? c : JSON.stringify(c));
  return `
    <div class="msg">
      <span class="role-tag role-result">result</span>
      <div class="msg-content" data-lines="${text.split('\n').length}">${escHtml(text)}</div>
    </div>`;
}

// ── 渲染单条记录 ──────────────────────────────────────────
function renderRecord(rec, isNew) {
  const el = document.createElement('div');
  el.className = 'record' + (isNew ? ' new' : '');
  el.dataset.id = rec.id;

  // 解析请求体
  const reqBody = rec.request?.body || {};
  const messages = reqBody.messages || [];
  const respBody = rec.response?.body || {};
  const usage = respBody.usage || {};
  const stopReason = respBody.stop_reason || '—';

  const timeStr = fmtTime(rec.timestamp);
  const durStr = fmtDuration(rec.duration_ms);
  const inTok = fmtNum(usage.input_tokens);
  const outTok = fmtNum(usage.output_tokens);
  const seqNum = rec.id; // e.g. "req_001"

  el.innerHTML = `
    <div class="record-header" onclick="toggleRecord(this)">
      <div class="record-header-left">
        <span class="req-id">${escHtml(seqNum)}</span>
        <span class="req-time">${timeStr}</span>
        <span class="req-duration">${durStr}</span>
        <span class="req-tokens">${inTok} in / ${outTok} out</span>
        <span class="req-stop">${escHtml(stopReason)}</span>
      </div>
      <span class="expand-icon">▼</span>
    </div>
    <div class="record-body"></div>`;

  // 懒渲染：展开时才填充 body
  el._rec = rec;
  el._rendered = false;

  return el;
}

function toggleRecord(headerEl) {
  const el = headerEl.closest('.record');
  el.classList.toggle('expanded');

  if (el.classList.contains('expanded') && !el._rendered) {
    el._rendered = true;
    const body = el.querySelector('.record-body');
    const rec = el._rec;
    const reqBody = rec.request?.body || {};
    const messages = reqBody.messages || [];
    const respBody = rec.response?.body || {};
    const usage = respBody.usage || {};

    let html = '';
    for (const msg of messages) {
      html += renderMessage(msg);
    }

    // 响应 footer
    const cacheCreate = fmtNum(usage.cache_creation_input_tokens);
    const cacheRead = fmtNum(usage.cache_read_input_tokens);
    html += `
      <div class="response-footer">
        <span><span class="label">stop:</span> <span class="val">${escHtml(respBody.stop_reason || '—')}</span></span>
        <span><span class="label">dur:</span> <span class="val">${fmtDuration(rec.duration_ms)}</span></span>
        <span><span class="label">in:</span> <span class="tok-in">${fmtNum(usage.input_tokens)}</span></span>
        <span><span class="label">out:</span> <span class="tok-out">${fmtNum(usage.output_tokens)}</span></span>
        <span><span class="label">cache_create:</span> <span class="tok-cache">${cacheCreate}</span></span>
        <span><span class="label">cache_read:</span> <span class="tok-cache">${cacheRead}</span></span>
      </div>`;

    body.innerHTML = html;

    // 对超长内容启用折叠
    body.querySelectorAll('.msg-content, .tool-param').forEach(el => {
      const lines = parseInt(el.dataset.lines || '0', 10);
      const threshold = el.classList.contains('tool-param') ? 5 : 10;
      if (lines > threshold) makeCollapsible(el, threshold);
    });
  }
}

// ── 统计更新 ──────────────────────────────────────────────
let totalReqs = 0, totalIn = 0, totalOut = 0;

function updateStats() {
  document.getElementById('stats').textContent =
    `${totalReqs} 请求 · ${fmtNum(totalIn)} in · ${fmtNum(totalOut)} out tokens`;
}

function accStats(rec) {
  totalReqs++;
  const u = rec.response?.body?.usage || {};
  totalIn  += u.input_tokens  || 0;
  totalOut += u.output_tokens || 0;
  updateStats();
}

// ── 主入口 ────────────────────────────────────────────────
async function main() {
  // 1. 获取模式信息
  const info = await fetch('/api/info').then(r => r.json());
  document.getElementById('filename').textContent = info.filename;

  const badge = document.getElementById('mode-badge');
  if (info.mode === 'live') {
    badge.textContent = 'live';
    badge.className = 'badge live';
  } else {
    badge.textContent = '回顾';
    badge.className = 'badge view';
  }

  // 2. 加载已有记录
  const list = document.getElementById('records-list');
  const records = await fetch('/api/records').then(r => r.json());
  for (const rec of (records || [])) {
    list.appendChild(renderRecord(rec, false));
    accStats(rec);
  }

  // 3. 实时模式：连接 SSE
  if (info.mode === 'live') {
    const es = new EventSource('/api/stream');
    es.addEventListener('record', e => {
      const rec = JSON.parse(e.data);
      const el = renderRecord(rec, true);
      list.appendChild(el);
      accStats(rec);
      el.scrollIntoView({ behavior: 'smooth', block: 'nearest' });
    });
  }
}

main().catch(console.error);
```

- [ ] **Step 2: 确认完整编译**

```bash
go build ./...
```

Expected: 无输出。

- [ ] **Step 3: commit**

```bash
git add webui/static/app.js
git commit -m "feat(webui): add frontend JS - render records, SSE live updates, collapsible content"
```

---

## Task 5: 修改 proxy.Handler 支持 WebUIPusher

**Files:**
- Modify: `proxy/handler.go`（新增接口 + 字段 + Push 调用）

- [ ] **Step 1: 在 `proxy/handler.go` 的 import 块后、`Handler` struct 前，添加接口定义**

在文件顶部 `type Handler struct` 前插入：

```go
// WebUIPusher 是 webui.Server 暴露给 proxy 的最小接口。
// 定义在 proxy 包内避免循环依赖（webui 导入 recorder，proxy 也导入 recorder）。
type WebUIPusher interface {
	Push(rec recorder.Record)
}
```

- [ ] **Step 2: 修改 `Handler` struct，新增 `webui` 字段**

将：
```go
type Handler struct {
	targetURL string
	client    *http.Client
	recorder  recorder.Recorder
	printer   *display.Printer
	summary   *display.Summary
	saveSSE   bool
	reqCount  atomic.Int64
}
```

改为：
```go
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
```

- [ ] **Step 3: 修改 `NewHandler` 函数签名，接受 `webui WebUIPusher` 参数**

将：
```go
func NewHandler(targetURL string, rec recorder.Recorder, printer *display.Printer, summary *display.Summary, saveSSE bool) *Handler {
	return &Handler{
		targetURL: strings.TrimRight(targetURL, "/"),
		client:    &http.Client{Timeout: 10 * time.Minute},
		recorder:  rec,
		printer:   printer,
		summary:   summary,
		saveSSE:   saveSSE,
	}
}
```

改为：
```go
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
```

- [ ] **Step 4: 在 `recordAndSummarize` 末尾（`h.recorder.Write(rec)` 之后）添加 Push 调用**

找到：
```go
	if err := h.recorder.Write(rec); err != nil {
		h.printer.PrintError(fmt.Sprintf("write record: %v", err))
	}
}
```

改为：
```go
	if err := h.recorder.Write(rec); err != nil {
		h.printer.PrintError(fmt.Sprintf("write record: %v", err))
	}

	if h.webui != nil {
		h.webui.Push(rec)
	}
}
```

- [ ] **Step 5: 修复 `main.go` 中的 `NewHandler` 调用（旧调用缺少最后一个参数）**

在 `main.go` 找到：
```go
handler := proxy.NewHandler(args.upstream, rec, printer, summary, args.saveSSE)
```

改为：
```go
handler := proxy.NewHandler(args.upstream, rec, printer, summary, args.saveSSE, nil)
```

（此时 webui 为 nil，实时模式接入在 Task 6 完成）

- [ ] **Step 6: 确认测试通过**

```bash
go test ./proxy/... ./...
```

Expected: 所有测试 PASS，无编译错误。

- [ ] **Step 7: commit**

```bash
git add proxy/handler.go main.go
git commit -m "feat(proxy): add WebUIPusher interface, wire optional web UI push"
```

---

## Task 6: 扩展 main.go — 实时模式 `--web-port` + 回顾模式 `view` 子命令

**Files:**
- Modify: `main.go`

- [ ] **Step 1: 在 `config` struct 中新增 `webPort` 字段**

将：
```go
type config struct {
	upstream string
	port     int
	quiet    bool
	saveSSE  bool
	logDir   string
}
```

改为：
```go
type config struct {
	upstream string
	port     int
	quiet    bool
	saveSSE  bool
	logDir   string
	webPort  int // 0 表示不启用 Web UI（实时模式）
}
```

- [ ] **Step 2: 在 `parseArgs` 中解析 `--web-port`**

在 `parseArgs` 的 switch 块中，`case "--log-dir":` 之后添加：

```go
		case "--web-port":
			if i+1 < len(args) {
				n, err := strconv.Atoi(args[i+1])
				if err != nil || n < 0 || n > 65535 {
					fmt.Fprintf(os.Stderr, "Error: invalid --web-port value %q\n", args[i+1])
					os.Exit(1)
				}
				cfg.webPort = n
				i++
			}
```

- [ ] **Step 3: 在 `run()` 函数中，创建 handler 前启动 webui（实时模式）**

找到：
```go
	handler := proxy.NewHandler(args.upstream, rec, printer, summary, args.saveSSE, nil)
```

替换为：
```go
	// 实时模式：若指定了 --web-port，启动 webui server
	var webuiSrv *webui.Server
	var pusher proxy.WebUIPusher
	if args.webPort != 0 {
		ws, err := webui.NewServer(webui.ModeLive, filepath.Base(logPath), args.webPort)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: start web UI: %v\n", err)
			return 1
		}
		webuiSrv = ws
		pusher = ws
		ws.Start()
		fmt.Fprintf(os.Stderr, "[claude-spy] Web UI:  %s\n", ws.URL())
	}
	handler := proxy.NewHandler(args.upstream, rec, printer, summary, args.saveSSE, pusher)
```

并在 `srv.Shutdown` 之后、`printer.PrintSessionSummary` 之前，添加 webui 关闭：

```go
	if webuiSrv != nil {
		webuiSrv.Shutdown(shutdownCtx)
	}
```

- [ ] **Step 4: 在 `main()` 中处理 `view` 子命令**

在 `run()` 函数 `parseArgs` 调用之前（`for _, a := range os.Args[1:]` 块之后），添加子命令分发：

```go
	// 检查是否为 view 子命令
	if len(os.Args) > 1 && os.Args[1] == "view" {
		return runView(os.Args[2:])
	}
```

然后在 `main.go` 底部新增 `runView` 函数：

```go
func runView(args []string) int {
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "Usage: claude-spy view <file.jsonl> [--port <n>]\n")
		return 1
	}
	filePath := args[0]
	port := 0 // 0 = 随机端口

	for i := 1; i < len(args); i++ {
		if args[i] == "--port" && i+1 < len(args) {
			n, err := strconv.Atoi(args[i+1])
			if err != nil || n < 0 || n > 65535 {
				fmt.Fprintf(os.Stderr, "Error: invalid --port value %q\n", args[i+1])
				return 1
			}
			port = n
			i++
		}
	}

	ws, err := webui.NewServer(webui.ModeView, filepath.Base(filePath), port)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: start web UI: %v\n", err)
		return 1
	}
	if err := ws.LoadFromFile(filePath); err != nil {
		fmt.Fprintf(os.Stderr, "Error: load file: %v\n", err)
		return 1
	}
	ws.Start()
	fmt.Fprintf(os.Stderr, "[claude-spy] Viewing: %s\n", filePath)
	fmt.Fprintf(os.Stderr, "[claude-spy] Web UI:  %s\n", ws.URL())
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

- [ ] **Step 5: 补全 main.go 的 import（新增 `claude-spy/webui`）**

在 `main.go` 的 import 块中添加：
```go
"claude-spy/webui"
```

- [ ] **Step 6: 确认完整编译 + 测试**

```bash
go build ./... && go test ./...
```

Expected: 编译成功，所有测试 PASS。

- [ ] **Step 7: 更新 `printUsage()` 函数，加入新参数说明**

在现有 Usage 字符串中增加：
```
  --web-port <n>     Start web UI on this port (live mode, alongside proxy)

Subcommands:
  view <file.jsonl> [--port <n>]   Browse a recorded log file in the browser
```

- [ ] **Step 8: commit**

```bash
git add main.go
git commit -m "feat: add --web-port flag (live mode) and 'view' subcommand (review mode)"
```

---

## Task 7: 端到端手动验证

**Files:** 无新增文件

- [ ] **Step 1: 构建二进制**

```bash
go build -o claude-spy .
```

Expected: 生成 `claude-spy` 可执行文件。

- [ ] **Step 2: 验证回顾模式**

如果 `~/.claude-spy/logs/` 下有 JSONL 文件：
```bash
./claude-spy view ~/.claude-spy/logs/<任意文件>.jsonl --port 9090
```

Expected 输出：
```
[claude-spy] Viewing: <filename>.jsonl
[claude-spy] Web UI:  http://localhost:9090
[claude-spy] Press Ctrl+C to exit
```

打开 `http://localhost:9090`，验证：
- 顶栏显示文件名、"回顾"标签
- 每条请求显示为折叠的摘要行
- 点击展开，messages 以气泡流展示
- tool_use 有绿色边框
- 长内容（>10行）有折叠按钮

若无现有日志文件，创建测试 JSONL：
```bash
cat > /tmp/test.jsonl << 'EOF'
{"id":"req_001","timestamp":"2026-03-28T10:00:00Z","duration_ms":1200,"request":{"method":"POST","path":"/v1/messages","headers":{},"body":{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":"你好，帮我写个 hello world"},{"role":"assistant","content":[{"type":"text","text":"好的，这是一个 Python 的 hello world：\n\nprint('Hello, World!')\n\n运行方式：\npython hello.py"}]}]}},"response":{"status":200,"headers":{},"body":{"stop_reason":"end_turn","usage":{"input_tokens":52103,"output_tokens":387,"cache_read_input_tokens":50000,"cache_creation_input_tokens":0}}}}
EOF
./claude-spy view /tmp/test.jsonl --port 9090
```

- [ ] **Step 3: 验证实时模式（可选，需要上游地址）**

```bash
./claude-spy --upstream https://api.anthropic.com --port 8080 --web-port 8081
```

Expected 输出额外包含：
```
[claude-spy] Web UI:  http://localhost:8081
```

打开 `http://localhost:8081`，顶栏显示绿色 "LIVE" 标识。

- [ ] **Step 4: 最终 commit**

```bash
git add -A
git commit -m "feat: web UI viewer complete - live and review modes"
```

---

## 自检备注

- **Task 1 → Task 2**：`LoadFromFile` 调用 `loadJSONLFile`，后者定义在 `handler.go`，同属 `webui` 包，无循环依赖 ✓
- **Task 5 → Task 6**：`proxy.WebUIPusher` 接口在 Task 5 定义，Task 6 的 `main.go` 传入 `*webui.Server` 作为实现 ✓；`webui.Server` 需实现 `Push(recorder.Record)` 方法，在 Task 1 的 `server.go` 已定义 ✓
- **embed 依赖**：`embed.go` 使用 `//go:embed static`，需要 `webui/static/` 目录在 Task 3 之前创建；Task 1 Step 3 已包含 `mkdir -p webui/static` 兜底 ✓
- **旧调用兼容**：Task 5 Step 5 将 `main.go` 的 `NewHandler` 调用补齐 `nil`，确保编译通过 ✓
