# 全链路测试规则

本文档定义 StreamGuard 的**全链路测试规则**：从客户端发起请求，到代理限流、转发、上游响应、流式回传的完整链路，每一环的验证规则与判定标准。

> 用例清单见 [testing.md](./testing.md)；本文档聚焦**链路规则与判定标准**。

## 1. 全链路拓扑

```
┌──────────┐   ①请求    ┌─────────────────────────────────────┐   ④转发   ┌──────────┐
│  客户端   │ ─────────► │           StreamGuard               │ ────────► │  上游服务 │
│ (SDK/curl)│            │                                     │           │ (LLM API)│
└──────────┘            │  ②限流排队 ──► ③路径拼接 ──► ⑤透传    │           └──────────┘
      ▲                 │                                     │                │
      │                 └─────────────────────────────────────┘                │
      │                                    ⑥流式回传                            │
      └────────────────────────────────────────────────────────────────────────┘
```

| 环节 | 说明 | 验证规则 |
|---|---|---|
| ① 请求进入 | 客户端连接本地代理 | 监听地址可达，`/healthz` 返回 ok |
| ② 限流排队 | 令牌桶 + FIFO 等待 | 超限请求排队而非拒绝；超时返回 429 |
| ③ 路径拼接 | 路径原样拼接到上游 | 路径不被改写、不被截断 |
| ④ 转发上游 | 请求头/体原样透传 | 除 `Host` 与逐跳头外全部透传 |
| ⑤ 响应接收 | 接收上游响应 | 状态码、响应头原样返回 |
| ⑥ 流式回传 | SSE 逐块实时透传 | 数据块不被缓冲，时间戳递增 |

## 2. 链路规则（R1–R8）

### R1：监听与健康检查

**规则**：代理启动后，`/healthz` 必须返回 `{"status":"ok"}`，且该端点**不转发到上游**。

**判定**：

```bash
curl -s http://127.0.0.1:8080/healthz
# 期望：{"status":"ok"}
```

**失败表现**：连接被拒绝（未启动）、返回上游响应（误转发）。

---

### R2：路径原样转发

**规则**：客户端请求路径**原样拼接**到 `upstream` 后转发，不做任何改写、截断或前缀剥离。

**判定**：

| 客户端请求 | 上游收到 |
|---|---|
| `/v1/chat/completions` | `<upstream>/v1/chat/completions` |
| `/model/v1/chat/completions` | `<upstream>/model/v1/chat/completions` |
| `/a/b/c?x=1&y=2` | `<upstream>/a/b/c?x=1&y=2` |

**验证方法**：查看代理日志中的路径，或直连上游对比状态码。

**失败表现**：上游返回 404（路径被改写）。

---

### R3：请求头透传

**规则**：客户端请求头**原样透传**，仅两类例外：

| 请求头 | 处理 | 原因 |
|---|---|---|
| `Host` | 默认改写为上游主机名 | 上游按 Host 做虚拟主机路由 |
| `Connection`、`Keep-Alive`、`Transfer-Encoding` 等 | 移除 | HTTP 规范要求的逐跳头 |

**必须透传的头**：

- `Authorization`（凭据由客户端携带，代理不注入）
- `Content-Type`、`Accept`
- 自定义业务头（`X-Request-Id`、`X-Trace-Id` 等）

**判定**：

```bash
curl -s -o NUL -w "%{http_code}" -X POST http://127.0.0.1:8080/<path> \
  -H "Authorization: Bearer <key>" \
  -H "X-Custom-Test: hello" \
  -d '{...}'
# 期望：200（若自定义头被丢弃，上游可能拒绝或行为异常）
```

**失败表现**：上游返回 401（`Authorization` 丢失）。

---

### R4：Host 头处理

**规则**：`Host` 是唯一会被代理改写的请求头。

| 配置 | 转发时的 `Host` | 适用场景 |
|---|---|---|
| `preserve_host: false`（默认） | 上游主机名 | 上游按域名路由（**大多数情况**） |
| `preserve_host: true` | 客户端原始 `Host` | 上游要求保留原始 Host |

**判定**：若上游返回 404 而直连正常，检查 `Host` 是否匹配。

**排查命令**：

