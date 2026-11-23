/**
 * Agent Skills metadata — single source of truth for /dashboard/skills.
 * Each skill = 1 raw URL the user copies and pastes to any AI agent.
 * Ported from KineRouter's src/shared/constants/skills.js; the SKILL.md files live
 * in this repo under skills/ and are served by the KineRouter GitHub repo.
 */

const REPO = 'keyzakyy-dev/kinerouter'
const BRANCH = 'main'
const SKILL_PATH = 'skills'

export const SKILLS_REPO_URL = `https://github.com/${REPO}`
export const SKILLS_RAW_BASE = `https://raw.githubusercontent.com/${REPO}/refs/heads/${BRANCH}/${SKILL_PATH}`
export const SKILLS_BLOB_BASE = `https://github.com/${REPO}/blob/${BRANCH}/${SKILL_PATH}`

export type Skill = {
  id: string
  name: string
  description: string
  endpoint: string | null
  icon: string
  isEntry?: boolean
}

export const SKILLS: Skill[] = [
  {
    id: 'kinerouter',
    name: 'KineRouter (Entry)',
    description:
      'Setup + index of all capabilities. Start here — covers base URL, auth, model discovery, and links to every capability skill.',
    endpoint: null,
    icon: 'hub',
    isEntry: true,
  },
  {
    id: 'kinerouter-chat',
    name: 'Chat',
    description: 'Chat / code-gen via OpenAI or Anthropic format with streaming.',
    endpoint: '/v1/chat/completions',
    icon: 'chat',
  },
  {
    id: 'kinerouter-image',
    name: 'Image Generation',
    description: 'Text-to-image via DALL-E, Imagen, FLUX, MiniMax, SDWebUI…',
    endpoint: '/v1/images/generations',
    icon: 'image',
  },
  {
    id: 'kinerouter-video',
    name: 'Video Generation',
    description: 'Text-to-video and image-to-video via xAI, OpenRouter and Vertex Veo.',
    endpoint: '/v1/videos/generations',
    icon: 'movie',
  },
  {
    id: 'kinerouter-tts',
    name: 'Text-to-Speech',
    description: 'OpenAI / ElevenLabs / Edge / Google / Deepgram voices.',
    endpoint: '/v1/audio/speech',
    icon: 'record_voice_over',
  },
  {
    id: 'kinerouter-stt',
    name: 'Speech-to-Text',
    description: 'Transcribe audio via OpenAI Whisper, Groq, Gemini, Deepgram, AssemblyAI…',
    endpoint: '/v1/audio/transcriptions',
    icon: 'mic',
  },
  {
    id: 'kinerouter-embeddings',
    name: 'Embeddings',
    description: 'Vectors for RAG / semantic search via OpenAI, Gemini, Mistral…',
    endpoint: '/v1/embeddings',
    icon: 'scatter_plot',
  },
  {
    id: 'kinerouter-web-search',
    name: 'Web Search',
    description:
      'Web and X search via Tavily / Exa / Brave / Serper / SearXNG / Google PSE / You.com / Xquik.',
    endpoint: '/v1/search',
    icon: 'search',
  },
  {
    id: 'kinerouter-web-fetch',
    name: 'Web Fetch',
    description: 'URL → markdown / text / HTML via Firecrawl, Jina, Tavily, Exa.',
    endpoint: '/v1/web/fetch',
    icon: 'language',
  },
]

export function getSkillRawUrl(id: string) {
  return `${SKILLS_RAW_BASE}/${id}/SKILL.md`
}

export function getSkillBlobUrl(id: string) {
  return `${SKILLS_BLOB_BASE}/${id}/SKILL.md`
}
