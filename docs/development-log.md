# 开发归档

记录 StreamGuard 的开发过程、决策与经验。

## 2026-09-21 GUI 日志面板显示逐请求基础信息（方案 A：开关控制，无新配置项）

### 背景

GUI 日志面板此前只显示生命周期事件（启动/停止/配置保存），看不到 API 请求本身。
根因是 GUI 从未接 `internal/server` 的逐请求信息。用户希望面板能看到每个请求的基础信息。

### 决策：方案 A（界面开关）而非方案 B（配置项）

- **A（采纳）**：后端始终把请求基础信息发给 GUI，前端加「显示请求日志」勾选框控制显隐。改动只在 `internal/server` + `internal/app` + GUI，**不新增配置项（免去九处同步），即点即生效**。
- B（否决）：新增 `panel_request_log` 布尔配置项，需重启/保存生效，要走完整九处同步。
- 字段：方法 + 路径 + 状态码 + 耗时，**外加排队/限流标记**（用户明确要求）。

### 改动

1. **server**：新增 `internal/server/requestlog.go`——`RequestEvent` + `statusRecorder`（实现 `Flush`/`Unwrap`/`Hijack` 透传，包裹 ResponseWriter 后**不破坏 SSE 逐块实时透传 R6 与协议升级**）。`Options.OnRequest` 钩子；`handleProxy` 用 statusRecorder 捕获状态码，defer 上报，覆盖限流早退（429）路径；用墙钟测量 + 1ms 阈值判定“排队”。
2. **app**：`RequestLogFunc` 类型 + `SetRequestLogHook` + `newRequestForwarder` 接线到 `server.Options.OnRequest`。GUI 用基本类型签名，无需 import server 包（守分层）。钩子为 nil 时不注册（CLI 默认零开销）。
3. **GUI app.go**：`LogEntry` 加 `source` 字段（system/request）；注册钩子把请求按状态码映射 info/warn/error（≥500→error、429/≥400→warn、其余→info），格式化 `POST /v1/... → 200 · 210ms · 排队`。
4. **前端**：「显示请求日志」勾选框（默认勾选），`filteredLogs` 过滤 source=request；models.ts 同步 LogEntry.source。

### 红线遵守

- **第 9 节**：面板只展示基础元信息，**不含请求体/响应体/header**，详细日志仍走 `verboseLogger` 落盘/控制台，不进面板。
- **R6**：statusRecorder 透传 Flush/Unwrap，SSE 测试证明 4 块间隔 ~120ms 未被缓冲。

### 验证

- server 6 测（含 SSE 透传、429 捕获、无钩子零开销）+ app 2 测全绿；`go vet`/`gofmt`/前端 tsc+eslint+vite build 全过。

## 2026-09-21 文档合并：ui-redesign.md 并入 ui-design.md

UI 重设计已落地，`ui-design.md`（现状）与 `ui-redesign.md`（愿景/分期）出现大量重叠、且前者反复引用后者。
决策：以**最新已实现样式为准**合并为单一 `ui-design.md`，保留设计理念、三条原则、对标萃取、交互依据、主题系统、
验收进度（已完成/待办）等长期有价值内容，删除过时的分期规划表，并**删除 `ui-redesign.md`**。
所有文档入口（README、docs/README、development-guide）本就只引用 `ui-design.md`，无需改动；仅本历史条目改为注明“已并入”。

## 2026-09-21 UI 重设计 P0/P1 落地 + 主题系统实施（修复半迁移断链）

### 背景

原 `ui-redesign.md` 提案（现已并入 [ui-design.md](./ui-design.md)，不再单列）已部分实施，但处于**半迁移状态**：index.html 已改为双视图结构（主视图 + 配置视图），
但 main.ts 仍引用旧 id `btnWinchClose`（新 HTML 为 `btnClose`）→ `$()` 抛异常 → `init()` 中断 → **整个 GUI 瘫痪**；
且无任何视图切换逻辑，配置页永久不可达；sparkline/熔断卡片/日志筛选器等一批占位未接线。

### 改动（纯前端，Go 侧零改动）

1. **修复阻断链**：`el.close` → `btnClose`；补齐 `.view`/`.main-view` 双视图布局 CSS（旧 `.layout` 网格样式已删）
2. **视图切换**：⚙ 进入配置视图、「返回主视图」离开；dirty 时 `confirm` 确认，绝不静默丢弃；首次运行（upstream 空）自动进配置视图并聚焦必填项（抽屉落地为全屏视图切换，窄窗更友好）
3. **日志区接线**：级别下拉 + 关键词搜索 + 自动滚动开关全部生效；hover 冻结（悬停暂停重绘、移开补渲染）；渲染上限 500 行；去重改用「长度 + 末条内容」比较（旧版只比长度，同数变更会漏刷新）；warn/error 行附 ⚠/✕ 徽标（不全靠颜色）
4. **统计卡片**：熔断卡片读已返回的 `breakerState`（未启用/正常/半开/⚠ 已熔断）；sparkline 用轮询 `total` 增量采样 60 点纯 SVG 绘制（零后端改动）；空状态引导（运行中且 0 请求时提示下一步）
5. **状态 pill 升级**：`● 运行中 · 2h13m`，前端记住启动时刻推算时长
6. **错误直达修复**：启动失败 toast 附「去修改端口」按钮（匹配 bind/占用/permission），点击直跳配置视图并聚焦监听地址
7. **字段级校验**：上游 URL 格式、监听地址端口范围、大小并发不同时为 0，失焦即红字、输入即消除
8. **快捷键**：`Ctrl+S` 保存（不在配置页时自动切过去）、`Ctrl+R` 启停，均拦截浏览器默认行为
9. **toast 扩展**：支持动作按钮；带动作按钮时停留 6s

### 新增文件

- `scripts/verify.ps1`：本地全链路验证助手（typecheck/lint/vite build + go test/vet/gofmt），绕过终端对 node_modules 路径的误拦截

### 验证

`verify.ps1` 全绿；`build.ps1` 完整构建通过（go test ./... -count=1 + vet + CLI + wails GUI）；敏感扫描无输出。待人工冒烟：9.2 清单 P0/P1 追加项 + 主题追加项。

### 经验教训

- **HTML 结构重构必须与 JS 同步提交**：半迁移状态比旧版更糟（旧版能用，新版直接白屏）；`$()` 找不到元素即抛异常的fail-fast 设计是对的，但暴露了缺一道前端构建/冒烟门禁
- **require-atomic-updates 消除法**：await 后写共享标记改用 promise 回调链，状态写在同步/回调路径，比 disable 注释干净
- **终端内容过滤会误拦含 node_modules 路径/Location 命令的命令行**，封装进 .ps1 脚本文件执行可绕过

