# 当前项目源码归属与改动边界清单

核查日期：2026-10-26。本清单用于后续需求定界、代码审查和上游合并；本次只新增说明文档及逐文件清单，不修改业务代码。

## 核查基线与结论

- 项目快照：`f09b94ec106f4b2ad2c8605917c99d27139a0375`（本次整理前的 `main`）。
- 上游来源：Git 远端 `upstream`，`https://github.com/Wei-Shaw/sub2api.git`。
- 当前 Fork：Git 远端 `origin`，`https://github.com/kiiik0120/sub2api.git`。`DEV_GUIDE.md` 中的 Fork 名称是历史信息，归属核查以 Git 远端和提交关系为准。
- 已合入上游基线：`aea725f2ea644d5592d0bbb1d63b607efa7e200a`，即 `v0.2.7^{commit}`；合并提交 `7f59c2b87` 的第二父提交也是该提交，且它是本地 `HEAD` 与当前本地 `upstream/main` 引用的共同祖先。
- 基线已有的文件构成原项目来源；在这些文件中嵌入的定制内容属于“原项目源码修改”，文件整体仍按混合文件管理。
- 快照共 4013 个 Git 跟踪文件，其中 3962 个与基线相同、23 个相对基线新增、28 个基线文件发生修改；无净删除或重命名。
- 此处“自研新增”表示本 Fork 相对该基线新增、由本地扩展历史支持的维护边界，不是原创版权认定，也不表示整个 Git 历史从未出现过同名文件。

本清单比较的是已合入基线与当前快照，不是与上游最新版本比较，也不是全部历史修改累计。未跟踪/被忽略的本机文件、运行数据库和部署实际配置不在该统计内；本次不联网刷新上游引用。

## 分类与修改原则

| 分类 | 认定依据 | 后续修改边界 |
| --- | --- | --- |
| 原项目源码，与基线一致 | 基线与快照均存在且 Git blob 无净差异 | 按原项目源码声明修改必要性、影响与升级风险 |
| 原项目源码修改，混合文件 | 基线已有文件，当前存在净差异 | 按函数、字段或代码块区分上游主体与定制段，避免整文件覆盖 |
| 自研新增文件 | 当前存在、基线不存在，并由本地扩展历史/内容支持 | 主要在独立模块演进；涉及原源码接入点仍须单独声明 |
| 归属待确认 | 缺少来源证据，或本机/部署内容不在 Git 快照内 | 先核实来源，再声明允许修改范围 |

所有代码仍须遵守仓库许可证和既有贡献来源。新增目录名称不能代替来源核查，文件作者或提交标题也不能单独证明原创归属。

## 原项目源码范围

`backend/`、`frontend/`、`deploy/`、`docs/` 等目录是上游主体与本地扩展并存的目录，不能整体称为自研。完整文件归属见 [逐文件清单](CHANGE_BOUNDARIES_FILES.tsv)，其中列出了每个跟踪文件的基线状态。

- `frontend/`：当前全部跟踪文件与基线一致。页面、组件、路由、API 封装、国际化均按原项目源码管理。
- `backend/ent/`、`backend/migrations/`：当前与基线一致。Ent Schema/生成代码、上游 SQL 迁移均按原项目来源管理；自研数据库变更优先进入独立扩展迁移。
- 上游已有的鉴权、账户、分组、调度、限流、用量、计费、支付、插件与管理能力，不能因被 Kox 调用就认定为自研。
- `backend/internal/service/seedance.go`、`backend/internal/handler/seedance.go` 的原生 Seedance 接口来自上游；本地新增的是计价属性提取、单价矩阵及快照传递。
- `scripts/`、`tools/`、`skills/`、`openspec/`、`.github/` 当前跟踪内容与基线一致。包括 prompt audit 与 Codex manifest 相关 OpenSpec，不能凭名称推定为本地自研。

## 自研功能与原源码接入边界