```bash
# 直连上游，手动设置本地 Host
curl -s -o NUL -w "%{http_code}" -X POST <upstream>/<path> \
  -H "Host: 127.0.0.1:8080" -H "Authorization: Bearer <key>" -d '{}'
# 若返回 404 → 上游按 Host 路由，必须保持 preserve_host=false
```

---

### R5：请求体逐字节透传

**规则**：请求体**不做任何解析、改写或重新序列化**，逐字节转发。

**判定**：上游收到的 body 与客户端发送的完全一致（含空格、字段顺序、Unicode）。

**失败表现**：上游报 JSON 解析错误、模型名丢失、`stream` 字段被改写。

---

### R6：限流排队（等待式）

**规则**：请求超过速率限制时**排队等待**，而非直接拒绝。

| 参数 | 行为 |
|---|---|
| `rate` | 每秒允许的请求数 |
| `burst` | 允许瞬时通过的请求数 |
| `max_wait` | 排队上限，超时返回 429 |

**判定**（`rate=1, burst=1`，连续 3 个请求）：

| 请求 | 期望耗时 | 说明 |
|---|---|---|
| 1 | 立即 | 消耗 burst |
| 2 | ~1000ms | 等待 1 个令牌周期 |
| 3 | ~2000ms | 等待 2 个令牌周期 |

**统计验证**：

```bash
curl -s http://127.0.0.1:8080/stats
# 期望：{"total":3,"waited":2,...}
```

**失败表现**：请求立即返回 429（未排队）、`waited` 计数为 0。

---

### R7：SSE 流式实时透传

**规则**：SSE 响应必须**逐块实时透传**，不得缓冲到响应结束。

**判定**：数据块到达时间戳**递增**，间隔接近上游发送间隔。

**验证脚本**：

```powershell
.\scripts\test-sse.ps1 -ApiKey <your-api-key>
```

**期望输出**：

```
[    161 ms] chunk #1
[    164 ms] chunk #2
[    185 ms] chunk #3
...
[    339 ms] DONE
```

**失败表现**：所有 chunk 时间戳几乎相同（被缓冲）。

**技术保障**：`FlushInterval = -1`（关闭缓冲）。

---

### R8：响应头与状态码透传

**规则**：上游响应的状态码与响应头**原样返回**给客户端。

**必须透传**：

- 状态码（200 / 400 / 401 / 429 / 500 等）
- `Content-Type`（`text/event-stream` / `application/json`）
- 自定义响应头（`X-Request-Id`、网关延迟头等）

**例外**：上游不可达时，代理返回 **502** 并附带 JSON 错误体：

```json
{"error":{"message":"upstream unreachable: ...","type":"upstream_error"}}
```

## 3. 测试层次与职责

| 层次 | 工具 | 覆盖链路环节 | 是否需要真实上游 |
|---|---|---|---|
| 单元测试 | `testing` | R2–R8（逻辑层） | ❌ 用 `httptest` 模拟 |
| 集成测试 | `httptest` + 真实 HTTP 服务 | R1–R8（含时序） | ❌ 用 `httptest` 模拟 |
| 端到端测试 | `curl` / `test-sse.ps1` | R1–R8（真实链路） | ✅ 需要 |

### 各层次验证重点

**单元测试**（`go test ./...`）：

- 逻辑正确性：路径拼接、头处理、限流算法
- 边界条件：并发、超时、取消、非法输入
- **不验证**：真实网络时序、真实网关行为

**集成测试**（`httptest.NewServer`）：

- 时序正确性：SSE 实时性（`TestProxy_SSEStreamingRealtime`）
- 并发安全：`TestProxy_PreserveHostConcurrent`
- **不验证**：真实 TLS、真实网关路由

**端到端测试**（真实上游）：

- 真实连通性：TLS 握手、DNS 解析
- 真实网关行为：Host 路由、认证、限流
- **必须验证**：R1、R3、R4、R7（这些环节模拟环境无法完全覆盖）

## 4. 测试执行顺序