## 2026-09-21 GUI 配置界面易用性重构 + CI 修复

### 背景

用户反馈配置界面不易理解：21 个字段平铺、互斥参数（主开关关闭时子参数仍可编辑）无视觉区分、`log_file=auto` 魔法字符串难懂。同时 CI 的 Cross-compile CLI 因敏感扫描规则过宽误伤示例域名而失败。

### GUI 改动（仅前端，后端零改动）

1. **分组卡片**：配置重组为 6 个语义分组——① 基础连接（必填）② 限流 ③ 大请求并发限制 ④ 熔断保护 ⑤ 上游限流重试 ⑥ 高级选项：日志（`<details>` 默认折叠）
2. **主开关联动**：③④⑤ 改为 switch 开关；关闭时子参数区半透明 + `pointer-events:none`，开关文字实时显示「已启用/未启用」
3. **速率 vs 并发对比文案**：②「收费站发卡速率（每秒放行几个）」vs ③「停车场车位数（同时处理几个）」，并在③中写明「可同时启用，请求依次通过两道闸门」——消除用户「二选一」误解
4. **日志落盘下拉化**：`log_file` 文本框改为三选一下拉（不落盘 / 自动：logs/streamguard-日期.log / 自定义路径），自定义时才显示输入框；`auto` 魔法字符串不再暴露给用户

### CI 修复（两项）

1. **frontend/dist 占位**：`go:embed all:frontend/dist` 在 CI 检出后无该目录（被 gitignore），vet 编译失败 → 测试步骤前生成占位 index.html
2. **敏感扫描规则过宽**：宽泛关键词误伤示例域名与规范文档中的规则文本 → 收紧为精确匹配真实值（真实域名后缀、真实主机名、真实 Key 前缀与哈希），development-guide.md 两处同步

### 经验教训

- **互斥/依赖参数必须提前视觉区分**：主开关关闭时子参数应禁用，否则用户会误以为参数生效
- **魔法字符串不要暴露给用户**：`auto` 这类约定值应在 UI 层转译为行为描述
- **CI 扫描规则要精确匹配**：宽泛关键词会误伤示例与文档自身，导致 CI 永远红
- **GUI exe 必须用 `wails build`**：`go build` 不嵌入前端资源，窗口打不开（2026-09-20 踩坑）

## 2026-09-20 新增「按请求大小分档的并发限流」

### 需求

> 输入 token 小于 10000，限 2 个并发；token 数大于 10000 限 1 个并发。

### 方案选型

评估了三种配置形态：

| 方案 | JSON 形态 | GUI 实现 | 用户理解 | 扩展性 |
|---|---|---|---|---|
| A. 数组规则 | `rules: [{...},{...}]` | ❌ 需动态列表组件 | 中（要理解「兜底」） | ✅ 好 |
| B. 固定两档 | 4 个平铺字段 | ✅ 4 个 input | ✅ 好 | ❌ 差 |
| **C. 单阈值 + 两个并发数** | 3 个平铺字段 | ✅ 3 个 input | ✅✅ 最好 | ⚠️ 中 |

**最终选 C**，理由：

1. **用户原话直译**：需求就是「一个阈值 + 两个数字」，零认知负担
2. **GUI 只需 3 个 input**：现有 GUI 是纯平铺表单，无动态列表组件，方案 A 需引入全新组件
3. **语义自解释**：小于阈值 → 小请求 → `small_concurrent`；大于 → 大请求 → `large_concurrent`
4. **覆盖 95% 场景**：真实需求几乎都是两档模型

**放弃方案 A 的原因**：为 5% 的扩展性牺牲 95% 场景的易用性，不划算。未来若需三档，可用 `size_limit_rules` 数组平滑升级（存在 rules 时忽略平铺字段）。

### 关键设计决策

#### 1. 与速率限流（`rate`/`burst`）的关系：串联，不冲突

两者管的是**不同维度**：

| | `rate`/`burst` | `size_limit_*` |
|---|---|---|
| 管什么 | 请求**速率**（每秒几个） | **同时在途数**（同时几个） |
| 阻塞点 | 令牌桶没令牌 | 该档位槽位已满 |

**串联顺序**：`Waiter.Wait()` → `Acquire()` → `proxy.ServeHTTP()` → `release()`

- 排队等速率的请求**不占用并发槽位**（槽位在速率通过后才获取）
- 两者语义正交，各自独立生效

#### 2. 共享 deadline，避免超时叠加

**问题**：若两者各自等待 `max_wait`，最坏总等待 = 30s + 30s = 60s。

**方案**：在 `handleProxy` 开头创建一次 `context.WithTimeout(ctx, maxWait)`，两者共享：

```go
ctx := r.Context()
if s.maxWait > 0 && (s.waiter != nil || s.concurrency != nil) {
    ctx, cancel = context.WithTimeout(ctx, s.maxWait)
    defer cancel()
}
s.waiter.Wait(ctx)              // 共享 deadline
s.concurrency.Acquire(ctx, tokens)  // 共享 deadline
```

**总等待仍受 `max_wait` 约束，不会叠加。**

#### 3. token 估算：零依赖启发式

精确 token 数需引入 tokenizer（如 tiktoken），违背「运行时零第三方依赖」约束。采用保守启发式：

- 优先解析 OpenAI 兼容的 `messages` 数组，累加各条 `content` 字节数（支持多模态数组，只统计 `text`）
- 解析失败或结构不符 → 退化为整体请求体字节估算
- 公式：`token ≈ 字节数 / 3`（向上取整）

**为什么除以 3**：英文约 4 字节/token，中文约 3 字节/token（UTF-8）。取 3 对英文偏保守（高估），对中文接近准确。**高估是安全方向**——宁可把请求判为大请求（更严格限流），也不要低估导致大请求挤占上游。

#### 4. 并发限流器实现要点

- **信号量语义**：`Acquire(ctx, tokens)` 返回 `release` 闭包，`defer release()`
- **FIFO 队列**：`dispatch()` 只唤醒队首中「该档位仍有空闲槽位」的等待者，保证公平且不超发
- **release 幂等**：用 `sync.Once` 包裹，重复调用不会导致计数错误
- **超时/取消竞态**：`removeWaiter` 返回 false 表示已被 release 分配槽位，此时视为成功而非超时
- **上限为 0 = 不限制**：直接放行，`release` 为空操作

#### 5. 请求体读取与回填

`peekBody(r)` 读取请求体用于 token 估算，并回填 `r.Body`，保证后续代理仍能读到完整内容。读取失败时回填空 body，避免代理读到半截数据。

### 新增配置项（九处同步）