| 功能 | 自研新增范围 | 原源码中的定制接入范围 | 维护约束 |
| --- | --- | --- | --- |
| Kox 内部 Key 与账单 | `backend/internal/koxbilling/`、`backend/internal/server/routes/kox_billing.go`、`backend/cmd/backfill-kox-usage/main.go` | `wire_gen.go`、`server/http.go`、`server/router.go`、用量命令及仓库、`web/embed_on.go` | 保留服务间鉴权、真实网关 Key 映射、用量事务及事件投递边界；不将整个原计费系统归为自研 |
| Kox 数据库扩展 | `backend/local-migrations/`、`extensions/kox/` | `repository/ent.go`、`repository/migrations_runner.go` | 区分自动迁移与独立脚本；保持上游迁移集独立 |
| 多厂商视频适配 | `backend/internal/videoadapter/`、`service/grok_video_adapter.go` 及测试、`extensions/video/` | `service/grok_media.go` 的适配器调用及相关字段 | 沿用原 Grok 路由、鉴权、调度与账单；厂商协议转换留在 adapter |
| 火山 Seedance 计价 | `service/seedance_pricing.go` | Seedance/Grok handler、service、返回结果结构、`openai_gateway_usage.go` 及对应测试 | 创建时固定单价并随任务保存；显式渠道价格优先；状态查询使用原始快照 |
| 豆包图片兼容 | 无独立新增文件 | `service/openai_images.go` 及测试 | 仅新增 Seedream/Seededit 模型识别，原图片转发体系属于上游 |
| 调度能力快照补充 | 无独立新增文件 | `repository/scheduler_cache.go` 及测试 | 保留 `base_url`、`openai_capabilities`，其余调度体系属于上游 |
| 部署与规则文档 | Coolify Compose、扩展说明、`AGENTS.md` | `Dockerfile`、`deploy/.env.example`、`DEV_GUIDE.md`、`docs/seedance-api.md`、`VERSION` | 配置、文档、版本标识与业务实现分别管理 |

## 数据库边界

- 上游 SQL 位于 `backend/migrations/`，本次基线对比无净差异。
- `backend/local-migrations/local_000_kox_billing.sql` 创建 `kox_service_accounts`、`kox_api_keys`、`kox_usage_logs`、`kox_billing_outbox`；`local_001_kox_gateway_key_mapping.sql` 补充真实网关 Key 映射。由服务在上游迁移后执行，仍使用 `schema_migrations` 账本，以 `local_` 文件名前缀区分。
- `extensions/kox/apply-migrations.sh` 是另一条需显式执行的迁移路径，使用独立的 `kox_extension_schema_migrations` 账本；其 `001_gateway_api_keys.sql` 也补充网关 Key 映射。它不是全部 Kox 表的初始化脚本。
- 两条路径当前都涉及 `kox_api_keys.gateway_api_key_id`，但索引名称与 SQL 定义存在差异。后续数据库任务须核对部署实际执行路径及库中状态，不能仅凭 README 假设只有一个账本。本次未执行任何迁移或修改 SQL。

## 全部自研新增文件

以下为相对基线新增的 23 个文件。新增文件与原源码接入修改分开管理。

| 文件 | 职责 |
| --- | --- |
| `AGENTS.md` | 本 Fork 协作与改动边界规则；此快照相对基线新增，不代表整个历史首次出现 |
| `backend/cmd/backfill-kox-usage/main.go` | 历史 usage_logs 补录 Kox 用量与 outbox 工具 |
| `backend/internal/koxbilling/handler.go` | Kox Key 生命周期、用量、签名 webhook/outbox 服务及其处理器或测试 |
| `backend/internal/koxbilling/service.go` | Kox Key 生命周期、用量、签名 webhook/outbox 服务及其处理器或测试 |
| `backend/internal/koxbilling/service_test.go` | Kox Key 生命周期、用量、签名 webhook/outbox 服务及其处理器或测试 |
| `backend/internal/server/routes/kox_billing.go` | 新增 /internal/v1/kox 服务间路由 |
| `backend/internal/service/grok_video_adapter.go` | Grok 视频厂商适配转发桥接 |
| `backend/internal/service/grok_video_adapter_test.go` | 视频适配转发契约测试 |
| `backend/internal/service/seedance_pricing.go` | Seedance 模型别名、价格矩阵、有效窗口及输出 token 成本计算 |
| `backend/internal/videoadapter/adapter.go` | 独立视频协议适配器及测试：OpenAI Videos、火山 Ark、天翼云 MiniMax |
| `backend/internal/videoadapter/adapter_test.go` | 独立视频协议适配器及测试：OpenAI Videos、火山 Ark、天翼云 MiniMax |
| `backend/internal/videoadapter/ctyun_minimax.go` | 独立视频协议适配器及测试：OpenAI Videos、火山 Ark、天翼云 MiniMax |
| `backend/internal/videoadapter/volcengine_ark.go` | 独立视频协议适配器及测试：OpenAI Videos、火山 Ark、天翼云 MiniMax |
| `backend/local-migrations/README.md` | 随服务启动执行的本地迁移集：Kox 表及网关 Key 映射；使用 schema_migrations |
| `backend/local-migrations/local_000_kox_billing.sql` | 随服务启动执行的本地迁移集：Kox 表及网关 Key 映射；使用 schema_migrations |
| `backend/local-migrations/local_001_kox_gateway_key_mapping.sql` | 随服务启动执行的本地迁移集：Kox 表及网关 Key 映射；使用 schema_migrations |
| `backend/local-migrations/migrations.go` | 随服务启动执行的本地迁移集：Kox 表及网关 Key 映射；使用 schema_migrations |
| `deploy/docker-compose.coolify.yml` | 本 Fork 的 Coolify 部署配置 |
| `extensions/kox/README.md` | Kox 独立迁移脚本及映射迁移；使用 kox_extension_schema_migrations |
| `extensions/kox/apply-migrations.sh` | Kox 独立迁移脚本及映射迁移；使用 kox_extension_schema_migrations |
| `extensions/kox/migrations/001_gateway_api_keys.sql` | Kox 独立迁移脚本及映射迁移；使用 kox_extension_schema_migrations |
| `extensions/video/CUSTOM_VIDEO_ADAPTERS.md` | 视频适配器配置、接入、账单边界和上游合并约定 |
| `extensions/video/README.md` | 视频适配器配置、接入、账单边界和上游合并约定 |

