# claude-spy 设计文档

## 概述

一个 Golang 工具，通过本地反向代理拦截 Claude Code 与 Anthropic API 之间的所有 HTTP 通信，实时在终端展示交互摘要，并将完整的请求/响应数据存储到 JSONL 文件。

## 目标

- 看到每次发给模型的**完整 input**（system prompt、messages、tools 定义）
- 看到模型返回的**完整 output**（text、tool_use、usage）
- 实时终端摘要 + 完整数据落盘，兼顾即时观察和事后分析
- 对用户透明，使用体验与直接用 claude 一致

## 架构

```
┌─────────────────────────────────────────────────┐
│  claude-spy                                       │
│                                                   │
│  1. 启动 Go reverse proxy (localhost:PORT)        │
│  2. 设置 ANTHROPIC_BASE_URL=http://localhost:PORT │
│  3. exec claude-internal (子进程，继承环境变量)     │
│                                                   │
│  claude-code 发请求 ──→ proxy ──→ 真实API         │
│                           │                       │
│                           ├─ 终端实时打印摘要      │
│                           └─ 完整数据写入 JSONL    │
│                                                   │
│  claude-code 退出 → proxy 自动关闭 → 打印统计汇总  │
└─────────────────────────────────────────────────┘
```

### 方案选型

选择**正向反向代理方案（方案 A）**，而非 eBPF/ptrace 或 MITM 透明代理：

- Claude Code 原生支持 `ANTHROPIC_BASE_URL` 环境变量
- 代理对 claude-code 暴露 HTTP，无需处理 TLS 证书
- 核心代码量约 300 行 vs eBPF 方案的 2000+ 行
- 普通用户权限即可运行

## 生命周期

1. 用户执行 `claude-spy [claude的参数...]`
2. 工具找一个可用端口，启动 HTTP reverse proxy
3. 设置 `ANTHROPIC_BASE_URL=http://localhost:<PORT>`，启动 `claude-internal` 作为子进程，透传所有命令行参数
4. 代理拦截所有请求/响应，实时打印摘要 + 写入 JSONL
5. `claude-internal` 退出后，proxy 关闭，打印 session 统计汇总

信号处理：Ctrl+C 等信号正确传递给 claude 子进程。

## 代理核心：请求/响应处理

### 请求流

```
Claude Code 发送 POST /v1/messages
    │
    ▼
Proxy 收到请求
    ├─ 读取完整 request body（JSON）
    ├─ 解析：model, system, messages[], tools[]
    ├─ 生成请求 ID（时间戳+序号）
    ├─ 终端打印请求摘要
    ├─ 转发到真实 API（加回原始 headers）
    │
    ▼
API 返回 SSE stream
    ├─ 逐 chunk 转发给 Claude Code（不阻塞）
    ├─ 同时复制一份累积完整 response
    ├─ stream 结束后：终端打印响应摘要
    └─ 将 request + response 写入 JSONL
```

### SSE streaming 处理

- Claude API 返回 `Content-Type: text/event-stream`
- 必须**边收边转发**，不能等完整后再返回（否则 claude-code 超时）
- 用 `io.TeeReader` 模式：一路写回给 client，一路写到 buffer 累积
- stream 结束后从 buffer 解析完整内容，拼出最终的 message 对象

### 非 streaming 请求

- 直接读完 response body，记录后返回
- 健康检查等非 `/v1/messages` 路径直接透传，不记录

## 终端实时摘要格式

全部输出到 **stderr**（claude-code 占用 stdout）。用 ANSI 颜色区分。

### 请求摘要（发出时打印）

```
━━━ REQ #3 ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
  Model:    claude-4.6-opus
  System:   3,241 chars
  Messages: 12 条 (user:5, assistant:5, tool_result:2)
  Tools:    15 个 [Bash, Read, Edit, Grep, Glob, ...]
  Tokens(est): ~52,000 input
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
```

### 响应摘要（stream 结束时打印）

```
━━━ RES #3 ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
  Duration: 3.2s
  Output:   text(245 chars) + tool_use(Bash)
  Stop:     tool_use
  Tokens:   52,103 in / 387 out / 0 cache_create / 48,200 cache_read
  Cost(est): $0.032
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
```

### Session 结束汇总

```
══════════ SESSION SUMMARY ══════════
  Requests:  23
  Duration:  12m 34s
  Tokens:    1,234,567 in / 45,678 out
  Cost(est): $1.23
  Log file:  ~/.claude-spy/logs/2026-03-26_session_abc123.jsonl
═════════════════════════════════════
```

### 摘要配置

- 用 ANSI 颜色：请求蓝色，响应绿色，错误红色
- 费用按公开定价估算（可配置单价）
- `--quiet` 参数关闭实时摘要，只保留文件记录