| 字段 | 默认值 | 说明 |
|---|---|---|
| `size_limit_enabled` | `false` | 总开关 |
| `size_limit_threshold` | `10000` | 大小请求分界（估算 token） |
| `size_limit_small_concurrent` | `0` | 小请求并发上限，`0` 不限制 |
| `size_limit_large_concurrent` | `0` | 大请求并发上限，`0` 不限制 |

**校验规则**：启用时至少要有一个档位设置了并发上限，否则配置无意义（返回错误）。

### 经验教训

1. **产品设计优先于技术实现**：数组方案技术上更优雅，但 GUI 无动态列表组件、用户需理解「兜底规则」，最终选了更简单的平铺方案
2. **配置形态要匹配现有 GUI 能力**：现有 GUI 是纯平铺表单，新配置应顺应而非对抗
3. **串联限流器必须共享 deadline**：否则超时叠加，用户难以预期
4. **估算类功能要可观测**：`/stats` 暴露 `size_limit_*` 字段，用户可据此校准阈值
5. **默认关闭是红线**：`size_limit_enabled: false` 时行为与之前完全一致

## 2026-09-20 代码审查问题修复

外部 AI 审查提出 9 项问题，逐条核实后修复 6 项（3 项为误报或已修复）。

### 核实结果

| # | 问题 | 核实 | 处理 |
|---|---|---|---|
| 1 | `timeout` 死配置 | ✅ 属实（实测 500ms 配置，上游 sleep 3s 仍返回 200） | 已修复 |
| 2 | 限流器递归重试导致统计重复计数 | ✅ 属实（实测 20 请求统计出 39） | 已修复 |
| 3 | X-Forwarded-For 重复注入 | ✅ 属实（实测 `"1.2.3.4:5678, 1.2.3.4"`） | 已修复 |
| 4 | Validate 负数检查不可达 | ✅ 属实（Normalize 先执行） | 已修复 |
| 5 | GUI 版本注入静默失效 | ✅ 属实（GUI main.go 无 version 变量） | 已修复 |
| 6 | Stop 后状态残留 | ✅ 属实 | 已修复 |
| 7 | README 测试数矛盾 | ⚠️ 已在本轮工程化中修复 | 无需处理 |
| 8 | 取消的等待者不释放 FIFO 槽位 | ⚠️ 设计取舍（不阻塞他人，仅浪费槽位） | 暂不处理 |
| 9 | SIGHUP / 中英混杂 / stats 无序 | ⚠️ SIGHUP 已实现；其余为风格问题 | 部分修复 |

### 修复详情

#### 1. timeout 死配置（最严重）

**问题**：`opts.Timeout` 仅做默认值规整，从未应用到请求。上游挂起时请求无限等待，违背 README 承诺。

**方案**：在 `ServeHTTP` 中按请求设置超时，**SSE 请求豁免**：

```go
if p.timeout > 0 && !isSSERequest(r) {
    ctx, cancel := context.WithTimeout(r.Context(), p.timeout)
    defer cancel()
    r = r.WithContext(ctx)
}
```

**SSE 识别**（`isSSERequest`）：
- `Accept: text/event-stream` 头
- 或请求体 JSON 中 `stream=true`（OpenAI 兼容接口）

**为何不用 `Transport.ResponseHeaderTimeout`**：该配置是全局的，无法按请求区分 SSE 与普通请求，会中断长 SSE 流。

#### 2. 限流器递归重试 → 循环

**问题**：令牌被抢占时递归调用 `Wait(ctx)`，`total++`/`waited++` 重复执行。

**方案**：改为 `for` 循环，整个等待过程受 `maxWait` 累计约束（`deadline`）。

#### 3. X-Forwarded-For 重复注入

**问题**：Director 手动设置 XFF，标准库 `ReverseProxy.ServeHTTP` 又追加一次。

**方案**：移除手动设置，交给标准库（它会自动追加客户端 IP，并保留客户端已有的 XFF）。

#### 4. Validate 负数检查不可达

**问题**：`Load`/`ApplyEnv` 后先 `Normalize()` 把负数规整为默认值，`Validate()` 的负数检查永不触发。

**方案**：明确「Normalize → Validate」调用顺序约定，`Validate` 只校验 Normalize 无法修复的项（地址、日志级别枚举），并补充 `log_level` 枚举校验。

#### 5. GUI 版本注入

**问题**：构建脚本对 GUI 执行 `-X main.version=`，但 GUI 入口未定义该变量，注入静默失效。

**方案**：GUI `main.go` 补充 `version`/`commit` 变量 + `-version` 标志。

#### 6. Stop 后状态残留

**方案**：`Stop()` 中清空 `waiter`/`proxy`/`breaker`/`retry` 引用。

#### 9. /stats 字段顺序

**方案**：`map[string]any` 改为 `statsResponse` 结构体，保证 JSON 字段顺序稳定。

### 经验总结

1. **「配置声明的能力 ≠ 实际生效的能力」是最大隐患**：`timeout` 死配置存在多轮未被发现，说明需要**针对每个配置项写行为测试**，而非只测解析
2. **外部审查需逐条实测验证**：9 项中 3 项为误报/已修复，不能盲信
3. **标准库已做的事不要重复做**：XFF 由 `ReverseProxy` 自动处理，手动设置反而出错
4. **递归改循环**：涉及统计计数时，递归会重复累加，循环更可控
5. **SSE 豁免超时**：全局 `ResponseHeaderTimeout` 会误伤长流，必须按请求粒度控制

---

## 2026-09-20 上游限流重试（retry）

### 需求

> 「如果上游 429 限流，是否可以设置等待 x 秒，间隔 x 秒重试几次呢？」

上游（网关）自身限流返回 429 时，客户端只能自己重试。希望在代理层自动等待重试。

### 决策：实现，但默认关闭

**理由**：
- 上游限流是真实痛点，代理层重试让客户端无感知（SSE 场景尤其有价值）
- 但**风险高于熔断器**：重试可能重复执行非幂等请求、加剧上游拥塞
- 因此与熔断器一致，**默认关闭**，不改变既有行为

### 设计

新增 `internal/retry` 包，实现重试策略：

| 配置 | 默认值 | 说明 |
|---|---|---|
| `retry_enabled` | `false` | 总开关 |
| `retry_max_attempts` | `3` | 最大尝试次数（含首次） |
| `retry_initial_wait` | `1s` | 指数退避起点 |
| `retry_max_wait` | `10s` | 单次等待上限 |

**重试流程**：

```
上游返回 429/503
  → 优先读取 Retry-After（秒数或 HTTP 日期）
  → 否则指数退避：1s → 2s → 4s…（受 max_wait 约束）
  → 等待后重试，最多 max_attempts 次
  → 仍失败则透传最后一次响应
```

### 关键实现点

#### 1. 只在响应头阶段重试

