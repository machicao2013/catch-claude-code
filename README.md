# claude-spy

一个独立的 HTTP 反向代理，部署在任意机器上，将 Claude Code 的 API 请求转发到 Anthropic API（或企业网关），同时实时打印请求摘要、将完整数据写入 JSONL 日志，并提供 Web UI 可视化查看每次对话详情。

## 架构

```
[Claude Code 客户端]
  ANTHROPIC_BASE_URL=http://<proxy-host>:8080
        │
        ▼
[claude-spy 代理服务]
  ├─ 拦截 /v1/messages
  ├─ 终端实时摘要 (stderr)
  ├─ JSONL 落盘 (~/.claude-spy/logs/)
  ├─ Web UI 实时展示 (可选, --web-port)
  └─ 转发到上游 API
        │
        ▼
[Anthropic API / 企业网关]
```

## 构建

```bash
go build -o claude-spy .
```

需要 Go 1.23+，无外部依赖。

## 使用

### 1. 启动代理（实时模式）

```bash
claude-spy --upstream https://api.anthropic.com --port 8080
```

启动后输出：

```
[claude-spy] Upstream API: https://api.anthropic.com
[claude-spy] Proxy listening on http://0.0.0.0:8080
[claude-spy] Set ANTHROPIC_BASE_URL=http://<your-ip>:8080
[claude-spy] Logging to ~/.claude-spy/logs/20260327_153000_a1b2c3d4.jsonl
```

同时开启 Web UI（在浏览器实时查看每次请求）：

```bash
claude-spy --upstream https://api.anthropic.com --port 8080 --web-port 8081
```

启动后额外输出：

```
[claude-spy] Web UI:  http://localhost:8081
```

打开 `http://localhost:8081` 即可在浏览器中实时查看对话记录。

### 2. 在客户端机器上配置 Claude Code

```bash
export ANTHROPIC_BASE_URL=http://<proxy-host>:8080
claude
```

或写入 `~/.bashrc` / `~/.zshrc` 永久生效。

### 3. 浏览所有日志（serve 模式）⭐ 推荐

启动一个持久运行的 Web 服务，通过 URL 路径直接访问任意日志文件，无需每次指定文件路径：

```bash
claude-spy serve
```

默认监听 `8888` 端口，读取 `~/.claude-spy/logs/` 目录：

```
[claude-spy] Serving logs from ~/.claude-spy/logs/
[claude-spy] Web UI: http://localhost:8888
[claude-spy] Press Ctrl+C to exit
```

打开 `http://localhost:8888` 即可：

- 看到所有日志文件列表，按时间倒序排列，显示文件大小
- 点击任意文件直接查看，URL 形如 `http://localhost:8888/20260327_153000_a1b2c3d4.jsonl`
- 查看页面顶部有「← 返回列表」链接

也可指定端口和目录：

```bash
claude-spy serve --port 9000 --log-dir /path/to/logs
```

### 4. 回顾单个日志（view 模式）

```bash
claude-spy view ~/.claude-spy/logs/20260327_153000_a1b2c3d4.jsonl
```

默认随机端口，也可以指定：

```bash
claude-spy view ~/.claude-spy/logs/20260327_153000_a1b2c3d4.jsonl --port 9090
```

启动后输出：

```
[claude-spy] Viewing: 20260327_153000_a1b2c3d4.jsonl
[claude-spy] Web UI:  http://localhost:9090
[claude-spy] Press Ctrl+C to exit
```

## Web UI

Web UI 以深色主题展示每条请求/响应，**单条 JSON 再长也一目了然**：

- **时间轴瀑布流**：每条请求默认折叠为摘要行（时间、耗时、token 用量、stop reason），点击展开
- **消息气泡流**：展开后按对话顺序显示所有 messages，角色用颜色区分：
  - 🟠 `user` — 橙色
  - 🟣 `assistant` — 紫色
  - 🟢 `tool_use`（工具调用）— 绿色边框，显示工具名和参数
  - ⚫ `tool_result`（工具返回）— 灰色
- **长内容折叠**：tool_result 超过 5 行、文本超过 10 行自动折叠，点击展开
- **响应摘要**：底部显示 stop reason、耗时、in/out/cache_create/cache_read token 用量
- **实时更新**：实时模式下新请求从底部滑入，顶栏显示绿色实时指示

## 参数

### 代理模式

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `--upstream <url>` | `$CLAUDE_SPY_UPSTREAM` | 上游 API 完整 base URL（必填） |
| `--port <n>` | `8080` | 代理监听端口 |
| `--web-port <n>` | 不启用 | Web UI 端口（启用实时查看） |
| `--quiet` | false | 关闭终端实时摘要，只保留日志文件 |
| `--save-sse` | false | 额外保存原始 SSE 事件 |
| `--log-dir <dir>` | `~/.claude-spy/logs` | 日志目录 |

也可通过环境变量提供上游地址：

```bash
export CLAUDE_SPY_UPSTREAM=https://api.anthropic.com
claude-spy --port 8080
```

### serve 子命令

```bash
claude-spy serve [--port <n>] [--log-dir <dir>]
```

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `--port <n>` | `8888` | Web UI 端口 |
| `--log-dir <dir>` | `~/.claude-spy/logs` | 日志目录 |

### view 子命令

```bash
claude-spy view <file.jsonl> [--port <n>]
```

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `<file.jsonl>` | 必填 | 要查看的 JSONL 日志文件路径 |
| `--port <n>` | 随机 | Web UI 端口 |

## 日志格式

每个 session 生成一个 JSONL 文件，每条记录包含一次完整的请求/响应对：

```json
{
  "id": "req_001",
  "timestamp": "2026-03-27T15:30:00.123Z",
  "duration_ms": 3200,
  "request": {
    "method": "POST",
    "path": "/v1/messages",
    "headers": { "x-api-key": "***MASKED***" },
    "body": { "model": "...", "messages": [...], "tools": [...] }
  },
  "response": {
    "status": 200,
    "body": { "content": [...], "usage": { "input_tokens": 52103, "output_tokens": 387 } }
  }
}
```

API key 等敏感 header 自动脱敏为 `***MASKED***`。

## 终止

按 `Ctrl+C` 优雅退出，打印 session 统计汇总：

```
══════════ SESSION SUMMARY ══════════
  Requests:  23
  Duration:  12m 34s
  Tokens:    1,234,567 in / 45,678 out
  Cost(est): $1.23
  Log file:  ~/.claude-spy/logs/...jsonl
═════════════════════════════════════
```

## 安全说明

服务绑定 `0.0.0.0`，仅应在受信任的内网环境中使用。代理将客户端的 `x-api-key` 等认证头原样转发给上游，不提供代理层鉴权。
