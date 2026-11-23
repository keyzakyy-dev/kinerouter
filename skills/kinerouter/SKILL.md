---
name: kinerouter
description: Entry point for KineRouter — local/remote AI gateway with OpenAI-compatible REST for chat, image, TTS, embeddings, web search, web fetch. Use when the user mentions KineRouter, KINEROUTER_URL, or wants AI without writing provider boilerplate. This skill covers setup + indexes capability skills; fetch the relevant capability SKILL.md from the URLs below when needed.
---

# KineRouter

Local/remote AI gateway exposing OpenAI-compatible REST. One key, many providers, auto-fallback.

## Setup

```bash
export KINEROUTER_URL="http://localhost:20128"      # or VPS / tunnel URL
export KINEROUTER_KEY="sk-..."                      # from Dashboard → Keys (only if requireApiKey=true)
```

All requests: `${KINEROUTER_URL}/v1/...` with header `Authorization: Bearer ${KINEROUTER_KEY}` (omit if auth disabled).

Verify: `curl $KINEROUTER_URL/api/health` → `{"ok":true}`

## Discover models

```bash
curl $KINEROUTER_URL/v1/models                  # chat/LLM (default)
curl $KINEROUTER_URL/v1/models/image            # image-gen
curl $KINEROUTER_URL/v1/models/tts              # text-to-speech
curl $KINEROUTER_URL/v1/models/embedding        # embeddings
curl $KINEROUTER_URL/v1/models/web              # web search + fetch (entries have `kind` field)
curl $KINEROUTER_URL/v1/models/stt              # speech-to-text
curl $KINEROUTER_URL/v1/models/image-to-text    # vision
```

Use `data[].id` as `model` field in requests. Combos appear with `owned_by:"combo"`.

Response shape:
```json
{ "object": "list", "data": [
  { "id": "openai/gpt-5", "object": "model", "owned_by": "openai", "created": 1735000000 },
  { "id": "tavily/search", "object": "model", "kind": "webSearch", "owned_by": "tavily", "created": 1735000000 }
]}
```

## Capability skills

When the user needs a specific capability, fetch that skill's `SKILL.md` from its raw URL:

| Capability | Raw URL |
|---|---|
| Chat / code-gen | https://raw.githubusercontent.com/keyzakyy-dev/kinerouter/refs/heads/main/skills/kinerouter-chat/SKILL.md |
| Image generation | https://raw.githubusercontent.com/keyzakyy-dev/kinerouter/refs/heads/main/skills/kinerouter-image/SKILL.md |
| Text-to-speech | https://raw.githubusercontent.com/keyzakyy-dev/kinerouter/refs/heads/main/skills/kinerouter-tts/SKILL.md |
| Speech-to-text | https://raw.githubusercontent.com/keyzakyy-dev/kinerouter/refs/heads/main/skills/kinerouter-stt/SKILL.md |
| Embeddings | https://raw.githubusercontent.com/keyzakyy-dev/kinerouter/refs/heads/main/skills/kinerouter-embeddings/SKILL.md |
| Web search | https://raw.githubusercontent.com/keyzakyy-dev/kinerouter/refs/heads/main/skills/kinerouter-web-search/SKILL.md |
| Web fetch (URL → markdown) | https://raw.githubusercontent.com/keyzakyy-dev/kinerouter/refs/heads/main/skills/kinerouter-web-fetch/SKILL.md |

## Errors

- 401 → set/refresh `KINEROUTER_KEY` (Dashboard → Keys)
- 400 `Invalid model format` → check `model` exists in `/v1/models/<kind>`
- 503 `All accounts unavailable` → wait `retry-after` or add another provider account
