# 测试用例文档

本文档记录 StreamGuard 的测试策略、用例清单与端到端验证方法。

> 全链路测试规则（R1–R8）与判定标准见 [test-rules.md](./test-rules.md)。

## 测试策略

| 层次 | 工具 | 覆盖范围 |
|---|---|---|
| 单元测试 | 标准库 `testing` | 配置解析、限流算法、代理转发、HTTP 服务 |
| 集成测试 | `httptest` 模拟上游 | 端到端请求链路、SSE 流式透传 |
| 手工验证 | `curl` + 真实上游 | 真实网关连通性、认证、流式响应 |

运行全部测试：

```bash
go test ./...
```

## 单元测试清单

### 0. CLI 入口（`cmd/streamguard`，7 个）

| 用例 | 验证点 |
|---|---|
| `TestFindConfigFile_Priority` | 配置文件查找优先级（当前目录 > exe 目录 > exe 上级目录） |
| `TestFindConfigFile_NotFound` | 无配置文件时返回空字符串 |
| `TestResolveConfigPath_Explicit` | 显式指定路径时直接返回，不做查找 |
| `TestResolveConfigPath_Empty` | 空路径时回退到 `findConfigFile` |
| `TestLoadConfig_Defaults` | 无配置文件时使用内置默认值 |
| `TestLoadConfig_FromFile` | 从指定文件加载配置 |
| `TestLoadConfig_InvalidFile` | 非法 JSON 报错 |

### 1. 配置加载（`internal/config`，19 个）

| 用例 | 验证点 |
|---|---|
| `TestDefault` | 默认值正确（listen/upstream/rate/burst/max_wait/timeout/log_level） |
| `TestLoadFromFile` | 从 JSON 文件加载配置 |
| `TestLoadMissingFile` | 配置文件不存在时的处理 |
| `TestLoadInvalidJSON` | 非法 JSON 报错 |
| `TestApplyEnv` | 环境变量覆盖生效 |
| `TestApplyEnvInvalidValue` | 非法环境变量被忽略，保留原值 |
| `TestNormalize` | 配置归一化（默认值填充） |
| `TestValidate` | 合法/非法配置校验（地址、日志级别） |
| `TestNormalizeThenValidate` | **先 Normalize 后 Validate 约定**：负数被规整为默认值 |
| `TestDefault_Retry` | 重试配置默认值 |
| `TestLoadFromFile_Retry` | 从文件加载重试配置 |
| `TestApplyEnv_Retry` | 重试环境变量覆盖 |
| `TestNormalize_Retry` | 重试配置归一化 |
| `TestValidate_Retry` | 重试配置校验 |

### 2. 等待式限流器（`internal/limiter`，12 个）

| 用例 | 验证点 |
|---|---|
| `TestWaiter_ImmediateWhenAvailable` | 有配额时立即通过，无需等待 |
| `TestWaiter_OnePerSecond` | 按 1 次/秒速率限流 |
| `TestWaiter_BlocksWhenExhausted` | 配额耗尽时阻塞等待 |
| `TestWaiter_MaxWaitTimeout` | 超过最大等待返回超时 |
| `TestWaiter_ContextCancel` | 客户端取消时立即返回 |
| `TestWaiter_ConcurrentOrdering` | 并发下等待者按到达顺序获得配额（FIFO） |
| `TestWaiter_InvalidParams` | 非法参数处理 |
| `TestWaiter_Stats` | 统计计数（total/waited）准确 |
| `TestWaiter_StatsNoDoubleCountUnderContention` | **高并发下统计不重复计数**（回归：此前递归重试导致虚高） |
| `TestWaiter_StatsWaitDuration` | 等待耗时统计（avg/max/min） |
| `TestWaiter_StatsTimedOut` | 等待超时计数 |
| `TestWaiter_StatsCanceled` | 等待取消计数 |

### 2b. 按请求大小分档的并发限流（`internal/limiter`，23 个）

**并发限流器（`concurrency_test.go`，12 个）**