```
① 单元测试（快速反馈）
   go test ./...
        │
        ▼
② 集成测试（时序验证）
   go test ./internal/proxy/ -run "SSE|Concurrent" -v
        │
        ▼
③ 启动代理
   .\streamguard.exe -config config.local.json
        │
        ▼
④ 端到端验证（按 R1→R8 顺序）
   R1 健康检查 → R2 路径 → R3 请求头 → R4 Host
   → R5 请求体 → R6 限流 → R7 SSE → R8 响应头
```

## 5. 判定标准汇总

| 规则 | 通过标准 | 失败信号 |
|---|---|---|
| R1 健康检查 | `/healthz` 返回 `{"status":"ok"}` | 连接拒绝 / 误转发 |
| R2 路径转发 | 上游收到完整原始路径 | 上游 404 |
| R3 请求头透传 | `Authorization` 等原样到达 | 上游 401 |
| R4 Host 处理 | 与上游路由要求匹配 | 上游 404（Host 不匹配） |
| R5 请求体透传 | 逐字节一致 | 上游 JSON 解析错误 |
| R6 限流排队 | 耗时递增，`waited` 计数正确 | 立即 429 / `waited=0` |
| R7 SSE 实时 | chunk 时间戳递增 | 时间戳几乎相同 |
| R8 响应透传 | 状态码与响应头一致 | 状态码被改写 |

## 6. 回归测试清单

每次修改代理核心逻辑后，必须重跑：

| 修改内容 | 必跑测试 |
|---|---|
| 路径拼接逻辑 | `TestProxy_ForwardBasic`、`TestProxy_ForwardsQueryString` |
| 请求头处理 | `TestProxy_ForwardsAllClientHeaders`、`TestProxy_DefaultRewritesHost`、`TestProxy_PreserveHost`、`TestProxy_PreserveHostConcurrent` |
| 请求体处理 | `TestProxy_ForwardsRequestBodyVerbatim`、`TestProxy_RequestBodyPreserved` |
| SSE 相关 | `TestProxy_SSEStreaming`、`TestProxy_SSEIncrementalDelivery`、`TestProxy_SSEStreamingRealtime` |
| 限流算法 | `internal/limiter` 全部用例 |
| 配置字段 | `internal/config` 全部用例 |

**完整回归命令**：

```bash
go test ./... && go vet ./...
```

## 7. 敏感信息规则

> ⚠️ **提交到 git 的文件中禁止出现真实域名、地址、API Key、公司信息。**

| 内容 | 存放位置 | 是否提交 |
|---|---|---|
| 真实上游地址 | `config.local.json` | ❌ 禁止 |
| 真实 API Key | `config.local.json` / 环境变量 | ❌ 禁止 |
| 真实模型清单 | `docs/local/upstream-models.local.md` | ❌ 禁止 |
| 通用占位符 | `config.example.json`、`docs/*.md` | ✅ 允许 |

**占位符规范**：

| 场景 | 占位符 |
|---|---|
| 上游地址 | `<your-upstream-host>/<base-path>` |
| API Key | `<your-api-key>` |
| 模型名 | `<model-name>` |
| 网关 | `<gateway>/<version>` |

**提交前检查**：

```bash
# 与 CI 敏感扫描规则保持一致（精确匹配真实值，避免误伤示例域名）
# 模式用字符类拆分书写（如 h[a]ier），敏感词本身不出现在仓库任何文件中
git grep -n -i -E "h[a]ier\.net|model[a]pi-test|api[k]ey-695f|695f046[7]" -- cmd/ internal/ scripts/ README.md docs/ config.example.json
# 期望：无输出
```

**忽略规则**（`.gitignore`）：

```
config.local.json
*.local.json
docs/local/
*.local.md
```

## 8. 常见失败模式

| 现象 | 根因 | 排查 |
|---|---|---|
| 上游 404 | `Host` 不匹配 | 见 R4 |
| 上游 401 | `Authorization` 丢失或无效 | 见 R3 |
| SSE 一次性返回 | 缓冲未关闭 | 检查 `FlushInterval = -1` |
| 请求立即 429 | 未进入排队逻辑 | 检查 `max_wait` 配置 |
| 端口占用 | 残留进程 | `netstat -ano \| findstr :8080` |
| 中文乱码 | 控制台 GBK 编码 | 非代理问题，用文件重定向验证 |