## JSONL 存储格式

### 文件位置

`~/.claude-spy/logs/<session-id>.jsonl`

### 记录结构

每条记录是一次完整的 API 请求/响应对：

```json
{
  "id": "req_003",
  "timestamp": "2026-03-26T15:30:00.123Z",
  "duration_ms": 3200,
  "request": {
    "method": "POST",
    "path": "/v1/messages",
    "headers": { "x-api-key": "***MASKED***" },
    "body": {
      "model": "claude-4.6-opus",
      "max_tokens": 16384,
      "system": "完整system prompt",
      "messages": ["完整messages数组"],
      "tools": ["完整tools定义"],
      "stream": true
    }
  },
  "response": {
    "status": 200,
    "headers": {},
    "body": {
      "id": "msg_xxx",
      "type": "message",
      "role": "assistant",
      "model": "claude-4.6-opus",
      "content": [],
      "stop_reason": "tool_use",
      "usage": {
        "input_tokens": 52103,
        "output_tokens": 387
      }
    }
  }
}
```

### 存储要点

- `request.body` 和 `response.body` 存完整原始 JSON
- SSE streaming response：从 SSE 事件中拼出最终完整 message 对象存入 `response.body`
- `--save-sse` 开启时额外保存原始 SSE 事件序列到 `response.sse_events`
- headers 中 API key 自动脱敏为 `***MASKED***`
- 每个 session 一个文件，文件名用 session 启动时间 + 短 ID

## 项目结构

```
claude-spy/
├── main.go              # 入口：参数解析、启动流程编排
├── proxy/
│   ├── server.go        # HTTP server 启动/关闭
│   ├── handler.go       # 请求处理：拦截、转发、记录
│   └── sse.go           # SSE stream 解析与边转发边累积
├── recorder/
│   ├── recorder.go      # 记录器接口：接收 request/response 对
│   ├── jsonl_writer.go  # JSONL 文件写入
│   └── masker.go        # 敏感信息脱敏
├── display/
│   ├── printer.go       # 终端摘要打印（stderr、ANSI 颜色）
│   └── summary.go       # Session 结束汇总统计
├── launcher/
│   └── launcher.go      # 子进程管理：启动 claude、信号传递、等待退出
└── go.mod
```

### 模块职责

| 模块 | 职责 | 依赖 |
|------|------|------|
| `main.go` | 解析参数、编排启动顺序、优雅退出 | 所有模块 |
| `proxy` | HTTP 反向代理核心，处理 SSE streaming | `recorder`, `display` |
| `recorder` | 数据持久化，写 JSONL，脱敏 | 无 |
| `display` | 终端实时输出，Session 统计 | 无 |
| `launcher` | 管理 claude 子进程生命周期 | 无 |

### 外部依赖

仅用 Go 标准库：`net/http`, `net/http/httputil`, `os/exec`, `encoding/json`, `io`。不引入第三方依赖，`go build` 产出单一二进制。

## CLI 接口

```bash
# 基本使用（替代 claude 命令）
claude-spy [claude的所有参数...]

# 示例
claude-spy                          # 等同于 claude
claude-spy --continue               # 等同于 claude --continue
claude-spy -p "hello"               # 等同于 claude -p "hello"

# claude-spy 自身参数（放在 -- 之前）
claude-spy --quiet -- --continue    # 安静模式
claude-spy --save-sse -- -p "hi"   # 保存原始SSE事件
claude-spy --port 9090 --           # 指定代理端口
```

### claude-spy 参数

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `--port` | 自动选择 | 代理监听端口 |
| `--quiet` | false | 关闭终端实时摘要 |
| `--save-sse` | false | 额外保存原始 SSE 事件序列 |
| `--log-dir` | `~/.claude-spy/logs` | 日志存储目录 |

## 错误处理

- 代理启动失败（端口占用）：自动重试其他端口，最多 3 次
- API 转发失败（网络错误）：返回 502 给 claude-code，记录错误到 JSONL
- JSONL 写入失败（磁盘满等）：stderr 打印警告，不中断代理服务
- 子进程异常退出：记录退出码，正常关闭代理并打印已有统计

## 边界约束

- 仅拦截 `/v1/messages` 路径的 POST 请求，其他路径透传不记录
- request body 和 response body 以 `json.RawMessage` 方式存储，不做结构校验，保证向前兼容
- 代理不修改任何请求/响应内容，纯旁路记录
- 单次 request body 上限不做限制（由 Go 的内存管理处理），实测单次请求通常 < 5MB
- session ID 格式：`YYYYMMDD_HHMMSS_<8位随机hex>`，如 `20260326_153000_a1b2c3d4`