| 用例 | 验证点 |
|---|---|
| `TestConcurrencyLimiter_SmallRequestImmediate` | 小请求有空闲槽位时立即放行 |
| `TestConcurrencyLimiter_ThresholdBoundary` | 阈值边界：等于阈值算小请求，大于算大请求 |
| `TestConcurrencyLimiter_SmallLimitEnforced` | 小请求并发上限被强制执行 |
| `TestConcurrencyLimiter_LargeLimitEnforced` | 大请求并发上限独立于小请求 |
| `TestConcurrencyLimiter_UnlimitedWhenZero` | 上限为 0 时不限制并发 |
| `TestConcurrencyLimiter_MaxWaitTimeout` | 等待超过 maxWait 返回 ErrWaitTimeout |
| `TestConcurrencyLimiter_ContextCancel` | ctx 取消时返回 ctx 错误 |
| `TestConcurrencyLimiter_FIFOOrdering` | 等待者按 FIFO 顺序获得槽位 |
| `TestConcurrencyLimiter_ReleaseIdempotent` | 重复调用 release 不会导致计数错误 |
| `TestConcurrencyLimiter_NoSlotLeakUnderContention` | 高并发下无槽位泄漏且不超限 |
| `TestConcurrencyLimiter_StatsCounts` | 统计计数正确 |
| `TestConcurrencyLimiter_DefaultThreshold` | 阈值 <=0 时规整为 10000 |

**token 估算（`tokens_test.go`，11 个）**

| 用例 | 验证点 |
|---|---|
| `TestEstimateTokens_EmptyBody` | 空请求体返回 0 |
| `TestEstimateTokens_ChatBody` | 从 chat/completions 请求体估算 token |
| `TestEstimateTokens_MultipleMessages` | 多条 message 累加 |
| `TestEstimateTokens_MultimodalContent` | 多模态 content 数组只统计 text 部分 |
| `TestEstimateTokens_NonChatBodyFallsBack` | 非 chat 结构退化为整体字节估算 |
| `TestEstimateTokens_InvalidJSONFallsBack` | 非法 JSON 退化为整体字节估算 |
| `TestEstimateTokens_ChineseText` | 中文文本估算（UTF-8 3 字节/字） |
| `TestEstimateTokens_RoundsUp` | 向上取整，小请求不会被估成 0 |
| `TestEstimateTokens_EmptyContentFallsBack` | content 为空时退化为整体估算 |
| `TestEstimateTokens_ThresholdScenario` | 用户场景：小请求 vs 大请求分档 |
| `TestCountRunes` | 字符计数辅助函数 |

### 3. 反向代理与透传（`internal/proxy`，33 个）

| 用例 | 验证点 |
|---|---|
| `TestProxy_ForwardBasic` | 基础请求转发与响应透传 |
| `TestProxy_SSEStreaming` | SSE 响应逐块透传 |
| `TestProxy_SSEIncrementalDelivery` | SSE 增量投递 |
| `TestProxy_SSEStreamingRealtime` | **SSE 实时性**：块间隔接近上游发送间隔，证明未被缓冲 |
| `TestProxy_ForwardsAllClientHeaders` | **请求头原样透传**：`Authorization`/`Content-Type`/`Accept`/`User-Agent`/自定义头 |
| `TestProxy_ForwardsQueryString` | 查询参数原样透传 |
| `TestProxy_ForwardsResponseHeaders` | 上游响应头原样返回 |
| `TestProxy_ForwardsRequestBodyVerbatim` | 请求体逐字节透传，不做改写 |
| `TestProxy_RequestBodyPreserved` | 请求体内容保持完整 |
| `TestProxy_DefaultRewritesHost` | 默认将 `Host` 改写为上游主机名 |
| `TestProxy_PreserveHost` | `preserve_host=true` 时保留客户端原始 `Host` |
| `TestProxy_PreserveHostConcurrent` | **并发安全**：并发请求的 `Host` 互不干扰 |
| `TestProxy_UpstreamUnreachable` | 上游不可达返回 502 |
| `TestProxy_UpstreamError` | 上游错误响应透传 |
| `TestNew_InvalidUpstream` | 非法上游地址在构造时报错 |
| `TestProxy_RetryDisabledByDefault` | 默认不重试 |
| `TestProxy_RetryOn429ThenSuccess` | 429 后重试成功 |
| `TestProxy_RetryExhaustedPassesThrough` | 重试耗尽透传最后一次响应 |
| `TestProxy_RetryNotTriggeredOn500` | 500 不触发重试 |
| `TestProxy_RetryPreservesRequestBody` | 重试时请求体完整重放 |
| `TestProxy_RetryRespectsRetryAfterHeader` | 等待受 `Retry-After` 与上限约束 |
| `TestProxy_RetryWithBreaker` | 重试与熔断器联动 |
| `TestProxy_TimeoutAppliesToNonSSE` | **非 SSE 请求受 Timeout 约束**（回归：此前 timeout 死配置） |
| `TestProxy_TimeoutExemptsSSE` | SSE 请求（body stream=true）豁免超时 |
| `TestProxy_TimeoutExemptsSSEByAcceptHeader` | SSE 请求（Accept 头）豁免超时 |
| `TestProxy_NoDuplicateXForwardedFor` | **XFF 不重复注入**（回归：此前上游收到 "ip, ip"） |
| `TestProxy_PreservesClientXForwardedFor` | 客户端自带 XFF 被保留并追加 |