**问题**：SSE 流一旦开始传输，重试会破坏流式输出。

**方案**：用 `bufferedResponseWriter` 缓冲上游响应，在**响应头到达、body 未写入客户端**时决定是否重试。重试决策完成后才 `flushTo` 真实 ResponseWriter。

**注意**：`bufferedResponseWriter` 必须实现 `http.Flusher`，否则 `ReverseProxy` 会因类型断言失败走降级路径。

#### 2. 请求体自动重放

**问题**：`r.Body` 读一次就消耗完了，重试时 body 为空。

**方案**：
- 优先使用 `r.GetBody`（标准库 server 自动设置）
- 缺失时（如测试场景）手动 `io.ReadAll` 缓冲到内存，并重建 `GetBody`

**踩坑**：`httptest.NewRequest` **不会**设置 `GetBody`，导致初版实现的重试守卫直接跳过重试。改为「缺失时手动缓冲」后修复。

#### 3. 尊重 Retry-After

上游明确告知等待时长时，比自己的退避算法更准。支持两种格式：
- 秒数：`Retry-After: 7`
- HTTP 日期：`Retry-After: Wed, 21 Oct 2026 07:28:00 GMT`

两者都受 `retry_max_wait` 约束，避免上游给出过长等待。

#### 4. 与熔断器联动

重试耗尽仍失败时，`ModifyResponse` 已计入熔断失败计数，无需额外处理。

### 测试

新增 11 个 retry 包单元测试 + 7 个 proxy 集成测试：

| 测试 | 验证点 |
|---|---|
| `TestPolicy_DisabledNeverRetries` | 未启用不重试 |
| `TestPolicy_DefaultStatuses` | 默认只重试 429/503 |
| `TestPolicy_ExponentialBackoff` | 指数退避 1s→2s→4s→8s |
| `TestPolicy_RespectsRetryAfterSeconds` | 尊重 Retry-After 秒数 |
| `TestPolicy_RespectsRetryAfterHTTPDate` | 尊重 HTTP 日期格式 |
| `TestProxy_RetryOn429ThenSuccess` | 429 后重试成功 |
| `TestProxy_RetryExhaustedPassesThrough` | 重试耗尽透传 |
| `TestProxy_RetryPreservesRequestBody` | 请求体完整重放 |
| `TestProxy_RetryRespectsRetryAfterHeader` | 等待受 MaxWait 约束 |

### 经验总结

1. **重试比熔断风险更高**：涉及请求重放与幂等性，必须默认关闭并充分文档化
2. **响应头阶段是重试的唯一安全窗口**：body 一旦开始传输就不能回头
3. **`httptest.NewRequest` 不设 `GetBody`**：测试与真实 server 行为有差异，实现要兼容两者
4. **缓冲响应器要实现 Flusher**：否则 `ReverseProxy` 行为异常
5. **优先尊重上游信号**：`Retry-After` 比本地退避算法更准确

---

## 2026-09-20 工程化增强（开源就绪）

### 背景

以「顶级开源仓库」标准审视项目，识别出 12 项可优化点（高/中/低优先级），本轮全部落地。

### 落地清单

| # | 优先级 | 项 | 说明 |
|---|---|---|---|
| 1 | 高 | MIT LICENSE | 明确开源许可 |
| 2 | 高 | GitHub Actions CI | 三平台矩阵测试 + 交叉编译 + 敏感扫描 |
| 3 | 高 | `cmd` 层测试 | 入口层此前零测试，补 7 个 |
| 4 | 高 | 测试计数修正 | 文档与实际长期漂移，统一为真实值 |
| 5 | 中 | stats 结构化指标 | 新增等待耗时/超时/取消等可观测指标 |
| 6 | 中 | 上游熔断 | 上游故障时快速失败，避免请求堆积 |
| 7 | 中 | CLI SIGHUP 热重载 | Unix 下 `kill -HUP` 重载配置 |
| 8 | 中 | 日志保留清理 | 按 `log_retain_days` 自动清理过期日志 |
| 9 | 低 | 版本注入 git hash | 构建时注入 commit，便于定位版本 |
| 10 | 低 | README 徽章 | CI / Go / License 徽章 |
| 11 | 低 | docs 索引页 | `docs/README.md` 导航 |
| 12 | 低 | 前端 eslint | 原生 JS 语法级检查，零框架依赖 |

### 关键设计决策

#### 熔断器：默认关闭，不改变既有行为

**决策**：新增 `internal/breaker` 包，但 `breaker_enabled` 默认 `false`。

**理由**：
- 熔断是「可选增强」，不应改变现有用户的默认行为
- 关闭时 `Allow()` 恒返回 `true`，零开销、零行为差异
- 三态机（closed → open → half-open）标准实现，冷却后放行单个探测请求

**实现要点**：
- `proxy.ErrorHandler`（上游不可达）与 `ModifyResponse`（5xx）计为失败
- 熔断开启时直接返回 503 + `Retry-After: 5`，不发起上游请求
- 半开状态只放行**一个**探测请求，成功则闭合，失败则重新打开

#### 日志清理：只删自己生成的文件

**决策**：`cleanupOldLogs()` 只删除形如 `streamguard-YYYYMMDD.log` 的文件。

**理由**：
- `logs/` 目录可能被用户放入其他文件，不能无差别删除
- 通过文件名解析日期，而非文件 mtime（mtime 可能被复制/同步改变）
- `log_retain_days=0` 时完全跳过清理

#### SIGHUP 热重载：平台差异用构建标签隔离

**决策**：`reload_unix.go`（`//go:build !windows`）监听 SIGHUP，`reload_windows.go` 为空实现。

**理由**：
- Windows 没有 SIGHUP 语义，强行模拟会引入复杂度
- 用构建标签隔离，保证三平台均可编译
- Windows 用户通过 GUI「保存并重启」或重启进程完成配置更新

#### 前端选型：保持原生 JS

**决策**：不引入 React/Vue/Svelte，仅补 eslint 做语法检查。

**理由**：
- GUI 功能面窄（配置表单 + 状态轮询），框架收益 < 引入成本
- 与项目「零第三方依赖」哲学一致
- 顶级 Go 工具项目（syncthing、caddy）的 Web UI 主流也是原生 JS
- 若未来 UI 明显复杂化，优先考虑 **Svelte**（编译期零运行时），而非 React

### 踩坑记录

#### 1. `Stats()` 在 `Start()` 前返回零值

**现象**：`TestApp_StatsIncludesBreaker` 失败，`BreakerEnabled` 为 `false`。

**原因**：熔断器在 `Start()` 中创建，未启动时 `a.breaker == nil`，`Stats()` 跳过熔断字段。

**解决**：测试改为先 `Start()` 再断言（符合真实使用场景）。

