# 自研视频适配器：路由、账单与升级合并约定

本文记录对官方 Sub2API 的本地扩展边界。目标是让不同厂商的视频生成协议经由既有 Grok 视频入口对外提供服务，同时不复制账户选择、鉴权、限流、用量和账单逻辑。

## 对外契约不变

客户端始终使用现有 Grok 视频路由，不调用厂商接口：

| 目的 | 公共路由 |
| --- | --- |
| 创建 | `POST /v1/videos/generations` |
| 编辑 | `POST /v1/videos/edits` |
| 延长 | `POST /v1/videos/extensions` |
| 查询 | `GET /v1/videos/{request_id}` |
| 下载 | `GET /v1/videos/{request_id}/content` |

账户仍然使用 `platform: "grok"`。因此用户 API Key、分组、模型白名单、账户调度、并发、优先级和计费配置全都沿用官方 Grok 路径。每家上游厂商建一个账户，绑定到同一 Grok 分组；通过 `model_mapping` 将公共模型名映射到上游模型名。

## 路由拦截位置

路由注册及其鉴权/模型白名单中间件不做厂商分支。视频请求最终进入 `OpenAIGatewayService.ForwardGrokMedia`：

```text
Gateway routes + existing middleware
  -> ForwardGrokMedia (grok_media.go)
     -> forwardAdaptedGrokVideo (grok_video_adapter.go)   [仅视频、仅配置 adapter 的账户]
        -> adapter.PrepareRequest
        -> adapter.BuildURL
        -> upstream request
        -> adapter.NormalizeResponse
     -> 原生 xAI Grok 转发路径                               [未配置 adapter 时]
```

`forwardAdaptedGrokVideo` 是唯一的协议拦截点。`videoAdapterOperation` 只把既有 `GrokMediaEndpoint` 映射为 `create`、`status`、`content`、`edit`、`extension` 操作；不增加新路由，也不改变原生账户的行为。没有 `credentials.video_adapter` 或其值为 `native` 时，函数返回 `handled=false`，官方 xAI 逻辑保持原样。

内容下载不直接把上游签名 URL 暴露给客户端。客户端仍请求公共 `.../content` 路由；服务端先查询任务状态，再调用 `ResolveContent` 获取上游资源。所有 adapter 的资源地址可为任意 HTTPS 主机，以兼容厂商 CDN 和租户 OSS；下载时**不转发账户 API Key**。基础校验仅拒绝非 HTTPS、URL 用户信息和非默认 HTTPS 端口。

## 适配器边界

适配器只放在 `backend/internal/videoadapter`，并实现：

```go
type Adapter interface {
    Kind() Kind
    BuildURL(baseURL, requestID string, operation Operation) (string, error)
    PrepareRequest(operation Operation, body []byte, contentType string) (PreparedRequest, error)
    NormalizeResponse(operation Operation, body []byte, requestID, contentProxyURL string) ([]byte, error)
    ResolveContent(baseURL, requestID string, statusBody []byte) (ContentRequest, error)
}
```

适配器名称必须是唯一、稳定、供应商前缀化的字符串，例如 `ctyun_minimax`、`volcengine_ark`；不要按模型名称自动推断协议，也不要将多个供应商塞进同一 adapter。选择逻辑只在 `videoadapter.Resolve` 注册。

| adapter | 用途 | 上游协议 |
| --- | --- | --- |
| `native` | 官方 xAI | 原始 Grok 视频协议 |
| `openai_videos` | 兼容 OpenAI Videos 的网关 | `/videos` |
| `volcengine_ark` | 火山 Ark | `/contents/generations/tasks` |
| `ctyun_minimax` | 天翼云 MiniMax-H3 | `/video_generation`、`/query/video_generation/{id}` |

新厂商的请求字段转换、状态枚举、任务 ID 提取、完成视频 URL 提取和厂商限制都应在其独立 adapter 内完成。不要在 `grok_media.go`、路由层、调度器或账单服务中加入 `if vendor == ...`。

## 账单不变量

账单仍由既有 Grok 账单链路处理；adapter 不直接写用量表、不扣余额、不创建账单记录。