### 4. HTTP 服务（`internal/server`，22 个）

| 用例 | 验证点 |
|---|---|
| `TestServer_Healthz` | `/healthz` 健康检查 |
| `TestServer_Stats` | `/stats` 返回统计信息 |
| `TestServer_ProxyChatCompletions` | `/v1/chat/completions` 转发到代理 |
| `TestServer_SSEPassthrough` | SSE 流式响应透传 |
| `TestServer_RateLimitWaits` | 超限请求排队等待 |
| `TestServer_RateLimitTimeout429` | 等待超时返回 429 |
| `TestServer_UnknownPathFallsBackToDefault` | 未知路径回退到默认代理处理 |
| `TestServer_UpstreamDownReturns502` | 上游不可达返回 502 |
| `TestServer_ClientCancelDuringWait` | 等待期间客户端取消 |
| `TestServer_SizeLimitSmallConcurrent` | 小请求并发上限生效（串行执行） |
| `TestServer_SizeLimitLargeRequestBlocked` | 大请求并发上限独立生效 |
| `TestServer_SizeLimitTimeout429` | 并发等待超时返回 429 |
| `TestServer_SizeLimitDisabledByDefault` | 未配置并发限流时行为不变（并行执行） |
| `TestServer_SizeLimitStats` | `/stats` 输出大小限流统计 |
| `TestServer_SizeLimitPreservesBody` | token 估算读取请求体后代理仍能读到完整 body |
| `TestServer_VerboseLogsRequestAndResponse` | 详细模式打印请求/响应内容 |
| `TestServer_VerboseDisabledByDefault` | 默认不打印内容，避免敏感信息泄漏 |
| `TestServer_VerboseSSEStreamsChunks` | 详细模式下 SSE 逐块打印且保持实时 |
| `TestVerboseRecorder_FlushPassthrough` | `Flush` 透传（SSE 实时性关键） |
| `TestServer_VerboseLoggerSeparatedFromMainLogger` | 详细日志与主日志分离 |
| `TestServer_StatsFieldOrderStable` | **/stats 字段顺序稳定**（回归：此前 map 序列化顺序随机） |
| `TestServer_StatsFieldOrderStable` | **/stats 字段顺序稳定**（回归：此前 map 序列化顺序随机） |

### 5. 应用核心层（`internal/app`，21 个）

| 用例 | 验证点 |
|---|---|
| `TestApp_StartStop` | 启动与停止 |
| `TestApp_Stats` | 统计信息 |
| `TestApp_Addr` | 监听地址获取 |
| `TestApp_Config` | 配置读取 |
| `TestApp_UpdateConfig` | 配置更新 |
| `TestApp_ProxiesRequest` | 请求经应用层正确代理 |
| `TestApp_DoubleStart` | 重复启动保护 |
| `TestApp_StopWhenNotRunning` | 未运行时停止不报错 |
| `TestApp_StopWithActiveLongConnection` | **长连接场景**：Shutdown 超时后强制关闭，状态一致 |
| `TestApp_VerboseLogFile` | `log_file` 指定路径时详细日志写入文件 |
| `TestApp_VerboseLogFileAuto` | `log_file="auto"` 自动生成按日期命名的日志 |
| `TestApp_VerboseLogFileRelativePath` | 相对路径基于当前目录解析到 `logs/` 下 |
| `TestApp_VerboseLogFileAbsolutePath` | 绝对路径不被改写 |
| `TestApp_VerboseLogFileInvalidPath` | 路径非法时回退到控制台，不阻断启动 |
| `TestApp_VerboseLogFileClosedOnStop` | 停止后文件句柄被关闭（Windows 可删除） |
| `TestApp_VerboseLogFileDisabledWhenNotDebug` | 非 debug 级别不创建日志文件 |
| `TestConfig_LogFileEnvOverride` | `STREAMGUARD_LOG_FILE` 环境变量覆盖 |

