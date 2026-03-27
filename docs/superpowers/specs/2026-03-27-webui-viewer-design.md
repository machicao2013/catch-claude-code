# Web UI 日志查看器 设计文档

**日期：** 2026-03-27
**状态：** 已批准

---

## 背景

`claude-spy` 将 Claude API 请求/响应记录为 JSONL 文件，每条记录包含完整的请求体（messages 数组、tools 列表等）和响应体，单条 JSON 非常长，终端难以阅读。需要一个 Web 界面来直观展示这些日志。

---

## 目标

1. **实时模式**：`claude-spy` 代理运行时，同时提供 Web UI，新请求实时出现在浏览器中
2. **回顾模式**：事后指定 JSONL 文件，在浏览器中浏览历史记录

---

## 架构

### 新增包结构

```
claude-spy/
├── main.go              # 扩展：解析 view 子命令 + --web-port 参数
├── webui/
│   ├── server.go        # HTTP server：路由注册、SSE hub、Push 方法
│   ├── handler.go       # API handlers：/api/records, /api/stream, /api/info
│   ├── embed.go         # //go:embed static
│   └── static/
│       ├── index.html   # 单页应用入口
│       ├── app.js       # 瀑布流 UI 逻辑（原生 JS，无框架）
│       └── style.css    # 深色主题样式
├── recorder/            # 不变
├── proxy/               # 不变
└── display/             # 不变
```

### 数据流

```
[实时模式]
proxy.Handler → webuiServer.Push(rec) → SSE hub → browser

[回顾模式]
jsonl file → webui.Server 读取 → GET /api/records → browser
```

---

## 启动方式

### 实时模式

```bash
claude-spy --upstream https://api.anthropic.com --web-port 8081
```

- 代理和 Web UI 同时启动
- 代理拦截请求后，写 JSONL 的同时调用 `webuiServer.Push(rec)`
- 浏览器通过 SSE 实时接收新记录

### 回顾模式

```bash
claude-spy view ~/.claude-spy/logs/20260327_153000_a1b2.jsonl [--port 8081]
```

- `--port` 可选，默认随机选空闲端口并打印 URL
- 一次性读取 JSONL 文件，启动 HTTP server
- 自动打印访问地址，浏览器一次性加载全部记录

---

## HTTP 路由

| 路由 | 方法 | 说明 |
|------|------|------|
| `/` | GET | 返回 `index.html` |
| `/static/*` | GET | CSS / JS 静态资源 |
| `/api/records` | GET | 返回所有记录（JSON 数组） |
| `/api/stream` | GET | SSE 长连接，实时推送新记录 |
| `/api/info` | GET | 返回模式（live/view）、文件名、session 统计 |

### SSE 事件格式

```
event: record
data: {"id":"req_003","timestamp":"...","duration_ms":1800,...}

event: stats
data: {"total_requests":3,"total_in_tokens":161000,"total_out_tokens":1489}
```

---

## Web UI 交互设计

### 整体布局

- **顶栏**：工具名、当前文件名、模式标识（实时绿点 / 回顾标签）、session 统计（请求数、总 token、时长）
- **主体**：时间轴瀑布流，每条请求默认折叠为摘要行，点击展开

### 折叠行（默认）

显示内容：`REQ #N · 时间 · 耗时 · input tokens / output tokens · stop reason`

### 展开后——聊天气泡流

每条 message 按顺序展示，role 用彩色标签区分：

| Role | 颜色 | 说明 |
|------|------|------|
| `user` | 橙色 | 用户输入 |
| `assistant` (text) | 紫色 | 模型文本回复 |
| `assistant` (tool_use) | 绿色边框 | 工具调用，显示工具名+参数 |
| `tool` (tool_result) | 灰色 | 工具返回结果 |

**长内容折叠规则：**
- `tool_result` 超过 5 行默认折叠，点击展开
- `text` 类型内容超过 10 行默认折叠
- 折叠时显示前几行 + "点击展开全文"

**响应区（每条记录底部）：**
- stop reason、耗时
- token 用量：in / out / cache_create / cache_read

### 实时 vs 回顾模式差异

| 特性 | 实时模式 | 回顾模式 |
|------|---------|---------|
| 数据加载 | SSE 增量推送 | 一次性 API 请求 |
| 新记录动画 | 从底部滑入 | 无 |
| 顶栏指示 | 绿色实时点 | "回顾" 标签 |

---

## 前端启动逻辑

```
页面加载
  → GET /api/info        (判断 live/view 模式)
  → GET /api/records     (加载已有记录，渲染)
  → 若 live 模式：
      连接 GET /api/stream (SSE)
      监听 record 事件 → 追加到列表
      监听 stats 事件 → 更新顶栏统计
```

前端使用原生 JS（无框架），CSS 深色主题，embed 进二进制。

---

## proxy.Handler 兼容性

`webui.Server` 实现可选注入。`WebUIPusher` 接口定义在 `proxy` 包中，避免循环依赖：

```go
// proxy/handler.go — 接口定义
type WebUIPusher interface {
    Push(rec recorder.Record)
}

// proxy.NewHandler 新增可选的 webui server 参数
func NewHandler(upstream string, rec recorder.Recorder, printer *display.Printer,
    summary *display.Summary, saveSSE bool, webui WebUIPusher) *Handler
```

`main.go` 中将 `*webui.Server`（实现了 `Push` 方法）传入；不使用 web UI 时传 `nil`。

`proxy.Handler` 在写完 JSONL 后调用 `Push()`，`webui` 为 `nil` 时跳过，不影响现有行为。

`/api/stream` 仅在实时模式下有意义；前端根据 `/api/info` 返回的 `mode` 字段决定是否连接 SSE，回顾模式下不建立 SSE 连接。

---

## 不在范围内

- 认证/鉴权（内网工具，沿用现有安全说明）
- 多文件同时查看
- 日志搜索/过滤（可后续迭代）
- 移动端适配
