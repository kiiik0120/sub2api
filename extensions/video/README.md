# Multi-provider video adapters

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

## Adding another provider

Add one adapter implementation under `backend/internal/videoadapter` and
register it in `Resolve`. Do not add vendor branches to handlers, schedulers, or
billing. Tests should cover the adapter interface plus one forwarding contract
test through `OpenAIGatewayService.ForwardGrokMedia`.

The `openai_videos` name describes the wire protocol, not a requirement to call
OpenAI directly. As of 2026-09-11, OpenAI marks its Sora Videos API deprecated
and scheduled for shutdown on 2026-09-24; the adapter remains useful for
third-party gateways that implement that contract.