### 6. 上游限流重试（`internal/retry`，11 个）

| 用例 | 验证点 |
|---|---|
| `TestPolicy_DisabledNeverRetries` | 未启用时永不重试 |
| `TestPolicy_DefaultStatuses` | 默认只重试 429/503 |
| `TestPolicy_CustomStatuses` | 自定义重试状态码 |
| `TestPolicy_Defaults` | 零值选项回退到默认值 |
| `TestPolicy_ExponentialBackoff` | 指数退避 1s→2s→4s→8s |
| `TestPolicy_BackoffClampedByMaxWait` | 退避受 `retry_max_wait` 约束 |
| `TestPolicy_RespectsRetryAfterSeconds` | 尊重 `Retry-After` 秒数 |
| `TestPolicy_RetryAfterClampedByMaxWait` | `Retry-After` 受上限约束 |
| `TestPolicy_RespectsRetryAfterHTTPDate` | 尊重 `Retry-After` HTTP 日期格式 |
| `TestPolicy_InvalidRetryAfterFallsBackToBackoff` | 非法 `Retry-After` 回退到退避 |
| `TestPolicy_NilSafe` | nil 策略安全（不 panic） |

## 端到端验证（真实上游）

### 前置条件

1. 已编译 `streamguard.exe`
2. `config.local.json` 已配置真实上游地址
3. 持有有效的上游 API Key

### 步骤 1：启动代理

```bash
.\streamguard.exe -config config.local.json
```

预期输出：

```
config loaded: listen=127.0.0.1:8080 upstream=<your-upstream> preserve_host=false rate=1.00/s burst=1 max_wait=30s
[app] server started, listening on 127.0.0.1:8080
rate limit: waiting mode, 1.00 req/s, burst 1, max wait 30s
```

### 步骤 2：健康检查

```bash
curl http://127.0.0.1:8080/healthz
```

预期：`{"status":"ok"}`

### 步骤 3：SSE 流式请求（核心验证）

```bash
curl -N -X POST http://127.0.0.1:8080/model/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <your-api-key>" \
  -d '{
    "model": "<model-name>",
    "messages": [{"role": "user", "content": "你好"}],
    "stream": true
  }'
```

**预期结果**：

1. 响应头 `Content-Type: text/event-stream`
2. 数据块**逐块实时输出**（不是等全部生成完才一次性返回）
3. 以 `data: [DONE]` 结束

**验证实时性的方法**：观察输出是否「逐字/逐块」出现。若所有内容瞬间一起出现，说明被缓冲。

也可用脚本自动测量每个数据块的到达时间：

```powershell
.\scripts\test-sse.ps1 -ApiKey <your-api-key>
```

脚本会打印每个 chunk 的到达时间戳。**若时间戳递增，说明实时透传生效**。

### 步骤 4：限流验证

连续发送 3 个请求，观察耗时递增：

