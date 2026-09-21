# 功能开发规范

> 本文档是 StreamGuard 新增功能时**必须遵守**的规范。任何 AI 助手或开发者在本仓库新增功能前，应先阅读本文档，并在完成后逐项自查。

## 1. 项目定位与边界

StreamGuard 是**本地大模型 API 限流代理**：等待式限流、路径原样转发、SSE 流式透传、零外部依赖。

新增功能前先回答三个问题：

1. **是否符合定位**？只做「代理 + 限流 + 可观测」，不做业务逻辑、不缓存响应、不改写请求内容
2. **是否引入新依赖**？默认禁止；确需引入时必须说明理由（CLI 版运行时依赖应保持为零）
3. **是否影响现有链路**？参考 [test-rules.md](./test-rules.md) 的 R1–R8 链路规则，改动不得破坏任何一条

## 2. 架构分层约束

```
cmd/streamguard       CLI 入口（只做参数解析、信号处理，不含业务逻辑）
cmd/streamguard-gui   GUI 入口（Wails 绑定层，只做转发与格式转换）
internal/app          核心层（生命周期管理，CLI 与 GUI 共用）
internal/config       配置（加载、校验、环境变量覆盖）
internal/limiter      限流（令牌桶 + FIFO 排队）
internal/proxy        反向代理（路径拼接、SSE 透传）
internal/server       HTTP 服务（路由、限流编排、详细日志）
```

**规则：**

- 业务逻辑只允许写在 `internal/`，入口层（`cmd/`）保持薄
- CLI 与 GUI 共用的逻辑必须下沉到 `internal/app`，禁止在两个入口重复实现
- GUI 绑定层（`cmd/streamguard-gui/app.go`）只做「参数转换 + 调用 core + 日志记录」，不写业务判断
- `internal/` 各包之间禁止循环依赖；`limiter`/`proxy` 不得反向依赖 `server`/`app`

## 3. 代码规范

### 3.1 Go 代码

| 项 | 要求 |
|---|---|
| 语言版本 | Go 1.25+，不使用 build tag 之外的实验特性 |
| 注释 | 所有导出符号必须有中文 doc comment，说明「是什么 + 何时用 + 并发约束」 |
| 错误处理 | 用 `fmt.Errorf("...: %w", err)` 包装，禁止吞错（`_ =` 需注释原因） |
| 并发 | 共享状态必须由 `sync.Mutex/RWMutex` 保护；锁的持有范围尽量小；注释标明「调用方需持有 xx 锁」 |
| 日志 | 用标准库 `log`；格式 `[模块] 消息 key=value`，如 `[app] server started, listening on %s` |
| 命名 | 导出用英文，注释可中文；不使用拼音缩写 |

### 3.2 依赖原则

- **运行时零第三方依赖**（CLI）；GUI 仅允许 Wails
- 新增 import 前先确认 `go.mod` 是否已有等价能力
- 禁止引入：Web 框架、DI 容器、ORM、配置库（标准库 `encoding/json` 已够用）

### 3.3 前端（GUI）

- 原生 HTML/CSS/JS，**不引入任何前端框架**（React/Vue 等）
- 构建仅依赖 Vite；`package-lock.json` 已被 gitignore（含内网私服地址，禁止提交）
- 新增配置项时必须同步修改三处：`index.html`（输入框）、`main.js`（el/collectConfig/fillConfig）、`wailsjs/go/models.ts`（绑定字段）
- 界面文案用中文；深色主题，遵循 `style.css` 既有变量（`--panel`、`--accent` 等）
- 布局需自适应：使用 `minmax(0, 1fr)`，窄屏（<980px）单列堆叠，操作按钮 sticky 固定

## 4. 配置项新增规范

新增配置字段必须**九处同步**，缺一不可：

