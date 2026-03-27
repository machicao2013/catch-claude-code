# claude-spy 独立代理模式设计文档

## 背景与目标

现有 `claude-spy` 工具采用本机模式：binary patch + 本地代理 + 子进程管理。
目标是去掉启动 claude code 的逻辑，将其改造为一个可独立部署的 HTTP 反向代理服务，
由 Claude Code 通过 `ANTHROPIC_BASE_URL` 环境变量指向该代理。

## 架构

```
[Claude Code 客户端]
  ANTHROPIC_BASE_URL=http://<proxy-host>:8080
        │
        ▼
[claude-spy 独立代理服务]  <── 本次改造目标
  监听 0.0.0.0:PORT
  ├─ 拦截 /v1/messages
  ├─ 终端实时摘要 (stderr)
  ├─ JSONL 落盘
  └─ 转发到上游 API
        │
        ▼
[Anthropic API / 企业网关]
```

## 变更范围

### 删除

- `launcher/` 目录整体删除（`launcher.go` + `launcher_test.go`）
- `main.go` 中所有 launcher 相关代码：binary patch、子进程启动、信号转发

### 修改

**`proxy/server.go`**
- `NewServer` 绑定地址从 `127.0.0.1` 改为 `0.0.0.0`，支持接收外部连接

**`main.go`**
- 精简：只负责参数解析、启动代理、阻塞等待退出信号、打印 session 统计
- 移除所有 launcher 依赖

### 保持不变

- `proxy/handler.go`、`proxy/sse.go` — 请求拦截与转发核心逻辑
- `recorder/` — JSONL 落盘
- `display/` — 终端摘要

## CLI 接口

```bash
# 通过参数指定上游
claude-spy --upstream https://api.anthropic.com --port 8080

# 通过环境变量
CLAUDE_SPY_UPSTREAM=https://api.anthropic.com claude-spy --port 8080
```

### 删除的旧参数

原 `main.go` 中以下参数随 launcher 逻辑一并删除（不再支持）：
- `--` 分隔符之后的 claude 子命令参数
- `CLAUDE_CODE_TEAMMATE_COMMAND` 环境变量

### 参数列表

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `--upstream` | `$CLAUDE_SPY_UPSTREAM` | 上游 API 完整 base URL，如 `https://api.anthropic.com`（必填） |
| `--port` | `8080` | 监听端口；`0` 表示随机选择可用端口 |
| `--quiet` | false | 关闭终端实时摘要 |
| `--save-sse` | false | 额外保存原始 SSE 事件至 `<log-dir>/<session-id>.sse.jsonl`；写入失败时 stderr 警告，不中断代理 |
| `--log-dir` | `~/.claude-spy/logs` | 日志目录 |

> **安全说明：** 服务绑定 `0.0.0.0`，仅应在受信任网络环境中使用。代理会将客户端的 `x-api-key` 等认证头原样转发给上游，无代理层鉴权。

### 启动输出示例

```
[claude-spy] Upstream API: https://api.anthropic.com
[claude-spy] Proxy listening on http://0.0.0.0:8080
[claude-spy] Set ANTHROPIC_BASE_URL=http://<your-ip>:8080
[claude-spy] Logging to ~/.claude-spy/logs/20260327_153000_a1b2c3d4.jsonl
```

## 生命周期

1. 解析参数，确认 `--upstream` 已提供且能被 `url.Parse` 解析为合法 URL（仅做语法校验，不做连接探测）；否则打印错误退出
2. 创建日志目录（如不存在），创建 JSONL recorder；初始化 display printer（`--quiet` 时 printer 静默输出）
3. 启动 HTTP proxy server（绑定 `0.0.0.0:PORT`，默认 8080）
4. 打印监听地址并提示用户配置 `ANTHROPIC_BASE_URL`（无论 `--quiet` 是否开启，此启动信息始终打印；`--quiet` 仅抑制后续请求的实时摘要）
5. 阻塞，监听 `SIGINT` / `SIGTERM`
6. 收到信号后执行优雅关闭，超时 10s 后强制退出；打印 session 统计汇总（内容：总请求数、总耗时、input/output tokens 合计、估算费用、日志文件路径）

## 协议分析

无协议变更。本次改造不涉及任何 JCE/RPC 协议修改，仅调整 Go 程序的运行模式。
代理转发逻辑（`/v1/messages` HTTP + SSE streaming）保持不变。

## 错误处理

| 场景 | 处理 |
|------|------|
| `--upstream` 未提供 | 打印错误信息，返回 exit code 1 |
| 端口绑定失败（指定端口）| 打印错误信息，返回 exit code 1；不自动重试，端口冲突需用户手动解决 |
| 端口=0（随机分配）| 由 OS 分配可用端口，不重试 |
| 上游连接失败 | 返回 502 给 Claude Code，记录到 JSONL |
| JSONL 写入失败 | stderr 打印警告，不中断代理 |
| 优雅关闭超时（10s）| 强制退出，仍打印已有统计 |