```bash
for ($i=1; $i -le 3; $i++) {
  $t = Measure-Command {
    curl.exe -s -o NUL -X POST http://127.0.0.1:8080/model/v1/chat/completions `
      -H "Content-Type: application/json" `
      -H "Authorization: Bearer <your-api-key>" `
      -d '{\"model\":\"<model-name>\",\"messages\":[{\"role\":\"user\",\"content\":\"hi\"}]}'
  }
  Write-Host "请求 $i 耗时: $($t.TotalMilliseconds) ms"
}
```

**预期**（`rate=1, burst=1`）：

| 请求 | 耗时 | 说明 |
|---|---|---|
| 1 | ~100ms | 立即通过 |
| 2 | ~1000ms | 等待约 1s |
| 3 | ~2000ms | 等待约 2s |

### 步骤 5：统计验证

```bash
curl http://127.0.0.1:8080/stats
```

预期：

```json
{"upstream":"<your-upstream>","rate":1,"burst":1,"total":3,"waited":2}
```

`waited=2` 表示后两个请求发生了排队等待。

## 实测记录

以下为对真实上游（OpenAI 兼容网关）的验证结果。

### SSE 流式透传 ✅

```
POST http://127.0.0.1:8080/<base-path>/v1/chat/completions
model=<model-name> stream=true
----------------------------------------
[    161 ms] chunk #1
[    164 ms] chunk #2
[    185 ms] chunk #3
[    211 ms] chunk #4
[    234 ms] chunk #5
[    258 ms] chunk #6
[    284 ms] chunk #7
[    307 ms] chunk #8
[    335 ms] chunk #9
[    336 ms] chunk #10
[    338 ms] chunk #11
[    339 ms] DONE
----------------------------------------
total chunks : 11
first chunk  : 161 ms
total time   : 348 ms
```

**结论**：数据块时间戳递增（161→164→185→211→234→258→284→307→335ms），
证明 SSE **逐块实时透传，未被缓冲**。

### 响应头透传 ✅

上游响应头完整返回给客户端：

```http
HTTP/1.1 200 OK
Content-Type: text/event-stream
Via: <gateway>/<version>
X-Gateway-Proxy-Latency: 0
X-Gateway-Upstream-Latency: 9
Transfer-Encoding: chunked
```

### 普通 JSON 响应 ✅

非流式请求同样正常透传：

```http
HTTP/1.1 200 OK
Content-Type: application/json
```

```json
{"id":"chatcmpl-...","object":"chat.completion","model":"<model-name>",
 "choices":[{"index":0,"message":{"role":"assistant","content":"Hi! How can I"},
 "finish_reason":"length"}],"usage":{"prompt_tokens":5,"total_tokens":10}}
```

### 限流排队 ✅

连续 3 个请求的代理日志：

```
[proxy] POST /<base-path>/v1/chat/completions client=127.0.0.1 elapsed=4.5659716s
[proxy] POST /<base-path>/v1/chat/completions client=127.0.0.1 elapsed=531.4891ms
[proxy] POST /<base-path>/v1/chat/completions client=127.0.0.1 elapsed=754.4099ms
```

统计接口：

```json
{"burst":1,"rate":1,"total":4,"upstream":"<upstream>","waited":1}
```

**结论**：`waited=1` 证明排队等待生效。

### 自定义请求头透传 ✅

携带 `X-Custom-Test`、`X-Request-Id` 请求头，上游正常响应 200，
证明自定义头被原样透传（未被代理丢弃或拒绝）。

## 常见问题排查

### 上游返回 404

**原因**：`Host` 头与上游虚拟主机不匹配。

**排查**：

```bash
# 直连上游对比
curl -s -o NUL -w "HTTP %{http_code}\n" -X POST <upstream>/<path> \
  -H "Host: 127.0.0.1:8080" -H "Authorization: Bearer <key>" -d '{}'
```

- 若 `Host: 127.0.0.1:8080` 返回 404，而 `Host: <upstream-host>` 返回 401/200
  → 上游按 `Host` 路由，**必须保持 `preserve_host=false`**（默认值）

### 上游返回 401

**原因**：缺少或无效的 `Authorization` 头。

**说明**：这是**正常行为**，说明路径与 Host 均正确，仅凭据无效。StreamGuard 不做凭据注入，客户端需自行携带。

### SSE 响应被缓冲

**排查**：确认代理使用 `FlushInterval = -1`（已内置，无需配置）。

**验证**：`go test ./internal/proxy/ -run TestProxy_SSEStreamingRealtime -v`

## 测试覆盖统计

| 模块 | 用例数 |
|---|---|
| CLI 入口 | 7 |
| 配置加载 | 19 |
| 等待式限流器 | 12 |
| 并发限流器 | 12 |
| token 估算 | 11 |
| 反向代理与透传 | 33 |
| HTTP 服务 | 22 |
| 应用核心层 | 21 |
| 上游熔断器 | 8 |
| 上游限流重试 | 11 |
| **合计** | **156** |