| # | 位置 | 内容 |
|---|---|---|
| 1 | `internal/config/config.go` | 字段定义 + JSON tag + 默认值 + `Normalize` 归一化 + `Validate` 校验 |
| 2 | 环境变量 | `STREAMGUARD_` 前缀，在 `ApplyEnv` 中支持覆盖 |
| 3 | `config.example.json` | 占位符形式的示例值 |
| 4 | `docs/configuration.md` | 配置项表格 + 详细说明 |
| 5 | `README.md` | JSON 示例 + 配置项简表 |
| 6 | GUI `index.html` | 输入框 + 说明文案 |
| 7 | GUI `main.js` | el 映射 + collectConfig + fillConfig |
| 8 | GUI `models.ts` | Wails 绑定字段 |
| 9 | 测试 | 默认值、归一化、校验、环境变量覆盖各至少 1 个用例 |

**命名约定**：JSON 字段用 `snake_case`；时长用字符串（`"30s"`），由 `internal/config/duration.go` 的 `Duration` 类型解析。

## 5. 测试规范（TDD）

**先写测试，再写实现。** 每个新功能必须包含：

### 5.1 单元测试

- 位置：与被测包同目录，`xxx_test.go`
- 命名：`Test被测对象_行为`，如 `TestWaiter_TimeoutReturns429`
- 覆盖点：正常路径、边界值、错误路径、并发安全（如适用）
- 不使用 `-race`（Windows 下 cygwin/mingw 冲突会误报）
- 测试不留垃圾文件：临时文件用 `t.TempDir()`；Windows 下注意句柄释放（先 Close 再删）

### 5.2 全链路验证（新增功能必做，缺一不可）

**任何新增功能都必须完整执行以下 5 步**，不允许只跑单元测试就提交：

```bash
# 1. 全量测试（强制重跑，不用缓存）
go test ./... -count=1

# 2. 静态检查
go vet ./...
gofmt -l .          # 输出必须为空

# 3. 构建
.\scripts\build.ps1 -Version "x.y.z"

# 4. 端到端冒烟
.\streamguard.exe -config config.local.json
# 另开终端：
curl http://127.0.0.1:8080/healthz        # 期望 {"status":"ok"}
curl http://127.0.0.1:8080/stats          # 期望返回限流统计

# 5. 敏感信息扫描（必须无输出；模式用字符类拆分，避免敏感词明文入库）
git grep -n -i -E "h[a]ier\.net|model[a]pi-test|api[k]ey-695f|695f046[7]" -- cmd/ internal/ scripts/ README.md docs/ config.example.json
```

### 5.3 链路规则回归

改动涉及代理/限流/SSE 时，必须回归 [test-rules.md](./test-rules.md) 的 R1–R8，重点：

- R2 路径原样转发（不被改写）
- R4 请求头透传（`Authorization` 原样）
- R6 SSE 逐块实时透传（不被缓冲）

## 6. 文档同步规范

新增功能必须同步更新文档，**文档与代码同一提交**：

| 文档 | 何时更新 |
|---|---|
| `README.md` | 用户可感知的功能（特性列表、配置表、使用方法） |
| `docs/configuration.md` | 任何配置项变化 |
| `docs/testing.md` | 新增/修改测试用例（用例数同步更新） |
| `docs/test-rules.md` | 新增链路规则 |
| `docs/development-log.md` | 重要决策、踩坑记录（追加，不改写历史） |
| `docs/ui-design.md` | GUI 界面变化 |

## 7. Git 提交规范

### 7.1 提交前检查清单

- [ ] `go test ./... -count=1` 全过
- [ ] `go vet ./...` 无输出
- [ ] `gofmt -l .` 无输出
- [ ] 敏感信息扫描无输出（见 5.2）
- [ ] `git status` 不含 `config.local.json`、`docs/local/`、`*.exe`、`logs/`、`package-lock.json`
- [ ] 文档已同步
- [ ] **已获得用户确认**（提交前必须列出文件清单 + commit message，经用户同意后执行）

### 7.2 Commit message

格式：`type: 中文摘要`，正文用列表说明要点。

```
feat: 新增 xxx 功能
fix: 修复 xxx 问题
docs: xxx 文档
chore: 构建/工具链调整
refactor: 重构（不改行为）
test: 测试补充
```

