# 火山多模态向量专用接入

本地新增平台为 **火山多模态向量**，平台标识 `ark_multimodal`。当前预置模型为
`doubao-embedding-vision-251215`，只提供火山原生多模态向量接口。

## 配置

1. 在账号管理创建「火山多模态向量」API Key 账号。
2. Base URL 填 `https://ark.cn-beijing.volces.com/api/v3`，Key 填火山方舟签发的 Key。
   当前接入限定该官方北京端点；不能填写到 `/embeddings/multimodal` 的完整操作地址。
3. 模型白名单默认包含 `doubao-embedding-vision-251215`。可使用账号模型映射配置调用别名。
4. 创建同平台分组，把账号加入该分组；也支持 Composite 分组。为该分组签发 Sub2API Key。
5. 在分组逐模型定价中设置 Token 模式，分别配置文本输入价与图片输入价。
   使用页面标注的 USD / 百万 token；这些是对调用方售价，不能直接当作火山官方成本。
   若未覆盖价格，仍遵循现有系统定价解析和兜底规则。

Composite 默认能识别上述预置模型。自定义别名建议配置 endpoint=`embeddings` 或 `any`
的显式 Composite 路由，目标平台选 `ark_multimodal`；模型价格属于当前 Key 的分组。
平台清单中的 `gateway=openai` 仅表示复用内部调度/结算服务，不表示对外提供 OpenAI 协议。

## 调用

对外主接口为 `POST /v1/embeddings/multimodal`。另提供
`/api/v3/embeddings/multimodal` 别名，共用鉴权、模型白名单及请求体大小限制。
不提供 `/v3` 或无前缀别名，避免嵌入式前端把这些路径作为页面处理，也避免为别名修改上游前端服务。

```bash
curl "$SUB2API_BASE_URL/v1/embeddings/multimodal" \
  -H "Authorization: Bearer $SUB2API_API_KEY" \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "doubao-embedding-vision-251215",
    "input": [{"type": "text", "text": "需要向量化的文字"}]
  }'
```

图文联合输入采用火山的内容对象数组：

```json
{
  "model": "doubao-embedding-vision-251215",
  "input": [
    {"type": "text", "text": "一张蓝色图片"},
    {"type": "image_url", "image_url": {"url": "https://example.com/image.png"}}
  ]
}
```

`input` 里的多个内容项保持火山原生含义，不转换成 OpenAI 文本批次。
响应原样保留 `data.embedding`、`usage` 等字段，不包装为 `data[0].embedding`。
标准 OpenAI SDK 的 `embeddings.create()` 不直接适用于这条接口。
`GET /v1/models` 或 `/models` 可查看专用平台的预置模型。

## 用量与边界

- 使用上游 `usage.prompt_tokens` 总输入量和 `prompt_tokens_details.image_tokens` 图片量，
  复用现有结算中的文本/图片不同价计算、分组倍率、配额和 Kox 同步。
- 失败响应不会生成成功用量。同步请求没有异步任务、轮询或视频结算状态。
- 专用平台拒绝普通 `/v1/embeddings`、聊天、Responses、图片和视频生成等入口。
- 原有 OpenAI embeddings handler、forwarder 和普通 `/v1/embeddings` 注册逻辑保持不变。
- Key 由账号配置保存；源代码、示例和测试不包含真实供应商 Key。

## 验证与维护

常规测试覆盖协议保持、模型映射、错误响应、平台隔离、路由、调度、图文计费和账号表单。
真实接口测试需显式在进程环境设置 `ARK_MULTIMODAL_TEST_API_KEY`：

```bash
cd backend
go test -tags unit ./internal/service -run '^TestArkMultimodalLive$' -count=1 -v
```

该测试会发起一次文字请求和一次小型内嵌图片+文字请求，产生火山实际用量；默认跳过。
2026-11-24 经本地新增 forwarder 验证：两种输入均返回 2048 维向量；图文请求中图片 token
明细正常返回。该验证不等于部署后的数据库、真实用户余额或 Kox webhook 已完成联调。

归属：新增 handler/service 使用上游 embeddings 生命周期与转发脚手架派生，保留其来源；
新增的火山平台限制、原生契约、路由和测试为本地扩展。平台常量、端点身份和具体路由注册留在新增文件中；原源码只保留平台清单、
路由扩展挂载、Composite 模型识别及前端账号表单的必要接入点。
后续升级应检查这些接入点，以及上游共享调度、用量解析、计费 helper 的接口变化。


2026-11-25 按“多新增、少修改”收敛：已有后端生产文件仅在 3 个位置新增共 8 行；
完整文件统计及维护影响见 [改动边界](ARK_MULTIMODAL_CHANGE_BOUNDARIES.md)。
平台隔离判断仍挂在公共路由入口，对其他平台直接放行；并非完全零公共入口改动。
