# Multi-provider video adapters

For the local implementation boundary, billing invariants, route interception
point, and upstream-merge checklist, see
[CUSTOM_VIDEO_ADAPTERS.md](CUSTOM_VIDEO_ADAPTERS.md).

Sub2API exposes one Grok/xAI-compatible asynchronous video interface while an
account-level adapter translates selected upstream protocols. Scheduling,
account failover, request ownership, protected content proxying, and deferred
video billing remain on the existing Grok path.

## Configuration

Create an API-key account with `platform` set to `grok`. Keep using the normal
Grok account fields:

```json
{
  "platform": "grok",
  "type": "apikey",
  "credentials": {
    "api_key": "vendor-api-key",
    "base_url": "https://vendor.example/v1",
    "video_adapter": "openai_videos",
    "model_mapping": {
      "grok-imagine-video": "vendor-video-model",
      "grok-imagine-video-1.5": "vendor-video-pro-model"
    }
  }
}
```

`video_adapter` is optional and non-secret. Missing values and `native` retain
the original xAI behavior. It may also use the object form
`{"type":"openai_videos"}` so future adapter-specific options can be added
without changing the account schema.

The initial adapters are:

| Adapter | Upstream create | Upstream status | Upstream content |
| --- | --- | --- | --- |
| `native` | `POST /videos/generations` | `GET /videos/{id}` | xAI signed URL or `GET /videos/{id}/content` |
| `openai_videos` | `POST /videos` | `GET /videos/{id}` | `GET /videos/{id}/content` |
| `volcengine_ark` | `POST /contents/generations/tasks` | `GET /contents/generations/tasks/{id}` | signed `content.video_url` |
| `ctyun_minimax` | `POST /video_generation` | `GET /query/video_generation/{id}` | signed `task.content.url` |

For `openai_videos`, Sub2API converts `duration` to `seconds`, derives the
protocol size from `resolution` and `aspect_ratio`, converts the first Grok
image reference to `input_reference.image_url`, and maps lifecycle states to
Grok's `pending`, `done`, and `failed` values. Completed jobs are returned as:

```json
{
  "request_id": "video_123",
  "status": "done",
  "model": "vendor-video-model",
  "video": {
    "url": "/v1/videos/video_123/content",
    "duration": 8
  }
}
```

Clients continue using Sub2API's existing routes:

- `POST /v1/videos/generations`
- `POST /v1/videos/edits`
- `POST /v1/videos/extensions`
- `GET /v1/videos/{request_id}`
- `GET /v1/videos/{request_id}/content`

### Volcengine Ark / Doubao Seedance

Use the Ark API root and map the public Grok model name to the Ark model ID:

```json
{
  "platform": "grok",
  "type": "apikey",
  "credentials": {
    "api_key": "ark-api-key",
    "base_url": "https://ark.cn-beijing.volces.com/api/v3",
    "video_adapter": "volcengine_ark",
    "model_mapping": {
      "grok-imagine-video": "doubao-seedance-1-5-pro-251215"
    }
  }
}
```

The adapter converts Grok's `prompt`, `aspect_ratio`, `resolution`, and
`duration` fields into Ark's `content` request and prompt options. Ark task
states and `content.video_url` are normalized to the Grok status/content
contract; signed media URLs are fetched without the account API key.

`doubao-seedance-1-0-lite-t2v-250428` is retained as a compatibility target for
third-party relays. Its adapter contract pins/validates the historical model
limits (5 or 10 seconds, 480p or 720p). Volcengine's LAS product discontinued
the corresponding operator on 2026-05-11 and recommends
`doubao-seedance-1-5-pro-251215`; confirm availability of the old model for the
specific Ark account or relay before enabling it in production.

### Tianyi Cloud / MiniMax-H3

Tianyi Cloud's MiniMax video API is not OpenAI-compatible. Use its dedicated,
vendor-prefixed adapter; retain `/v1` in the configured base URL because the
adapter adds only the operation path.

```json
{
  "platform": "grok",
  "type": "apikey",
  "credentials": {
    "api_key": "ctyun-app-key",
    "base_url": "https://ai.ctaigw.cn/v1",
    "video_adapter": "ctyun_minimax",
    "model_mapping": {
      "minimax-h3": "MiniMax-H3"
    }
  }
}
```

For text-to-video, the adapter translates the Grok request into Tianyi's
`content` array and requires a 4--15 second duration. It uses `16:9` when the
public request does not contain an aspect ratio, and supports `480P`, `768P`,
and `2K`. It converts `queued`/`running`/`succeeded`/`failed` task states to
the Grok lifecycle and downloads the completed signed `task.content.url`
without forwarding the account key. Adapter content downloads accept any HTTPS
signed resource hostname, so provider CDN and tenant-specific object-storage
domains do not require new code or configuration.

## Adding another provider

Add one uniquely vendor-prefixed adapter implementation under
`backend/internal/videoadapter` and register it in `Resolve`. Do not add vendor
branches to handlers, schedulers, or billing. Tests should cover the adapter
interface plus one forwarding contract test through
`OpenAIGatewayService.ForwardGrokMedia`.

The `openai_videos` name describes the wire protocol, not a requirement to call
OpenAI directly. As of 2026-09-11, OpenAI marks its Sora Videos API deprecated
and scheduled for shutdown on 2026-09-24; the adapter remains useful for
third-party gateways that implement that contract.