#### 2. eslint 扁平配置重复键

**现象**：`eslint.config.js` 中 `globals` 出现两次 `window`。

**原因**：手写配置时重复添加。

**解决**：去重，并补充 `alert`/`confirm` 等浏览器全局。

#### 3. `no-unused-vars` 误报 catch 占位参数

**现象**：`catch (_) { /* 忽略 */ }` 报 `'_' is defined but never used`。

**解决**：配置 `caughtErrorsIgnorePattern: "^_"` 等忽略模式。

### 经验总结

1. **可选增强默认关闭**：新能力不应改变既有默认行为，降低升级风险
2. **平台差异用构建标签**：比运行时判断更清晰，且保证编译期正确
3. **清理类操作要保守**：只删自己生成的文件，宁可漏删不可误删
4. **文档计数要自动化校验**：测试数漂移是常见问题，CI 应能发现
5. **前端选型看复杂度**：轻量工具面板用原生 JS 即可，避免过度工程

---

## 2026-09-20 初始开发

### 需求澄清

**初始理解偏差**：最初按「拒绝式限流」（超限返回 429）实现。

**用户澄清**：核心是「限流**等待请求**」，例如 1 秒 1 次，超限时**排队等待**而非拒绝。

**最终需求**：

| 项 | 说明 |
|---|---|
| 代理目标 | 本地全部路径，原样转发到上游 |
| 限流方式 | **等待式**（阻塞排队） |
| 限流粒度 | 例如 1 秒 1 次 |
| 配置来源 | JSON 配置文件 |
| 协议适配 | SSE 流式与普通 JSON 自动适配 |
| 平台 | Windows 客户端，无 Redis |

### 技术选型

| 层面 | 选型 | 理由 |
|---|---|---|
| 语言 | Go 1.25 | 交叉编译单文件 exe，无运行时依赖 |
| 限流算法 | 令牌桶 + FIFO 排队 | 天然支持等待语义，平滑速率 |
| 代理 | `net/http/httputil.ReverseProxy` | 标准库，成熟稳定 |
| SSE 透传 | `FlushInterval = -1` | 关闭缓冲，实时转发 |
| 配置 | JSON + 环境变量 | 零外部依赖（不引入 yaml 库） |
| 测试 | 标准库 `testing` | 无第三方测试框架依赖 |

### 开发过程（TDD）

按「先写测试 → 实现 → 验证」的循环推进：

| 阶段 | 模块 | 测试数 | 结果 |
|---|---|---|---|
| 1 | 配置加载 | 8 | ✅ |
| 2 | 等待式限流器（核心） | 8 | ✅ |
| 3 | 反向代理与 SSE 透传 | 7 | ✅ |
| 4 | 请求头/请求体透传 | 7 | ✅ |
| 5 | SSE 实时性 | 1 | ✅ |
| 6 | HTTP 服务 | 9 | ✅ |
| 7 | 应用核心层 | 9 | ✅ |
| **合计** | | **49** | **全部通过** |

> 完整用例清单与端到端验证方法见 [testing.md](./testing.md)。
> 全链路测试规则（R1–R8）见 [test-rules.md](./test-rules.md)。

### 遇到的问题与解决

#### 1. `time.Duration` 无法解析 `"2s"` 字符串

**现象**：JSON 配置中 `"max_wait": "30s"` 解析失败，报 `cannot unmarshal string into Go struct field`。

**原因**：Go 标准库的 `time.Duration` 在 JSON 中默认按**纳秒整数**解析。

**解决**：自定义 `Duration` 类型，实现 `json.Unmarshaler` / `json.Marshaler`，同时兼容字符串与数字。

```go
func (d *Duration) UnmarshalJSON(data []byte) error {
    // 先尝试字符串 "2s"，失败则回退为数字（纳秒）
}
```

#### 2. 客户端取消时测试断言错误

**现象**：`TestServer_ClientCancelDuringWait` 失败，断言 `rec2.Code != 200` 不成立。

**原因**：客户端取消时，代码**不写响应**（连接已断开，写响应无意义），而 `httptest.ResponseRecorder` 默认状态码为 200。

**解决**：修正测试断言，改为验证「请求未被转发到上游」（这才是真正要保证的行为）。

#### 3. 敏感信息隔离

**需求**：真实上游地址、公司信息不能出现在提交到 git 的代码中。

**解决**：
- `.gitignore` 排除 `config.local.json`、`*.local.json`、`docs/local/`、`*.local.md`
- `config.example.json` 使用通用占位符（`api.example.com`、`your-api-key-here`）
- 真实信息集中在 `config.local.json` 与 `docs/local/upstream-models.local.md`
- 用 `git check-ignore -v` 验证忽略规则生效

#### 4. Host 头未改写导致上游 404

**现象**：通过代理请求真实上游返回 `404 Not Found`（nginx），但直连上游同一路径返回 `401`（路径正确，仅缺凭据）。

**排查过程**：
1. 直连上游 `/model/v1/chat/completions` → **401**；`/v1/chat/completions` → **404**
   → 确认正确路径是 `/model/v1/chat/completions`
2. 通过代理请求同一路径 → **404**，与直连不一致 → 代理改写了某些东西
3. 直连上游并手动设置 `Host: 127.0.0.1:8080` → **404**；`Host: <上游域名>` → **401**
   → **根因确认**：上游 nginx 按 `Host` 头做虚拟主机路由

**根因**：`httputil.NewSingleHostReverseProxy` 的默认 Director **只设置 `r.URL.Host`，不修改 `r.Host`**。而 Go 的 `http.Transport` 发送请求时以 `r.Host` 为准，因此 `Host` 头仍是客户端的 `127.0.0.1:8080`，上游按此路由找不到虚拟主机。

**解决**：在自定义 Director 中显式设置 `r.Host`：

```go
if opts.PreserveHost && clientHost != "" {
    r.Host = clientHost   // 保留客户端原始 Host
} else {
    r.Host = target.Host  // 改写为上游主机名（默认）
}
```

**附带修复**：原实现用闭包外变量 `clientHost` 缓存首个请求的 Host，存在**并发数据竞争**且后续请求会复用错误值。改为从当前请求 `r.Host` 读取，并新增 `TestProxy_PreserveHostConcurrent` 回归测试。

**经验**：`ReverseProxy` 的 `Host` 行为容易误解——`r.URL.Host` 与 `r.Host` 是两个独立字段，前者决定连接目标，后者决定 `Host` 请求头。

---

## 阶段四：桌面版（Wails）与双模式架构

### 需求

> 「go 的桌面 Wails 桌面版（代理主要是桌面用，可以配置），也需要支持命令哈」

