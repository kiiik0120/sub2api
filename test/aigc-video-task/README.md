# TokenHub Video through Ark

TokenHub video models share the OpenAI API-key account used for TokenHub images. Set the account Base URL to `https://tokenhub.tencentmaas.com`, store the TokenHub API key in the API Key field, enable `TokenHub Video (Ark)`, and map each public model name to the exact upstream model ID.

WAND model IDs from the model list can be used directly as public names or account mappings. The adapter translates them to TokenHub's accepted spelling (with a hyphen after `video`):

- `wand-vega-video1.0-lite`
- `wand-vega-video1.0-pro`
- `wand-vega-video1.5-pro`

The upstream IDs used for WAND requests are `wand-vega-video-1.0-lite`, `wand-vega-video-1.0-pro`, and `wand-vega-video-1.5-pro`.
- `minimax-video-v2.3`
- `minimax-video-v2.3-fast`
- `minimax-video-h3`
- `minimax-video-h3-max`

Submit an Ark-compatible task at `/api/v3/contents/generations/tasks`:

```json
{
  "model": "minimax-video-h3",
  "content": [
    {"type": "text", "text": "A quiet garden vlog at sunrise, flowers moving in a light breeze"},
    {"type": "image_url", "role": "reference_image", "image_url": {"url": "https://example.com/flower.jpg"}},
    {"type": "video_url", "role": "reference_video", "video_url": {"url": "https://example.com/garden.mp4"}}
  ],
  "resolution": "768P",
  "ratio": "16:9",
  "duration": 6,
  "extras": {
    "minimax": {"extra": {"prompt_expansion_mode": "balanced"}}
  }
}
```

Poll the returned `id` at `/api/v3/contents/generations/tasks/{id}`. The adapter normalizes TokenHub task states and returns the result at `content.video_url`. Successful task usage is billed from TokenHub's `tokenhub_usage.total_tokens`; configure token pricing for each mapped model in the group's pricing settings. TokenHub video pricing may differ by model, so use the model's applicable TokenHub rate.

MiniMax v2.3 and v2.3-fast use their flat upstream endpoint and only accept text plus first/last frame images. MiniMax H3/H3-Max and WAND keep their multimodal `content` arrays. WAND-specific options can be passed in `extras.tokenhub.options`; MiniMax options can be passed in `extras.minimax`.
