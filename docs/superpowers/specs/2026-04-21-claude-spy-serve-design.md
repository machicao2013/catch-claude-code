# claude-spy serve 命令设计

**日期**: 2026-04-21  
**状态**: 待实现

## 背景

现有 `claude-spy view <file.jsonl> [--port <n>]` 命令每次需要指定文件路径和端口，用完即退出，使用不便。目标是新增一个持久运行的 web 服务，通过 URL 路径直接访问对应的 JSONL 文件。

---

## 命令接口

```
claude-spy serve [--port <n>] [--log-dir <dir>]
```

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `--port` | `8888` | 监听端口 |
| `--log-dir` | `~/.claude-spy/logs/` | 日志目录 |

- 启动后持久运行，Ctrl+C 退出
- 启动时打印：`[claude-spy] Serving logs from ~/.claude-spy/logs/ at http://localhost:8888`

---

## 路由设计

| 路径 | 行为 |
|------|------|
| `GET /` | 文件列表页，按修改时间倒序，最多显示 100 条 |
| `GET /:filename` | 加载对应 JSONL，渲染 viewer UI（复用现有页面） |
| `GET /api/files` | 返回文件列表 JSON（供列表页前端调用） |
| `GET /api/records?file=:filename` | 返回指定文件的 records JSON |
| `/static/*` | 静态资源（复用现有） |

**路由规则：**
- `/:filename` 只接受 `.jsonl` 后缀，其他一律 404
- 访问不存在的文件时：渲染文件列表页 + 顶部红色提示条"文件 `xxx.jsonl` 不存在"

---

## webui.Server 扩展

### 新增 Mode

```go
ModeServe Mode = "serve"
```

### Server 结构体新增字段

```go
logDir string  // serve 模式：日志根目录
```

### 请求处理逻辑

每次请求实时读文件，无缓存，无额外状态：

- `GET /` → 扫描 `logDir`，渲染文件列表页
- `GET /api/files` → 扫描 `logDir`，返回文件列表 JSON（按修改时间倒序，最多 100 条）
- `GET /:filename` → 直接读取 `logDir/filename`，加载 records，渲染 viewer UI
- `GET /api/records?file=:filename` → 直接读取文件，返回 records JSON

`/api/files` 返回字段：`filename`、`size`（字节数）、`mtime`（ISO 8601 时间字符串）

---

## 前端 UI

### 文件列表页（`/`）

- 复用现有 `index.html` 的整体风格（颜色、字体、布局）
- 列表每行显示：可读时间、文件名、文件大小
- 文件名格式 `20240421_123456_abcd.jsonl` 解析为可读时间，例如 `2024-04-21 12:34:56`
- 点击行跳转到 `/:filename`
- 文件不存在时，列表页顶部显示红色提示条

### viewer 页（`/:filename`）

- 完全复用现有 viewer UI，无需改动前端逻辑
- 页面顶部增加「← 返回列表」链接

---

## main.go 变更

新增 `runServe(args []string) int` 函数，在 `run()` 入口处新增分支：

```go
if len(os.Args) > 1 && os.Args[1] == "serve" {
    return runServe(os.Args[2:])
}
```

`printUsage()` 中新增 serve 子命令说明。

---

## 不在范围内

- 文件内容缓存（每次请求直接读磁盘，足够快）
- 文件上传、删除等写操作
- 认证鉴权