### 7.3 禁止提交的内容

| 内容 | 原因 |
|---|---|
| `config.local.json` | 含真实上游地址 |
| `docs/local/` | 内部模型清单 |
| `*.exe` / `logs/` | 构建产物与运行时数据 |
| `package-lock.json` | 含内网私服地址（nexus） |
| 任何真实域名、API Key、内网 IP | 安全红线 |

### 7.4 Tag 与发布

项目初期快速迭代，**默认不打 tag**；确需发布时先与用户确认版本号。

## 8. 跨平台规范

- CLI 必须保持三平台可编译（windows/darwin/linux），禁止引入平台专属 API；确需时用 `runtime.GOOS` 分支并注释
- 构建统一走 `scripts/build.ps1`，交叉编译用 `-OS darwin|linux`；PowerShell 脚本**纯 ASCII**（避免编码问题）
- GUI 仅支持当前 OS 构建（Wails 限制），非 windows 目标自动跳过
- 环境变量注意恢复：脚本中设置 `GOOS/GOARCH` 后必须还原，避免污染后续 `go test`
- 路径处理用 `filepath.Join` / `os.UserConfigDir()`，禁止硬编码 `\` 或 `%AppData%`

## 9. GUI 特有规范

- 详细日志（请求/响应内容）**不得进入界面日志面板**（避免刷屏与敏感信息展示），落盘走 `log_file`
- GUI 无控制台：所有用户需要看到的输出必须有界面呈现（日志面板 / 状态卡片 / 底部路径栏）
- 新增绑定方法三处同步：Go 方法 → `wailsjs/go/main/App.js` → `App.d.ts`
- 窗口默认最大化（`WindowStartState: options.Maximised`），布局自适应（见 3.3）
- 底部栏展示工作路径（配置文件 + 日志位置），方便用户查看或清理

## 10. 新增功能自查清单

完成后逐项打勾：

- [ ] 架构：逻辑在 `internal/`，入口层未膨胀，无循环依赖
- [ ] 依赖：未引入新的第三方依赖（或已说明理由并经确认）
- [ ] 代码：导出符号有中文注释，错误正确包装，并发安全
- [ ] 配置：九处同步（见第 4 节）
- [ ] 测试：TDD 先行，单元测试覆盖正常/边界/错误路径
- [ ] 全链路：5.2 的 5 步全部通过（新增功能缺一不可）
- [ ] 链路规则：R1–R8 未被破坏
- [ ] 文档：与代码同一提交
- [ ] Git：检查清单（7.1）全过，用户已确认提交
- [ ] 跨平台：CLI 三平台可编译，脚本纯 ASCII

## 11. 新增功能全流程示例（结合本项目）

以「新增一个配置项 `max_conns`（上游最大并发连接数）」为例，演示从需求到提交的完整流程。

### 第 1 步：定位与边界判断（第 1 节三问）

| 问题 | 回答 |
|---|---|
| 符合定位吗？ | ✅ 限流代理的可观测/控制能力，属于「代理 + 限流」范畴 |
| 引入新依赖吗？ | ✅ 不引入，纯 Go 标准库实现 |
| 破坏链路吗？ | 需检查 R1–R8：`max_conns` 作用于 `internal/proxy` 的 Transport 层，不影响路径转发（R2）、请求头透传（R4）、SSE 透传（R6） |

**结论**：可以做。落点：`internal/config`（配置）+ `internal/proxy`（应用）。

### 第 2 步：TDD 先行——先写测试（第 5.1 节）

```go
// internal/config/config_test.go 新增用例
func TestConfig_MaxConns_Default(t *testing.T)          // 默认值 0 = 不限制
func TestConfig_MaxConns_Normalize(t *testing.T)        // 负数归一化为 0
func TestConfig_MaxConns_Validate(t *testing.T)         // 非法值报错
func TestConfig_MaxConns_EnvOverride(t *testing.T)      // STREAMGUARD_MAX_CONNS 覆盖

