# KineRouter — Agent Skills

Drop-in skills for any AI agent (Claude, Cursor, ChatGPT, custom SDK). Just **copy a link** below and paste it to your AI — it will fetch the skill and use KineRouter for you.

> Tip: start with the **kinerouter** entry skill — it covers setup and links to all capability skills.

## Skills

| Capability | Copy link below and paste to your AI |
|---|---|
| **Entry / Setup** (start here) | https://raw.githubusercontent.com/keyzakyy-dev/kinerouter/refs/heads/main/skills/kinerouter/SKILL.md |
| Chat / code-gen | https://raw.githubusercontent.com/keyzakyy-dev/kinerouter/refs/heads/main/skills/kinerouter-chat/SKILL.md |
| Image generation | https://raw.githubusercontent.com/keyzakyy-dev/kinerouter/refs/heads/main/skills/kinerouter-image/SKILL.md |
| Video generation (xAI Grok Imagine) | https://raw.githubusercontent.com/keyzakyy-dev/kinerouter/refs/heads/main/skills/kinerouter-video/SKILL.md |
| Text-to-speech | https://raw.githubusercontent.com/keyzakyy-dev/kinerouter/refs/heads/main/skills/kinerouter-tts/SKILL.md |
| Speech-to-text | https://raw.githubusercontent.com/keyzakyy-dev/kinerouter/refs/heads/main/skills/kinerouter-stt/SKILL.md |
| Embeddings | https://raw.githubusercontent.com/keyzakyy-dev/kinerouter/refs/heads/main/skills/kinerouter-embeddings/SKILL.md |
| Web search | https://raw.githubusercontent.com/keyzakyy-dev/kinerouter/refs/heads/main/skills/kinerouter-web-search/SKILL.md |
| Web fetch (URL → markdown) | https://raw.githubusercontent.com/keyzakyy-dev/kinerouter/refs/heads/main/skills/kinerouter-web-fetch/SKILL.md |

## How to use

Paste to your AI (Claude, Cursor, ChatGPT, …):

```
Read this skill and use it: https://raw.githubusercontent.com/keyzakyy-dev/kinerouter/refs/heads/main/skills/kinerouter/SKILL.md
```

Then ask normally — *"generate an image of a cat"*, *"transcribe this URL"*, etc.

## Configure your shell once

```bash
export KINEROUTER_URL="http://localhost:20128"   # local default, or your VPS / tunnel URL
export KINEROUTER_KEY="sk-..."                   # from Dashboard → Keys (only if requireApiKey=true)
```

Verify: `curl $KINEROUTER_URL/api/health` → `{"ok":true}`.

## Links

- Source: https://github.com/keyzakyy-dev/kinerouter
- Install: https://raw.githubusercontent.com/keyzakyy-dev/kinerouter/main/kine/install.sh