即：既要**桌面图形界面**（可视化配置、实时状态），也要保留**命令行模式**（脚本化、服务化）。

### 架构决策：抽取 `internal/app` 核心层

**问题**：如果 CLI 与 GUI 各自实现一遍服务启动逻辑，会产生大量重复代码，且行为容易不一致。

**方案**：抽取 `internal/app` 作为核心层，封装服务生命周期：

```
CLI 入口 ──┐
           ├──> internal/app ──> config / limiter / proxy / server
GUI 入口 ──┘
```

`app.App` 提供统一接口：

| 方法 | 说明 |
|---|---|
| `New(cfg)` | 创建实例（不自动启动） |
| `Start()` | 启动服务，支持 `:0` 随机端口 |
| `Stop(ctx)` | 优雅停止，未运行时调用不报错 |
| `Restart(ctx)` | 重启 |
| `UpdateConfig(ctx, cfg)` | 更新配置并重启（「保存并重启」语义） |
| `Running()` / `Addr()` | 运行状态与**实际**监听地址 |
| `Stats()` | 统计快照 |
| `Config()` | 配置**副本**（防止外部修改内部状态） |

**关键设计点**：

1. **先 `net.Listen` 再 `Serve`**：这样才能拿到实际端口（支持 `:0` 随机端口，便于测试与多实例）。
2. **`Config()` 返回副本**：避免调用方拿到指针后直接改内部状态，破坏封装。
3. **`Stop()` 幂等**：未运行时调用返回 `nil`，调用方可无脑清理。

### 踩坑：goroutine 捕获共享字段导致空指针 panic

**现象**：`TestApp_Stats` 偶发 panic：

```
panic: runtime error: invalid memory address or nil pointer dereference
net/http.(*Server).Serve(0x0, ...)
    internal/app/app.go:121
```

**原因**：`Start()` 里启动的 goroutine 直接访问了 `a.httpServer`：

```go
go func() {
    a.httpServer.Serve(ln)   // ← 危险：Stop() 会把 a.httpServer 置为 nil
}()
```

当 `Stop()` 把 `a.httpServer = nil` 后，goroutine 若尚未进入 `Serve`，就会解引用 nil。

**解决**：goroutine 捕获**局部变量**，不访问共享字段：

```go
httpServer := &http.Server{...}
a.httpServer = httpServer
go func() {
    httpServer.Serve(ln)     // ← 安全：捕获局部变量
}()
```

**教训**：goroutine 中访问可变共享字段是竞态高发区。**优先捕获局部变量**，让 goroutine 与字段的生命周期解耦。

### 踩坑：Go `internal` 包规则阻止跨模块引用

**现象**：

```
app.go:16:2: use of internal package
github.com/streamguard/streamguard/internal/app not allowed
```

**原因**：Wails 初始化时生成了独立的 `go.mod`（模块名 `streamguard-gui`），而 Go 规定 `internal` 包**只能被同一模块内**的代码引用。独立模块无法访问主模块的 `internal`。

**解决**：删除 GUI 的独立 `go.mod`，把 `cmd/streamguard-gui` 并入主模块，Wails 依赖加入主模块的 `go.mod`。

**教训**：`internal` 是**模块级**可见性边界，不是目录级。跨模块共享代码要么放 `pkg/`，要么合并模块。

### 桌面版实现

**后端绑定层**（`cmd/streamguard-gui/app.go`）：桥接前端与 `internal/app`。

暴露给前端的方法：

| 方法 | 说明 |
|---|---|
| `GetConfig()` / `SaveConfig(cfg)` | 读取 / 保存配置（保存后自动重启服务） |
| `Start()` / `Stop()` / `Restart()` | 服务控制 |
| `GetStatus()` | 运行状态 + 统计（前端每秒轮询） |
| `GetLogs()` / `ClearLogs()` | 日志（内存环形缓冲，保留最近 500 条） |
| `GetConfigPath()` | 配置文件路径 |

**配置文件位置**：`%AppData%\StreamGuard\config.json`（用户配置目录，不污染程序目录）。

**前端**：原生 HTML/CSS/JS，零构建依赖（Wails vanilla 模板）。深色主题，左右分栏：

```
┌──────────────────────────────────────────────────┐
│ 🛡️ StreamGuard          ● 运行中                  │
├───────────────────────┬──────────────────────────┤
│ 配置                   │ 运行状态                  │
│  上游地址 [________]   │  ┌────┐┌────┐┌────┐      │
│  速率 [__] 突发 [__]   │  │请求││等待││速率│      │
│  等待 [__] 超时 [__]   │  └────┘└────┘└────┘      │
│  监听 [__] 级别 [__]   │ 日志                      │
│  [保存配置] [重新加载] │  11:03:00 服务已启动...   │
├───────────────────────┴──────────────────────────┤
│ 配置文件：C:\Users\...\config.json  [启动][停止]  │
└──────────────────────────────────────────────────┘
```

**前端轮询**：每秒调用 `GetStatus()` 与 `GetLogs()`，日志仅在条数变化时重绘（避免无谓 DOM 操作）。

### 端到端验证

启动 CLI 代理（`rate=1, burst=1`），连续发送 3 次请求：

| 请求 | 耗时 | 说明 |
|---|---|---|
| 第 1 次 | 0 ms | 消耗突发令牌，立即转发 |
| 第 2 次 | 98 ms | 排队等待约 100 ms |
| 第 3 次 | 1006 ms | 排队等待约 1 s |

`/stats` 返回 `{"total":3,"waited":2}` —— 3 次请求中 2 次发生等待，**等待式限流完全生效**。

### 构建产物

| 产物 | 大小 | 说明 |
|---|---|---|
| `streamguard.exe` | 6.34 MB | CLI 版 |
| `streamguard-gui.exe` | 11.44 MB | 桌面版（Wails 构建） |

### 经验总结

1. **核心层抽取是双模式的关键**：`internal/app` 让 CLI 与 GUI 共享逻辑，避免行为漂移。
2. **goroutine 优先捕获局部变量**：避免与可变共享字段产生竞态。
3. **`internal` 是模块级边界**：跨模块共享需合并模块或改用 `pkg/`。
4. **先 `Listen` 后 `Serve`**：才能支持 `:0` 随机端口，提升可测试性。
5. **返回副本而非指针**：`Config()` 返回深拷贝，保护内部状态。
6. **前端零构建依赖**：原生 HTML/CSS/JS 足够，减少工具链复杂度。

---

## 阶段五：构建产物统一

### 需求

> 「编译后的结果统一个目录，方便后续新增功能改造」

**问题**：产物分散在多处，不便管理：

| 产物 | 原位置 |
|---|---|
| CLI | 仓库根目录 `streamguard.exe` |
| GUI | `cmd/streamguard-gui/build/bin/streamguard-gui.exe` |