// internal/proxy/proxy_test.go 新增用例
func TestProxy_MaxConns_LimitsConcurrency(t *testing.T) // httptest 上游验证并发上限生效
```

先运行 `go test ./internal/config/ -run MaxConns`，确认**编译失败/测试失败**（红），再进入第 3 步。

### 第 3 步：实现（第 2、3 节架构与代码规范）

1. `internal/config/config.go`：新增 `MaxConns int` 字段 + JSON tag `max_conns` + 默认值 + `Normalize`/`Validate` + `ApplyEnv` 支持 `STREAMGUARD_MAX_CONNS`
2. `internal/proxy/proxy.go`：`New()` 接收配置，设置 `Transport.MaxConnsPerHost`；导出符号补中文 doc comment
3. **不改动** `cmd/` 任何文件（入口层无需感知）

### 第 4 步：配置项九处同步（第 4 节，逐项核对）

| # | 位置 | 本次改动 |
|---|---|---|
| 1 | `internal/config/config.go` | ✅ 第 3 步已完成 |
| 2 | 环境变量 | ✅ `ApplyEnv` 已支持 |
| 3 | `config.example.json` | 加 `"max_conns": 10` |
| 4 | `docs/configuration.md` | 表格加一行 + 详细说明 |
| 5 | `README.md` | JSON 示例 + 配置简表 |
| 6 | GUI `index.html` | 新增输入框 + 说明文案 |
| 7 | GUI `main.js` | el 映射 + collectConfig + fillConfig |
| 8 | GUI `models.ts` | 绑定字段 `maxConns` |
| 9 | 测试 | ✅ 第 2 步已写 |

### 第 5 步：全链路验证（第 5.2 节，5 步缺一不可）

```bash
# 1. 全量测试（含新用例，强制重跑）
go test ./... -count=1

# 2. 静态检查
go vet ./...
gofmt -l .          # 必须无输出

# 3. 构建
.\scripts\build.ps1 -Version "0.0.1"

# 4. 端到端冒烟（用 config.local.json 启动，验证新配置生效）
.\streamguard.exe -config config.local.json
# 另开终端：
curl http://127.0.0.1:8080/healthz
curl http://127.0.0.1:8080/stats

# 5. 敏感信息扫描（必须无输出；模式用字符类拆分，避免敏感词明文入库）
git grep -n -i -E "h[a]ier\.net|model[a]pi-test|api[k]ey-695f|695f046[7]" -- cmd/ internal/ scripts/ README.md docs/ config.example.json
```

### 第 6 步：文档同步（第 6 节）

- `docs/configuration.md`：新增 `max_conns` 行
- `README.md`：配置示例与简表
- `docs/testing.md`：用例数 +5
- `docs/development-log.md`：追加一条「新增 max_conns 的决策记录」（如为何默认不限制）

### 第 7 步：自查清单（第 10 节）+ 申请提交（第 7 节）

逐项打勾第 10 节清单，全部通过后向用户列出：

```
待提交文件：
  M  internal/config/config.go
  M  internal/config/config_test.go
  M  internal/proxy/proxy.go
  M  internal/proxy/proxy_test.go
  M  config.example.json
  M  docs/configuration.md
  M  docs/testing.md
  M  docs/development-log.md
  M  README.md
  M  cmd/streamguard-gui/frontend/index.html
  M  cmd/streamguard-gui/frontend/main.js
  M  cmd/streamguard-gui/frontend/wailsjs/go/models.ts

commit message：
  feat: 新增 max_conns 配置项（上游最大并发连接数）
```

**经用户确认后**才执行 `git add` / `git commit`。

### 反例（禁止的做法）

- ❌ 只改 `config.go` 就提交，GUI/文档/示例不同步（违反九处同步）
- ❌ 在 `cmd/streamguard/main.go` 里写并发限制逻辑（违反架构分层）
- ❌ 引入 `github.com/pkg/errors` 处理错误（违反零依赖）
- ❌ 只跑 `go test ./internal/config/` 就申请提交（违反全链路 5 步）
- ❌ 提交信息写 `update code`（违反 commit message 规范）
