# 火山多模态向量本次改动边界

基线：`213b2ea0f`。本清单只记录此次改动，不包含用户已有工作区修改。

| path | classification | scope | reference_commit |
| --- | --- | --- | --- |
| backend/internal/domain/platforms.go | 原项目源码修改 | 登记专用平台以复用调度和额度清单 | 213b2ea0f |
| backend/internal/domain/platforms_test.go | 原项目源码修改 | 平台清单预期加入新平台 | 213b2ea0f |
| backend/internal/service/composite_platform.go | 原项目源码修改 | 预置模型识别到专用平台 | 213b2ea0f |
| backend/internal/service/platform_list_test.go | 原项目源码修改 | 平台派生列表预期加入新平台 | 213b2ea0f |
| backend/internal/server/routes/gateway.go | 原项目源码修改 | 仅追加 5 行扩展挂载；具体路由、models 分发及平台限制留在新增文件 | 213b2ea0f |
| frontend/src/components/account/CreateAccountModal.vue | 原项目源码修改 | 专用账号选项、API Key 类型、模型及基址预设 | 213b2ea0f |
| frontend/src/components/account/EditAccountModal.vue | 原项目源码修改 | 专用账号默认基址 | 213b2ea0f |
| frontend/src/components/account/__tests__/CreateAccountModal.spec.ts | 原项目源码修改 | 验证账号平台/凭据/模型提交和禁用自动探测 | 213b2ea0f |
| frontend/src/composables/useModelWhitelist.ts | 原项目源码修改 | 专用模型预置 | 213b2ea0f |
| frontend/src/constants/platformCatalog.builtin.json | 原项目源码修改 | 同步后端平台清单 | 213b2ea0f |
| frontend/src/constants/__tests__/platforms.spec.ts | 原项目源码修改 | 平台选项与显示名预期 | 213b2ea0f |
| backend/internal/handler/ark_multimodal_embeddings.go | 新增扩展_含原源码派生 | 上游 handler 生命周期骨架派生；新增专用准入、调度与原生协议入口 | 213b2ea0f |
| backend/internal/service/ark_multimodal_embeddings.go | 新增扩展_含原源码派生 | 上游 forwarder 骨架派生；新增火山凭据/基址/请求/响应/用量校验 | 213b2ea0f |
| backend/internal/handler/ark_multimodal_embeddings_test.go | 自研新增 | handler 调度、并发释放、专用/Composite 分组及图文计费 | 213b2ea0f |
| backend/internal/service/ark_multimodal_embeddings_test.go | 自研新增 | 原生协议、错误、平台隔离、事务计费与显式 opt-in 实测 | 213b2ea0f |
| backend/internal/server/routes/ark_multimodal.go | 自研新增 | 只允许专用平台调用多模态向量及模型目录 | 213b2ea0f |
| backend/internal/server/routes/ark_multimodal_test.go | 自研新增 | 路由注册、原生平台隔离和端点身份 | 213b2ea0f |
| ARK_MULTIMODAL_EMBEDDINGS_CN.md | 自研新增 | 配置、调用、计费单位、实测和维护边界 | 213b2ea0f |
| ARK_MULTIMODAL_CHANGE_BOUNDARIES.md | 自研新增 | 本次逐文件维护边界 | 213b2ea0f |
| docs/CHANGE_BOUNDARIES.md | 自研文档修改 | 追加本次边界；旧快照保持不变 | 213b2ea0f |
| backend/internal/domain/ark_multimodal.go | 自研新增 | 独立声明平台常量，避免更改上游 constants.go | 213b2ea0f |

## 2026-11-25 收敛改动

原项目源码修改由 16 个已有文件收敛到 11 个，其中 4 个为测试、1 个为平台清单 JSON。
11 个文件共新增 82 行、删除 13 行；删除项为平台测试期望以及账号表单的局部替换。
统计不包括自研新增文件、自研边界说明或用户原有 `.gitignore` / `.dockerignore` 修改。

- 后端已有生产文件只保留三个接入点：`domain/platforms.go` 新增 1 行平台登记，
  `service/composite_platform.go` 新增 2 行模型识别，`server/routes/gateway.go` 新增 5 行扩展挂载。
- 两个原常量文件、公共端点文件和两个公共路由测试已恢复为本次开发前版本，避免格式对齐和
  中间件链替换造成合并冲突。平台常量、端点上下文、原生路由注册位于新增文件。
- 账号创建/编辑表单与模型预置仍有必要接入；本次不再扩大为通用前端扩展框架。
- 公共路由仍会经过新增的平台判断；已有平台直接继续原处理。不能将此描述为完全零主链路触点。
- 只新增文件而零注册无法让现有账号/分组界面及路由识别新平台。上游升级仍需检查上述 3 个后端
  接入点及前端表单；不承诺零冲突。派生 handler/service 依赖现有共享 helper，也需做接口兼容回归。


2026-11-25 Docker 验证：嵌入式前端只会绕过既有 `/v1/` 和 `/api/` 等 API 前缀，
因此专用接口最终只保留 `/v1/embeddings/multimodal` 与 `/api/v3/embeddings/multimodal`。
删除此前额外提供的 `/v3` / 无前缀别名；只调整自研路由、测试和说明，
不修改原项目 `web/embed_on.go` 或其前端绕过规则，不增加原源码接入文件。