新增功能后需要到不同目录找产物。

### 方案：统一输出到仓库根目录

```
streamguard/
├── streamguard.exe        # CLI 版
├── streamguard-gui.exe    # 桌面版
└── config.example.json    # 示例配置
```

**构建脚本增强**（`scripts/build.ps1`）：

| 参数 | 说明 |
|---|---|
| `-Version "1.0.0"` | 注入版本号（`-ldflags -X main.version`） |
| `-Target all\|cli\|gui` | 选择性构建，避免改一处功能就全量重编 |
| `-SkipTest` | 跳过测试与 vet，快速迭代 |

**关键实现**：Wails 默认输出到 `build/bin`，脚本构建后复制到仓库根目录：

```powershell
wails build -platform windows/amd64 -skipbindings -ldflags "-X main.version=$Version"
Copy-Item "$guiDir\build\bin\streamguard-gui.exe" "$RepoRoot\streamguard-gui.exe" -Force
```

### 踩坑：PowerShell 5.1 读取 UTF-8 无 BOM 脚本导致语法错误

**现象**：脚本执行报错，指向一个看似正常的 `if` 语句：

```
所在位置 build.ps1:35 字符: 21
+ if (-not $SkipTest) {
+                     ~
语句块或类型定义中缺少右"}"。
```

**原因**：脚本文件是 **UTF-8 无 BOM**，而 **Windows PowerShell 5.1 默认按系统 ANSI（GBK）编码读取**。中文注释被解码成乱码，其中全角句号 `。` 的字节序列恰好破坏了引号配对，导致解析器认为字符串未闭合。

**解决**：**构建脚本内一律使用英文**（注释与输出），彻底规避编码问题。

**教训**：
- PowerShell 5.1 对 UTF-8 无 BOM 支持不佳，脚本含非 ASCII 字符时风险高
- 跨平台/跨工具链的脚本，**优先用 ASCII**，或显式写入 UTF-8 BOM
- 报错位置可能具有误导性——语法错误常源于**更早的编码问题**

### 构建产物对比

| 产物 | 大小 | 说明 |
|---|---|---|
| `streamguard.exe` | 6.34 MB | CLI 版（`-s -w` 剥离符号后） |
| `streamguard-gui.exe` | 11.44 MB | 桌面版（含 WebView2 绑定） |

> CLI 从 9.01 MB 降至 6.34 MB，得益于 `-ldflags "-s -w"` 剥离调试符号与符号表。

### 经验总结

1. **产物集中管理**：仓库根目录单一出口，`.gitignore` 一行排除，新增功能无需调整路径。
2. **构建脚本参数化**：`-Target` 支持增量构建，改 GUI 不必重编 CLI。
3. **PowerShell 脚本用 ASCII**：规避 5.1 的编码陷阱。
4. **`-s -w` 显著瘦身**：CLI 体积减少约 30%。

### 关键设计决策

#### 等待式 vs 拒绝式限流

**决策**：只保留等待式限流。

**理由**：
- 用户明确要求「等待请求」
- 等待式对客户端更友好，无需处理 429 重试逻辑
- 但无限等待会耗尽连接，故引入 `max_wait` 保护（默认 30s）

#### 令牌桶 + FIFO 排队

**决策**：用 `nextAvailable` 时间戳实现先来先服务。

**理由**：
- 纯令牌桶在并发下可能「后到先得」，不公平
- 记录 `nextAvailable` 保证等待者按到达顺序获得配额

#### 路径原样转发

**决策**：不做路径改写，客户端请求路径直接拼接到 `upstream` 后转发。
**理由**：
- 用户明确「只要将类 path 对应的 api 转到对应后端 path 就行」
- 避免多路由配置带来的复杂度
- 客户端只需把 base_url 指向本地代理，其余路径保持不变

#### 请求头原样透传

**决策**：客户端请求头（含 `Authorization`、`Content-Type`、自定义头）与请求体原样转发到上游，不做修改或丢弃。
**理由**：
- 用户明确「前端的 header 也需要原样转给上游服务」
- 代理定位为「限流 + 透明转发」，凭据由客户端自行携带
- `httputil.ReverseProxy` 默认即复制所有客户端 header，仅需处理 `Host` 与逐跳头

**实现要点**：
- `Host` 默认改写为上游主机名（`ReverseProxy` 标准行为）；新增 `preserve_host` 配置项，为 `true` 时保留客户端原始 `Host`（部分网关如 Kong 依赖它做路由）
- `X-Forwarded-For` 自动追加客户端 IP
- 逐跳头（`Connection`、`Keep-Alive` 等）按 HTTP 规范移除
- 新增 6 个测试覆盖：全量请求头透传、查询参数透传、响应头透传、请求体逐字节透传、Host 默认改写、Host 保留

### 真实上游端到端验证

| 验证项 | 结果 | 证据 |
|---|---|---|
| SSE 流式透传 | ✅ | 11 个 chunk 时间戳递增（161→164→185→…→335ms），证明未被缓冲 |
| 响应头透传 | ✅ | `Via: kong/2.2.1`、`X-Kong-Proxy-Latency` 等完整返回 |
| 普通 JSON 响应 | ✅ | 非流式请求正常返回 `chat.completion` |
| 限流排队 | ✅ | `/stats` 返回 `waited=1`，日志显示耗时递增 |
| 自定义请求头透传 | ✅ | `X-Custom-Test`、`X-Request-Id` 携带后上游正常响应 200 |

新增 `scripts/test-sse.ps1` 用于自动测量每个 SSE 数据块的到达时间，便于回归验证。

### 修复：GUI 未加载本地 `config.local.json`

**根因**：`cmd/streamguard-gui/app.go` 的 `startup()` 中硬编码了 `os.UserConfigDir()` 路径，
未实现 CLI 中的 `findConfigFile()` 查找逻辑。

**修复**：新增 `resolveConfigPath(explicit string)`，与 CLI 保持一致的查找优先级：

| 优先级 | 位置 |
|---|---|
| 1 | 命令行 `-config <path>` |
| 2 | 当前工作目录 `config.local.json` |
| 3 | exe 所在目录 `config.local.json` |
| 4 | exe 上级目录 `config.local.json`（适配 `dist/` 布局） |
| 5 | `%AppData%\StreamGuard\config.json`（回退） |

同时为 GUI 增加 `-config` 命令行参数，并在启动日志中输出实际加载的配置文件路径。

**验证**：启动 GUI 后，`config.local.json` 的访问时间被刷新（14:16:24），
而 `%AppData%\StreamGuard\config.json` 未被触碰（仍为 13:42:29），证明加载来源正确。

### 修复：SSE 长连接导致「保存并重启」报 graceful shutdown 超时