## 全部原源码修改文件

以下 28 个文件均是混合维护文件，表中只描述本地净差异；上游主体不因此变成自研。

| 文件 | 本地定制内容 |
| --- | --- |
| `DEV_GUIDE.md` | 保留 Seedance 自研计价的升级约定；其他内容沿用上游 |
| `Dockerfile` | 额外复制 extensions/kox 到镜像 |
| `backend/cmd/server/VERSION` | 版本标识由 0.2.5 改为 0.2.7；版本元数据，不是自研业务能力 |
| `backend/cmd/server/wire_gen.go` | 生成装配代码：注入 Kox 服务、用量仓库依赖与路由依赖 |
| `backend/internal/handler/grok_media.go` | 创建视频待结算记录时保存输出 token 单价快照 |
| `backend/internal/handler/seedance.go` | 完成结算时恢复创建阶段单价快照 |
| `backend/internal/repository/ent.go` | 上游迁移完成后执行 local-migrations |
| `backend/internal/repository/migrations_runner.go` | ApplyMigrations 顺序执行上游及本地迁移集 |
| `backend/internal/repository/scheduler_cache.go` | 调度快照 credentials 白名单保留 base_url 和 openai_capabilities |
| `backend/internal/repository/scheduler_cache_unit_test.go` | 补充 Seedance 能力及 base_url 快照保留测试 |
| `backend/internal/repository/usage_billing_repo.go` | 接入 Kox，事务内写入本地用量和 Kox 用量及事件 |
| `backend/internal/repository/usage_log_repo_insert.go` | 新增 CreateInTx，复用调用方计费事务 |
| `backend/internal/server/http.go` | 提供 Kox 依赖并启动事件投递后台任务 |
| `backend/internal/server/router.go` | 将 Kox 依赖传入并注册内部路由 |
| `backend/internal/service/billing_service_test.go` | 增加输出 token 成本及 Seedance 价格快照测试 |
| `backend/internal/service/gateway_usage_billing.go` | 补充 Kox 所需计费命令元数据，避免重复写本地用量 |
| `backend/internal/service/grok_media.go` | 适配器接入点及视频请求属性、待结算价格快照字段 |
| `backend/internal/service/openai_gateway_service.go` | OpenAIForwardResult 增加 OutputTokenPricePerTokenOverride |
| `backend/internal/service/openai_gateway_usage.go` | 无显式渠道定价时按快照计价；避免重复写本地用量 |
| `backend/internal/service/openai_images.go` | 增加 doubao-seedream、doubao-seededit、seedream、seededit 图片模型识别 |
| `backend/internal/service/openai_images_test.go` | 豆包图片模型识别及校验测试 |
| `backend/internal/service/seedance.go` | 提取分辨率、参考视频、音频属性，在创建时确定单价 |
| `backend/internal/service/seedance_test.go` | 补充创建请求计价属性提取测试 |
| `backend/internal/service/usage_billing.go` | UsageBillingCommand 增加 Kox 元数据和 LocalUsageLog |
| `backend/internal/web/embed_on.go` | /internal/ 路径绕过嵌入式前端 |
| `backend/internal/web/embed_test.go` | 内部账单路径绕过前端的测试 |
| `deploy/.env.example` | 新增 Kox 内部凭据、网关用户与分组、webhook 配置说明 |
| `docs/seedance-api.md` | 补充创建时计价快照、覆盖价格优先级和价格依据 |