1. 创建请求先由 `ParseGrokMediaRequest` 读取公共模型、分辨率和时长。
2. `PrepareRequest` 返回转换后的 `PreparedRequest`；其中 `VideoResolution` 和 `VideoDurationSeconds` 会回写到请求元数据，确保账单采用上游实际请求值（含 adapter 默认值）。
3. `NormalizeResponse` 必须保留或生成公共 `request_id`、`status` 和 `model`。创建阶段只记录异步任务标识及创建时的计价参数。
4. `grokMediaUsageFromResponse` 在状态查询看到完成 `video.url` 后才设置视频数量并完成异步视频用量归集，避免创建时重复收费。
5. `ForwardGrokMedia` 返回的 `OpenAIForwardResult` 仍包含公共模型、映射后的 upstream model、响应 ID、视频分辨率和时长，供既有审计与账单流程消费。

禁止在 adapter 中把上游原始用量字段当作本地收费依据，除非先在公共账单模型中新增经过评审的映射；否则会造成与现有价格表、重试和状态轮询不一致。

## 配置示例：天翼云 MiniMax-H3

```json
{
  "platform": "grok",
  "type": "apikey",
  "credentials": {
    "api_key": "<CTYUN_APP_KEY>",
    "base_url": "https://ai.ctaigw.cn/v1",
    "video_adapter": "ctyun_minimax",
    "model_mapping": {
      "minimax-h3": "MiniMax-H3"
    }
  }
}
```

`video_adapter` 是**账户级**字段：同一账户的所有映射模型必须使用相同上游协议。不同协议请创建不同账户；若多个账户映射同一公共模型并绑定同一分组，既有调度器会按账户的并发、优先级和可用性选择。

## 新增 adapter 的最小变更清单

1. 在 `backend/internal/videoadapter/<vendor>.go` 实现 `Adapter`。
2. 在 `adapter.go` 添加唯一 `Kind` 并在 `Resolve` 注册；不改账户数据表。
3. 添加 adapter 单测：URL、请求转换、创建/查询/失败状态归一化、签名资源下载和非法 URL 基础校验。
4. 在 `backend/internal/service/grok_video_adapter_test.go` 添加一次转发契约测试，断言上游 URL、请求体和公共响应。
5. 更新 `extensions/video/README.md` 的配置表与本文件的 adapter 表。
6. 执行：

```bash
go test ./internal/videoadapter
go test ./internal/service -run '^TestForwardGrokVideo' -count=1
go test ./internal/service -run '^TestGrokMediaVideo' -count=1
```

## 官方升级/合并策略

- 优先把自研改动限定在 `backend/internal/videoadapter/`、`grok_video_adapter.go` 和其测试；不要改路由注册、模型白名单或账单入口。
- 合并官方更新时，首先保留 `ForwardGrokMedia` 原生分支；确认 `forwardAdaptedGrokVideo` 仍在原生请求构造之前且只处理 `enabled=true` 的 adapter 账户。
- 若官方修改 `GrokMediaEndpoint`、`OpenAIForwardResult`、`GrokMediaRequestInfo` 或 `grokMediaUsageFromResponse`，逐项核对本文件“账单不变量”，不要用整段覆盖解决冲突。
- 合并后至少跑“新增 adapter 最小变更清单”的测试，并对每个已接入供应商做一次创建、一次状态查询、一次内容下载冒烟测试。
- 自研配置字段只使用 `credentials.video_adapter`；避免修改公共 API schema，从而降低迁移和回滚成本。

## 排障顺序

1. `GET /v1/models`：确认用户 Key 在目标 Grok 分组且模型可见。
2. 创建返回 404：核对 `base_url` 是否已经含正确版本前缀，以及该账户选择的 adapter 协议。
3. 创建返回认证错误：核对该账户上游 API Key，不要检查用户 API Key。
4. 创建成功、查询失败：检查 adapter 的 `task_id`、查询路径及状态嵌套字段。
5. 状态 `done`、内容下载失败：查看服务端到签名资源 URL 的网络/DNS/代理日志；确认资源 URL 为 HTTPS。资源下载不带上游 API Key。