**现象**：GUI 点击「保存并重启」时日志出现

```
failed to apply config: graceful shutdown failed: context deadline exceeded
```

**根因**：`http.Server.Shutdown()` 会等待所有活跃连接变为**空闲**后才返回，仅受传入 `ctx` 约束。
而 StreamGuard 代理的是 **SSE 长连接**（`WriteTimeout` 刻意设为 0 以免长流被中断），
只要有一个流式请求仍在传输，该连接就永远不会空闲 → `Shutdown` 一直等到 10 秒超时。

更严重的是：旧实现在 `Shutdown` 出错时**提前 return**，导致 `a.running` 未置为 `false`，
服务卡在「半停止」状态，与实际不符。

**修复**（`internal/app/app.go` 的 `Stop`）：

1. `Shutdown` 超时后调用 `httpServer.Close()` **强制关闭**残留连接
2. 无论优雅还是强制，都统一置 `running=false` 并清理句柄，保证状态一致
3. 超时不再作为错误返回给调用方（改为记录日志），避免 GUI 弹出无意义的失败提示

**回归测试**：新增 `TestApp_StopWithActiveLongConnection` —— 构造一个挂起的 SSE 请求，
用 200ms 超时调用 `Stop`，断言返回 `nil`、`Running()==false`、且挂起请求被中断。

### 新增：详细日志模式（打印完整请求/响应内容）

**需求**：命令行启动时能详细打印代理的 HTTP 请求内容与响应内容，便于排查问题。

**实现**：

1. `server.Options` 新增 `Verbose` 与 `VerboseLogger` 两个字段
2. `log_level=debug` 时自动开启 `Verbose`（`internal/app` 中判断）
3. 新增 `internal/server/verbose.go`：
   - `verboseRecorder` 包装 `http.ResponseWriter`，捕获状态码、响应头与响应体
   - **关键**：实现并透传 `http.Flusher`，否则 SSE 流会被缓冲，破坏实时性
   - SSE 场景下 `ReverseProxy` 每收到一个数据块调用一次 `Write`，天然实现逐块打印
4. 请求侧 `logRequest` 读取请求体后**回填** `r.Body`，保证代理仍能读到完整内容

**日志格式**：

| 前缀 | 含义 |
|---|---|
| `[req] >>>` | 请求行 |
| `[req] <Header>: <值>` | 请求头 |
| `[req] body (N bytes)` | 请求体 |
| `[resp] <<< <状态码>` | 响应状态行 |
| `[resp] <Header>: <值>` | 响应头 |
| `[resp] chunk#N` | 响应体数据块（SSE 逐块实时） |
| `[resp] <<< done` | 响应汇总 |

**日志分离设计**：`VerboseLogger` 与主 `Logger` 独立。
GUI 模式下详细日志只输出到控制台（`os.Stdout`），**不进入界面日志面板**，避免刷屏。

**热更新**：GUI 点「保存并重启」会走 `UpdateConfig` → `Stop()` + `Start()` 重建服务，
因此 `log_level` 无需重启程序即可生效（服务中断约几十毫秒）。
直接改配置文件或环境变量则需重启进程。

**测试**：新增 5 个测试（`internal/server/verbose_test.go`）：
`TestServer_VerboseLogsRequestAndResponse`、`TestServer_VerboseDisabledByDefault`、
`TestServer_VerboseSSEStreamsChunks`、`TestVerboseRecorder_FlushPassthrough`、
`TestServer_VerboseLoggerSeparatedFromMainLogger`。

### 增强：详细日志支持落盘（`log_file`）

**问题**：GUI 是窗口程序（`-H windowsgui`），双击启动时**没有控制台**，
`os.Stdout` 写入会被系统静默丢弃 —— 「只在控制台打印」在 GUI 场景下等于什么都看不到。

**方案**：新增 `log_file` 配置项，让详细日志可选落盘：

| `log_file` | 输出位置 | 适用场景 |
|---|---|---|
| `""`（默认） | 控制台 | CLI 前台运行 |
| `"auto"` | `<当前目录>/logs/streamguard-YYYYMMDD.log` | **GUI 场景推荐**，按天分文件 |
| 相对路径 | `<当前目录>/logs/<文件名>` | 固定文件名 |
| 绝对路径 | 指定文件（追加写入） | 固定位置归档 |

**实现**（`internal/app/app.go`）：

1. `App` 新增 `verboseFile *os.File` 字段持有文件句柄
2. 新增 `setupVerboseLogger()`：在 `Start()` 中当 `log_level=debug` 时调用
   - 先关闭上一次的文件句柄（避免重启时泄漏）
   - `"auto"` → 生成 `<当前目录>/logs/streamguard-YYYYMMDD.log`
   - 相对路径 → 基于当前目录解析到 `logs/` 下；绝对路径原样使用
   - 目录创建或文件打开失败 → **回退到控制台**，不影响服务启动
3. 新增 `logDir()`：基于 `os.Getwd()` 返回 `<当前目录>/logs`，取不到时回退相对路径
4. 新增 `closeVerboseFile()`：在 `Stop()` 中关闭句柄
5. `config` 新增 `LogFile` 字段与 `STREAMGUARD_LOG_FILE` 环境变量支持

**关键设计**：

- 日志路径**显式基于命令当前目录**解析，不依赖进程 cwd 的隐式行为
- 启动日志打印实际使用的**绝对路径**，便于确认落盘位置
- 日志文件路径非法时**不阻断服务启动**，仅记录警告并回退到控制台

**测试**：新增 8 个测试（`internal/app/verbose_log_test.go`）：
`TestApp_VerboseLogFile`、`TestApp_VerboseLogFileAuto`、`TestApp_VerboseLogFileRelativePath`、
`TestApp_VerboseLogFileAbsolutePath`、`TestApp_VerboseLogFileInvalidPath`、
`TestApp_VerboseLogFileClosedOnStop`、`TestApp_VerboseLogFileDisabledWhenNotDebug`、
`TestConfig_LogFileEnvOverride`。

### 待办与后续

- [ ] 系统托盘图标（`getlantern/systray`）
- [ ] 动态限流规则热更新
- [ ] Prometheus 指标
- [ ] 压测脚本验证限流效果

### 经验总结

1. **需求澄清优先于编码**：初期对「限流」的理解偏差（拒绝 vs 等待）会导致返工，务必先确认行为语义
2. **TDD 有效捕获边界问题**：并发安全、窗口过期、context 取消等边界均由测试先行发现
3. **敏感信息隔离要前置**：在写第一行配置代码时就设计好 gitignore 规则，避免事后清理
4. **Go 的 JSON 时长坑**：`time.Duration` 需自定义序列化才能支持人类可读格式