## 可追溯证据与待确认项

主要本地功能提交：`2c3010d51`（Kox 账单）、`cce91147a`（线上账单回调与历史补录）、`b8c538236`（本地迁移与独立 Key）、`5b90f7bb6`（计费事务）、`6c11814e9`（视频适配器）、`f282f5d1b`（豆包接入）、`d4f39eb62`（天翼云 MiniMax）、`55f844654`（资源下载策略）、`ed7d6138c`（视频边界文档）、`93ffb265b`（豆包生图）、`8031c4687`（调度快照）、`438999e26`（火山收费）、`f09b94ec1`（协作边界规则）。历史提交描述用于定位，当前差异以固定基线为准。

已有细化约定见 [视频适配边界](../extensions/video/CUSTOM_VIDEO_ADAPTERS.md)、[Kox 独立迁移说明](../extensions/kox/README.md)、[本地迁移说明](../backend/local-migrations/README.md) 和 [开发指南](../DEV_GUIDE.md)。CodeGraph 的本项目 `kox` 查询未命中，本次来源结论以 Git 对象、历史及实际文件为准。

待确认范围：部署环境实际启用的供应商与配置、已执行的迁移路径、数据库实际结构、未跟踪或忽略的本机文件，以及第三方借鉴代码的原创版权归属。对 Git 快照中的 51 个净差异文件已完成维护归属分类；这些运行时及版权问题不据此推定。

## 2026-11-19 Composite 媒体兼容补丁

本补丁基于已合入 PR #2 的代码；下方历史逐文件清单仍保持其原快照含义，不将当前补丁混入历史统计。

- **原项目源码修改**：`service/composite_platform.go` 为新增的标准语音/数字人模型补平台识别，保留显式路由与账号归属优先级；`server/routes/gateway.go` 将 Composite 视频查询接到按所有权绑定分发的处理器；`GroupsView.vue`、`groupsImagePricing.ts` 和中英文文案开放组合分组现有语音/视频价格字段。相关测试随接入点调整。必要性是复用上游“一 Key 一组合分组”机制，后续上游合并需保留这些小范围接入段。
- **自研部分修改**：本次新增 `handler/composite_media.go`、`service/composite_media.go` 及对应测试；调整此前新增的 `handler/heygen_video.go`、`mediakit_asr.go`、`tokenhub_speech.go` 的分组准入，使其支持正确解析到目标平台的 Composite 请求。文档见 `COMPOSITE_MEDIA_GUIDE_CN.md`。
- **不涉及**：Kox 核心及用量事务桥接、数据库表和迁移、扣款与回调实现、正式 Docker 配置、上游账号 Key、云端运行配置。只解决请求分发和价格配置入口，不重新实现计费。

## 2026-11-20 收敛媒体定价前端改动

- **原项目源码修改**：`frontend/src/views/admin/GroupsView.vue`、`groupsImagePricing.ts`、对应 `__tests__/groupsImagePricing.spec.ts` 及 `i18n/locales/{zh,en}/admin/overview.ts` 全部恢复到 Kiiik 的 `3e91eedff` 版本。取消本地新增的 OpenAI/Composite 语音通用面板和 HeyGen/Composite 视频通用面板，直接使用该版本已有的「分组逐模型定价」。上述五个文件相对该基准无净差异，减少后续合并接入点；账号创建/编辑能力选项和 HeyGen 平台入口保留。
- **自研部分修改**：`handler/heygen_video.go` 和 `service/heygen_video.go` 的创建价格准入识别现有 Group/Channel 视频模型价卡，防止已配置模型价格却被旧通用字段拦截；旧通用视频价格继续兼容，模型 Token 价卡不用于无 token 的视频计量。增加 `service/media_model_pricing_test.go`，并调整 `handler/composite_media_test.go` 验证逐模型定价、实际单位、倍率、完整时长、所有权及轮询去重。更新四份媒体接入指南，新增 `MEDIA_MODEL_PRICING_CN.md` 操作教程。
- **不涉及**：原项目后端计费计算、供应商协议、账号平台归类、数据库迁移、Kox 核心和扣费事务、现有账号/分组价格、历史任务费用；不把时长或字符换算成虚拟 token。下方 TSV 继续保留原快照含义，不用本次文件覆盖历史归属清单。

