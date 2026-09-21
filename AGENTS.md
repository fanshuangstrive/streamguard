# AGENTS.md — AI 协作规范

本文件是 AI 助手在本仓库工作时**必须遵守**的入口规范。

## 必读文档

新增任何功能前，先完整阅读并遵守：

- **[docs/development-guide.md](./docs/development-guide.md)** — 功能开发规范（架构分层、代码规范、配置项九处同步、TDD 测试、Git 提交规范、自查清单）
- [docs/test-rules.md](./docs/test-rules.md) — R1–R8 链路规则（改动不得破坏）
- [docs/configuration.md](./docs/configuration.md) — 配置项说明
- [docs/development-log.md](./docs/development-log.md) — 历史决策与踩坑记录

## 目录结构规范

新文件必须放入对应目录，禁止在根目录堆放源码：

```
├── cmd/
│   ├── streamguard/          # CLI 入口（仅参数解析、信号处理）
│   └── streamguard-gui/      # GUI 入口（Wails 绑定层，仅转发与格式转换）
│       └── frontend/         # 前端（原生 HTML/CSS/JS，零框架）
├── internal/                 # 所有业务逻辑（禁止被外部项目 import）
│   ├── app/                  # 生命周期管理（CLI/GUI 共用）
│   ├── config/               # 配置加载、校验、环境变量
│   ├── limiter/              # 限流（令牌桶 + FIFO）
│   ├── proxy/                # 反向代理、SSE 透传
│   └── server/               # HTTP 服务、路由、日志
├── docs/                     # 项目文档（内部笔记放 docs/local/，不提交）
├── scripts/                  # 构建/测试脚本（PowerShell 纯 ASCII）
└── 根目录                    # 仅允许：go.mod、README、AGENTS.md、配置模板、.gitignore
```

放置规则：

- 新增**可复用逻辑** → `internal/<领域>/`，按领域建包，不建 `utils`/`common` 杂物包
- 新增**入口** → `cmd/<名称>/main.go`，保持薄
- 新增**文档** → `docs/`，命名小写中划线（如 `xxx-guide.md`）
- 新增**脚本** → `scripts/`，PowerShell 纯 ASCII
- **测试文件** → 与被测包同目录 `xxx_test.go`
- 根目录禁止新增源码、临时文件；编译产物（`*.exe`）不提交

## 核心红线（违反即返工）

1. **架构**：业务逻辑只写 `internal/`，入口层（`cmd/`）保持薄；CLI/GUI 共用逻辑下沉 `internal/app`
2. **依赖**：运行时零第三方依赖（GUI 仅 Wails）；前端零框架
3. **配置**：新增配置字段必须九处同步（见 development-guide 第 4 节）
4. **测试**：TDD 先行；**新增功能必须全链路测试**（单元测试 + `go test ./... -count=1` + `go vet` + `gofmt -l .` + 构建 + 端到端冒烟 + 敏感扫描，见 development-guide 第 5 节，缺一不可）
5. **敏感信息**：禁止提交真实域名、API Key、`config.local.json`、`docs/local/`、`package-lock.json`、`*.exe`、`logs/`
6. **Git**：提交前必须列出文件清单 + commit message，**经用户确认后**才执行；项目初期不打 tag
7. **文档**：与代码同一提交
8. **跨平台**：CLI 三平台可编译；PowerShell 脚本纯 ASCII；不用 `go test -race`

## 提交前自查

完成功能后，按 [development-guide 第 10 节](./docs/development-guide.md#10-新增功能自查清单) 逐项打勾，全部通过后再向用户申请提交。
