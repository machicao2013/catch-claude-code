# claude-spy

一个独立的 HTTP 反向代理，部署在任意机器上，将 Claude Code 的 API 请求转发到 Anthropic API（或企业网关），同时实时打印请求摘要并将完整数据写入 JSONL 日志。

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

### 1. 在代理机器上启动

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

### 2. 在客户端机器上配置 Claude Code

```bash
export ANTHROPIC_BASE_URL=http://<proxy-host>:8080
claude
```

或写入 `~/.bashrc` / `~/.zshrc` 永久生效。

## 参数

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `--upstream <url>` | `$CLAUDE_SPY_UPSTREAM` | 上游 API 完整 base URL（必填） |
| `--port <n>` | `8080` | 监听端口 |
| `--quiet` | false | 关闭实时摘要，只保留日志文件 |
| `--save-sse` | false | 额外保存原始 SSE 事件 |
| `--log-dir <dir>` | `~/.claude-spy/logs` | 日志目录 |

也可通过环境变量提供上游地址：

```bash
export CLAUDE_SPY_UPSTREAM=https://api.anthropic.com
claude-spy --port 8080
```

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