## 后续使用与刷新

2026-11-20 经用户明确授权修正独立克隆模型计费：原项目源码修改 **无**。自研仅修改 `service/tokenhub_speech.go`、`handler/tokenhub_speech.go`：`minimax-voice-clone` 成功返回 voice_id 按一次操作进入已有模型按次定价，要求显式克隆价卡；标准余额模式先检查完整单次费用，沿用现有在途预留和结算。TTS 字符计费不附加克隆激活费，不按上游 use_count 拆出额外账单。新增自研 `service/tokenhub_clone_pricing_test.go`，调整 `handler/composite_media_test.go`，同步两份根目录语音说明。不改前端、通用计费、Kox、数据库结构、模型价卡或用户余额，不追扣历史克隆；属于用户选择的本地成功创建收费规则，与供应商的延后成本计量分开。

2026-11-20 真实语音联调补充：仅修改自研 `backend/internal/handler/mediakit_asr.go` 的任务计费 ID，复用已有 `StableGrokAudioBillingRequestID`，防止 HTTP 中间件的每次请求 ID 覆盖任务 ID 导致重复轮询扣费。`handler/composite_media_test.go` 补充每次请求不同 ClientRequestID 的回归场景。原项目源码修改：无；不修改通用去重、扣费事务、前端、数据库结构。此前无请求 ID 中间件的测试未覆盖该问题；此条记录真实测试发现及修复边界。

每次修改前引用本清单并按 `AGENTS.md` 分别声明原源码修改、自研修改和不涉及范围；涉及混合文件时写明具体函数或字段。功能完成后更新对应边界条目和文件清单，未涉及类别明确写“无”。

可用以下只读命令复核本次边界；同步上游或新增功能后应重新确定已合入基线，不能把本快照当作永久归属表：

```sh
git rev-parse HEAD
git rev-parse 'v0.2.7^{commit}'
git merge-base HEAD upstream/main
git diff --name-status aea725f2ea644d5592d0bbb1d63b607efa7e200a f09b94ec106f4b2ad2c8605917c99d27139a0375
git diff aea725f2ea644d5592d0bbb1d63b607efa7e200a -- path/to/file
```

本次新增的 `docs/CHANGE_BOUNDARIES.md` 与 `docs/CHANGE_BOUNDARIES_FILES.tsv` 属于本地维护文档，未计入上述整理前快照的 4013 个文件；本次只新增两份清单文档，业务代码无修改。


## 2026-11-24 火山多模态向量专用接入

本次基于 `213b2ea0f` 新增 `ark_multimodal` 专用平台。范围限定原生
`/embeddings/multimodal`；不改 OpenAI embeddings handler/forwarder，不扩展火山聊天、
生图、视频或异步任务。使用 Git `origin/main` 与本分支历史确认：平台注册、路由、
端点归一化、Composite 识别、前端账号表单及对应原有测试均属于原源码接入修改。

独立新增的 handler/service 从上游 embeddings 生命周期与转发骨架派生，文件头保留来源；
其中火山平台与协议校验、官方基址、原生响应及用量校验属于本地扩展，不能把复用骨架
认定为完全独立原创。专用路由限制、新增测试及说明为自研新增维护范围。

逐文件边界见 [ARK_MULTIMODAL_CHANGE_BOUNDARIES.md](../ARK_MULTIMODAL_CHANGE_BOUNDARIES.md)，
配置和验证见 [火山多模态向量说明](../ARK_MULTIMODAL_EMBEDDINGS_CN.md)。上文旧快照和旧 TSV
统计保持其历史含义；本补充不重写旧统计。数据库迁移、通用计费、Kox、Docker 和已有账号数据
均未修改。真实 Key 仅用于进程内联调，未写入仓库。


2026-11-25 合并维护收敛：原项目源码修改从 16 个已有文件减到 11 个（含测试及前端清单）。
后端已有生产文件仅保留平台注册 1 行、Composite 模型识别 2 行、公共路由扩展挂载 5 行。
原 domain/service 常量文件、公共端点文件及两份公共路由测试已恢复，逻辑迁入新增扩展文件。
账号、原生 OpenAI 转发、公共扣费事务、数据库和部署配置均不在此次收敛修改范围。
详见根目录 `ARK_MULTIMODAL_CHANGE_BOUNDARIES.md`；仍存在公共入口平台判断及上游 helper 依赖，
后续升级需要回归验证，不能承诺零合并冲突。
