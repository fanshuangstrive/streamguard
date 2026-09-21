# StreamGuard

> Windows 本地大模型 API 限流代理 —— 令牌桶排队等待，路径原样转发，自动适配 SSE 流式与普通 JSON 响应，无 Redis 依赖。

[![Go Version](https://img.shields.io/badge/Go-1.25+-00ADD8?logo=go)](https://go.dev/)
[![Platform](https://img.shields.io/badge/Platform-Windows-0078D6?logo=windows)](https://www.microsoft.com/windows)
[![License](https://img.shields.io/badge/License-MIT-green)](./LICENSE)
[![CI](https://github.com/streamguard/streamguard/actions/workflows/ci.yml/badge.svg)](https://github.com/streamguard/streamguard/actions/workflows/ci.yml)

## 简介

StreamGuard 是一个跑在 Windows 本机的轻量代理，位于你的客户端程序与上游大模型服务之间：

```
客户端程序  →  StreamGuard (127.0.0.1:8080)  →  上游模型服务
                    ↓
              限流排队（1 秒 1 次）
```

**核心特性：**

- **等待式限流**：请求超限时**排队等待**而非直接拒绝，平滑控制调用速率（如 1 秒 1 次）
- **路径原样转发**：客户端请求路径直接拼接到上游地址，无需配置路由规则
- **协议自动适配**：SSE 流式响应逐块实时透传，普通 JSON 响应正常缓冲，无需配置
- **零外部依赖**：纯内存实现，不需要 Redis，单文件 exe 直接运行
- **透明转发**：不修改请求内容，客户端请求头（含 `Authorization`、自定义头）与请求体原样透传
- **OpenAI 兼容**：完全对齐 `/v1/chat/completions` 协议，客户端无需改造
- **双模式**：既可作为命令行程序运行，也提供 Wails 桌面界面（可视化配置 + 实时状态）

## 快速开始

### 1. 准备配置

复制示例配置并填入你的真实信息：

```bash
copy config.example.json config.local.json
```

编辑 `config.local.json`：

```json
{
  "listen": "127.0.0.1:8080",
  "upstream": "https://your-upstream-host/base-path",
  "preserve_host": false,
  "rate": 1,
  "burst": 1,
  "max_wait": "30s",
  "timeout": "120s",
  "log_level": "info",
  "log_file": "",
  "log_retain_days": 7,
  "breaker_enabled": false,
  "breaker_threshold": 5,
  "breaker_cooldown": "30s",
  "retry_enabled": false,
  "retry_max_attempts": 3,
  "retry_initial_wait": "1s",
  "retry_max_wait": "10s",
  "size_limit_enabled": false,
  "size_limit_threshold": 10000,
  "size_limit_small_concurrent": 0,
  "size_limit_large_concurrent": 0
}
```

> ⚠️ `config.local.json` 已在 `.gitignore` 中排除，**切勿提交到仓库**。

### 2. 编译

**推荐：使用构建脚本**（产物统一输出到**仓库根目录**，方便直接运行）

```powershell
# 构建 CLI + GUI（含测试与静态检查）
.\scripts\build.ps1 -Version "1.0.0"

# 只构建 CLI
.\scripts\build.ps1 -Target cli

# 只构建 GUI
.\scripts\build.ps1 -Target gui

# 跳过测试快速构建
.\scripts\build.ps1 -SkipTest
```

构建完成后根目录得到：

| 文件 | 说明 |
| --- | --- |
| `streamguard.exe` | 命令行版 |
| `streamguard-gui.exe` | 桌面版 |

> 📌 **产物规范**：所有可执行文件输出到仓库根目录，通过 `.gitignore` 的 `*.exe` 统一忽略，**不提交 git**。

**手动编译**

```bash
# CLI
GOOS=windows GOARCH=amd64 go build -o streamguard.exe ./cmd/streamguard

# GUI（需先安装 Wails CLI）
cd cmd/streamguard-gui
wails build -platform windows/amd64
# 产物在 build/bin/streamguard-gui.exe，可复制到根目录
```

### 3. 运行

**命令行模式**（适合脚本、服务化）：

```bash
.\streamguard.exe -config config.local.json
```

**桌面界面模式**（适合日常使用，可视化配置）：

```bash
# 自动加载仓库根目录的 config.local.json（与 CLI 一致）
.\streamguard-gui.exe

# 或显式指定配置文件
.\streamguard-gui.exe -config config.local.json
```

桌面版提供图形化配置界面、实时状态卡片与日志面板，无需手写 JSON。

> **配置查找规则**：GUI 与 CLI 一致，按 `-config` 参数 → 当前目录 → exe 目录 → exe 上级目录
> 的顺序查找 `config.local.json`；都找不到时才回退到 `%AppData%\StreamGuard\config.json`。
> 详见 [docs/configuration.md](./docs/configuration.md#配置文件查找规则)。

**调试：打印完整请求/响应内容**

将 `log_level` 设为 `debug`，即可打印请求头、请求体、响应头与响应体（SSE 逐块实时打印）：

```bash
# 临时开启（环境变量，无需改配置）
$env:STREAMGUARD_LOG_LEVEL="debug"; .\streamguard.exe -config config.local.json
```

```
[req] >>> POST /v1/chat/completions HTTP/1.1
[req] Authorization: Bearer ***
[req] body (58 bytes): {"model":"...","stream":true}
[resp] <<< 200 OK
[resp] chunk#1 (32 bytes): data: {"choices":[...]}
[resp] chunk#2 (14 bytes): data: [DONE]
[resp] <<< done: status=200 chunks=2 bytes=46
```

输出位置由 `log_file` 决定：

| `log_file` | 输出位置 |
|---|---|
| `""`（默认） | 控制台 |
| `"auto"` | `<当前目录>/logs/streamguard-YYYYMMDD.log`（**GUI 场景推荐**） |
| 相对路径 | `<当前目录>/logs/<文件名>` |
| 绝对路径 | 指定文件 |

> ⚠️ 详细日志含 `Authorization` 等敏感信息，仅用于本地调试。
> GUI 双击启动无控制台，请设置 `log_file: "auto"`。
> 详见 [docs/configuration.md](./docs/configuration.md#详细日志调试请求响应内容)。

启动后输出：

```
   _____ __             __  ______                     __
  / ___// /__________ _/ / / ____/___  __  ___________/ /
  \__ \/ __/ ___/ __ / / / / __/ __ \/ / / / ___/ __  /
 ___/ / /_/ /  / /_/ / / / /_/ / /_/ / /_/ / /  / /_/ /
/____/\__/_/   \__,_/_/  \____/\____/\__,_/_/   \__,_/

  Windows 本地大模型 API 限流代理  vdev
2026/09/20 10:00:00 配置加载完成: listen=127.0.0.1:8080 ...
2026/09/20 10:00:00 限流策略: 等待式，1.00 请求/秒，突发 1，最长等待 30s
2026/09/20 10:00:00 StreamGuard 已启动，监听 127.0.0.1:8080
```

### 4. 调用

把客户端原本指向上游的地址改为本地代理地址即可：

```bash
curl http://127.0.0.1:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "your-model-name",
    "messages": [{"role": "user", "content": "你好"}],
    "stream": true
  }'
```

无需携带 `Authorization` 头，StreamGuard 会自动注入。

## 限流行为

StreamGuard 采用**等待式限流**（令牌桶 + 阻塞排队）：

| 场景 | 行为 |
|---|---|
| 有可用配额 | 立即转发，无额外延迟 |
| 配额耗尽 | **阻塞等待**，直到获得配额再转发 |
| 等待超过 `max_wait` | 返回 `429`，OpenAI 兼容错误格式 |
| 客户端主动断开 | 立即释放等待，不占用配额 |

以 `rate=1, burst=1`（1 秒 1 次）为例：

```
请求1 ──立即通过──> 上游
请求2 ──等待1s────> 上游
请求3 ──等待1s────> 上游
```

## 配置项

| 字段 | 类型 | 默认值 | 说明 |
|---|---|---|---|
| `listen` | string | `127.0.0.1:8080` | 本地监听地址 |
| `upstream` | string | — | 上游模型服务基础地址 |
| `preserve_host` | bool | `false` | 是否保留客户端原始 `Host` 头 |
| `rate` | number | `1` | 限流速率（QPS） |
| `burst` | number | `1` | 突发容量 |
| `max_wait` | string | `30s` | 最大等待时长 |
| `timeout` | string | `120s` | 上游请求超时 |
| `log_level` | string | `info` | 日志级别，`debug` 时打印完整请求/响应内容 |
| `log_file` | string | `""` | 详细日志落盘路径，`auto` 写入当前目录 `logs/`，空则输出到控制台 |
| `log_retain_days` | number | `7` | 启动时清理超过该天数的日志文件，`0` 关闭清理 |
| `breaker_enabled` | bool | `false` | 上游连续失败达阈值后快速失败（503），冷却后半开探测 |
| `breaker_threshold` | number | `5` | 熔断阈值（连续失败次数） |
| `breaker_cooldown` | string | `30s` | 熔断冷却时长 |
| `retry_enabled` | bool | `false` | 上游返回 429/503 时等待后自动重试 |
| `retry_max_attempts` | number | `3` | 最大尝试次数（含首次） |
| `retry_initial_wait` | string | `1s` | 首次重试等待（指数退避起点） |
| `retry_max_wait` | string | `10s` | 单次等待上限 |
| `size_limit_enabled` | bool | `false` | 按估算 token 数分档限制并发（大请求保护） |
| `size_limit_threshold` | number | `10000` | 大小请求分界（估算 token） |
| `size_limit_small_concurrent` | number | `0` | 小请求并发上限，`0` 不限制 |
| `size_limit_large_concurrent` | number | `0` | 大请求并发上限，`0` 不限制 |

完整说明见 [docs/configuration.md](./docs/configuration.md)。

## HTTP 端点

| 端点 | 方法 | 说明 |
|---|---|---|
| `/healthz` | GET | 健康检查（本地处理，不转发） |
| `/stats` | GET | 限流与代理统计（本地处理，不转发） |
| 其他任意路径 | * | 原样转发到上游，如 `/v1/chat/completions`、`/model/v1/chat/completions` |

> StreamGuard 不做路径改写：客户端请求路径直接拼接到 `upstream` 后转发。

## 测试

```bash
go test ./...
```

共 **137** 个测试用例，覆盖配置加载、等待式限流、按请求大小分档的并发限流、反向代理与透传、HTTP 服务、应用核心层、上游熔断器、上游限流重试。

- 用例清单与端到端验证：[docs/testing.md](./docs/testing.md)
- 全链路测试规则（R1–R8）：[docs/test-rules.md](./docs/test-rules.md)

验证 SSE 实时透传（需有效 API Key）：

```powershell
.\scripts\test-sse.ps1 -ApiKey <your-api-key>
```

## 项目结构

```
streamguard/
├── cmd/
│   ├── streamguard/          # 命令行入口
│   │   └── main.go           # 配置加载、服务启动、优雅退出
│   └── streamguard-gui/      # 桌面版（Wails）
│       ├── main.go           # Wails 应用配置
│       ├── app.go            # 前后端绑定层
│       ├── wails.json        # Wails 项目配置
│       └── frontend/         # 前端界面（原生 HTML/CSS/JS）
├── internal/
│   ├── app/                  # 应用核心层（CLI 与 GUI 共用）
│   │   └── app.go            # 服务生命周期管理
│   ├── config/               # 配置加载与校验
│   │   ├── config.go
│   │   └── duration.go       # 支持 "1s" 字符串的时长类型
│   ├── limiter/              # 限流器
│   │   └── waiter.go         # 等待式限流（令牌桶 + FIFO 排队）
│   ├── proxy/                # 反向代理（路径原样转发 + SSE 透传）
│   │   └── proxy.go
│   └── server/               # HTTP 服务
│       └── server.go
├── docs/                     # 文档
│   ├── configuration.md      # 配置说明
│   ├── ui-design.md          # 界面设计
│   ├── request-flow.md       # 全链路请求示例
│   ├── testing.md            # 测试用例与端到端验证
│   ├── test-rules.md         # 全链路测试规则
│   ├── development-log.md    # 开发归档
│   ├── development-guide.md  # 功能开发规范（新增功能必读）
│   ├── upstream-models.md    # 上游模型清单（模板）
│   └── local/                # 本地内部文档（不提交）
├── scripts/                  # 辅助脚本
│   ├── build.ps1             # 统一构建脚本（产物输出到仓库根目录）
│   ├── test-sse.ps1          # SSE 实时性验证脚本
│   └── gen_icon.go           # 图标生成脚本
├── streamguard.exe           # CLI 版（编译产物，不提交）
├── streamguard-gui.exe       # 桌面版（编译产物，不提交）
├── config.example.json       # 配置模板（可提交）
├── config.local.json         # 本地配置（不提交）
└── .gitignore
```

### 架构分层

```
        ┌─────────────────┐   ┌─────────────────┐
        │  CLI 入口        │   │  GUI 入口        │
        │  cmd/streamguard │   │  cmd/...-gui     │
        └────────┬────────┘   └────────┬────────┘
                 │                     │
                 └──────────┬──────────┘
                            ▼
                 ┌─────────────────────┐
                 │  internal/app       │  ← 核心层：生命周期管理
                 │  Start/Stop/Stats   │
                 └──────────┬──────────┘
                            ▼
        ┌───────────┬───────────┬───────────┐
        │  config   │  limiter  │  proxy    │
        └───────────┴───────────┴───────────┘
                            ▼
                 ┌─────────────────────┐
                 │  internal/server    │  ← HTTP 服务
                 └─────────────────────┘
```

`internal/app` 把「服务如何运行」与「如何被调用」解耦，CLI 与 GUI 共享同一套核心逻辑。

## 技术栈

| 层面 | 技术 | 说明 |
|---|---|---|
| 语言 | Go 1.25+ | 唯一后端语言，静态编译单文件 exe |
| HTTP 服务 | 标准库 `net/http` | 反向代理、SSE 透传，无第三方 Web 框架 |
| 限流 | 自研令牌桶 + 并发信号量 | 标准库并发原语实现，令牌桶 + FIFO 排队，纯内存 |
| 桌面 GUI | [Wails v2](https://wails.io/) | Go + Web 前端混合桌面应用，无边框窗口 |
| GUI 前端 | 原生 HTML/CSS/JS + [Vite](https://vitejs.dev/) | 零框架、零 npm 运行时依赖，构建仅需 Vite |
| 配置 | JSON + 环境变量（`STREAMGUARD_` 前缀） | 标准库 `encoding/json`，支持热更新 |
| 测试 | 标准库 `testing` | 156 个单元测试，含 SSE 实时性端到端验证 |

> **依赖原则**：除 Wails（GUI 必需）外，运行时零第三方依赖；CLI 版仅依赖 Go 标准库。

## 开发

本项目采用**测试驱动开发（TDD）**，每个模块先写测试再实现。

> 📖 **新增功能必读**：[docs/development-guide.md](./docs/development-guide.md) —— 架构分层、代码规范、配置项九处同步、测试与提交规范。

```bash
# 运行全部测试
go test ./...

# 查看覆盖率
go test ./... -cover

# 生成覆盖率报告
go test ./... -coverprofile=coverage.out
go tool cover -html=coverage.out
```

### 测试覆盖

| 模块 | 测试文件 | 用例数 |
|---|---|---|
| `cmd/streamguard` | `main_test.go` | 7 |
| `limiter` | `waiter_test.go` | 12 |
| `limiter` | `concurrency_test.go` | 12 |
| `limiter` | `tokens_test.go` | 11 |
| `config` | `config_test.go` | 19 |
| `proxy` | `proxy_test.go` | 7 |
| `proxy` | `header_test.go` | 4 |
| `proxy` | `host_test.go` | 3 |
| `proxy` | `breaker_test.go` | 6 |
| `proxy` | `retry_test.go` | 7 |
| `proxy` | `timeout_test.go` | 5 |
| `proxy` | `sse_realtime_test.go` | 1 |
| `server` | `server_test.go` | 17 |
| `server` | `verbose_test.go` | 5 |
| `app` | `app_test.go` | 10 |
| `app` | `verbose_log_test.go` | 8 |
| `app` | `log_retention_test.go` | 3 |
| `breaker` | `breaker_test.go` | 8 |
| `retry` | `retry_test.go` | 11 |

合计 **156** 个测试用例。

## 安全约定

- **敏感信息隔离**：真实上游地址只放在 `config.local.json`，已被 `.gitignore` 排除
- **仅监听回环**：默认绑定 `127.0.0.1`，不暴露到局域网
- **透明转发**：不修改请求内容，客户端凭据与模型名原样透传
- **提交前检查**：提交前请确认 `git status` 中不含 `config.local.json` 与 `docs/local/`

## 后续规划

- [x] 桌面界面（Wails，可视化配置 + 实时状态）
- [ ] 系统托盘图标（最小化到托盘、快捷启停）
- [ ] 可视化监控面板（QPS 曲线、等待分布）
- [ ] 动态限流规则（运行时调整，无需重启）
- [ ] Prometheus 指标暴露

## License

MIT
