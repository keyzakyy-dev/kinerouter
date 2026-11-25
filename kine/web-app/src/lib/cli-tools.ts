/**
 * CLI tool catalogue, mirrored from src/shared/constants/cliTools.js.
 *
 * `form` says which body the server's `<tool>-settings` POST expects
 * (internal/gateway/clitools.go); `guide` tools have no settings file at all
 * and render instructions instead.
 */

export type CliForm = 'env' | 'single' | 'multi' | 'baseUrl' | 'guide'

export type CliNote = { type: 'info' | 'warning' | 'cloudCheck' | 'error'; text: string }

export type CliGuideStep = {
  step: number
  title: string
  desc?: string
  /** Copyable value; supports {{baseUrl}} / {{apiKey}} / {{model}}. */
  value?: string
  copyable?: boolean
  type?: 'apiKeySelector' | 'modelSelector'
  docsUrl?: string
}

export type CliTool = {
  id: string
  name: string
  description: string
  icon?: string
  image?: string
  color?: string
  form: CliForm
  docsUrl?: string
  /** Server route id when it differs from the catalogue id (devin is GET-only). */
  routeId?: string
  /** Callouts rendered above the steps / form (reference cliTools.js `notes`). */
  notes?: CliNote[]
  guideSteps?: CliGuideStep[]
  codeBlock?: { language: string; code: string }
  /** Guide only shows once a tunnel or cloud endpoint exists. */
  requiresExternalUrl?: boolean
  installUrl?: string
}

export const CLI_TOOLS: CliTool[] = [
  { id: 'claude', name: 'Claude Code', description: 'Anthropic Claude Code CLI', image: 'claude', color: '#D97757', form: 'env' },
  { id: 'openclaw', name: 'Open Claw', description: 'Open Claw AI Assistant', image: 'openclaw', color: '#FF6B35', form: 'single' },
  { id: 'codex', name: 'OpenAI Codex CLI / App', description: 'OpenAI Codex CLI', image: 'codex', color: '#10A37F', form: 'single' },
  { id: 'copilot', name: 'GitHub Copilot', description: 'GitHub Copilot in VS Code through chatLanguageModels.json', image: 'copilot', color: '#1F6FEB', form: 'multi', docsUrl: 'https://code.visualstudio.com/docs/copilot/customization/language-models' },
  { id: 'opencode', name: 'OpenCode', description: 'OpenCode AI Terminal Assistant', image: 'opencode', color: '#E87040', form: 'multi' },
  { id: 'cowork', name: 'Claude Cowork', description: 'Claude Desktop Cowork (third-party inference)', image: 'claude', color: '#D97757', form: 'multi' },
  { id: 'hermes', name: 'Hermes Agent', description: 'Nous Research self-improving AI agent', image: 'hermes', color: '#8B5CF6', form: 'single' },
  { id: 'droid', name: 'Factory Droid', description: 'Factory Droid AI Assistant', image: 'droid', color: '#00D4FF', form: 'multi' },
  {
    id: 'cursor', name: 'Cursor', description: 'Cursor AI Code Editor', image: 'cursor', color: '#000000', form: 'guide',
    requiresExternalUrl: true,
    notes: [
      { type: 'warning', text: 'Requires Cursor Pro account to use this feature.' },
      { type: 'cloudCheck', text: 'Cursor routes requests through its own server, so local endpoint is not supported. Please enable Tunnel or Cloud Endpoint in Settings.' },
    ],
    guideSteps: [
      { step: 1, title: 'Open Settings', desc: 'Go to Settings → Models' },
      { step: 2, title: 'Enable OpenAI API', desc: 'Enable "OpenAI API key" option' },
      { step: 3, title: 'Base URL', value: '{{baseUrl}}', copyable: true },
      { step: 4, title: 'API Key', type: 'apiKeySelector' },
      { step: 5, title: 'Add Custom Model', desc: 'Click "View All Model" → "Add Custom Model"' },
      { step: 6, title: 'Select Model', type: 'modelSelector' },
    ],
  },
  { id: 'cline', name: 'Cline', description: 'Cline AI Coding Assistant', image: 'cline', color: '#5B9BD5', form: 'single' },
  { id: 'kilo', name: 'Kilo Code', description: 'Kilo Code AI Assistant', image: 'kilocode', color: '#FF6B6B', form: 'single' },
  {
    id: 'roo', name: 'Roo', description: 'Roo AI Assistant', image: 'roo', color: '#FF6B6B', form: 'guide',
    guideSteps: [
      { step: 1, title: 'Open Settings', desc: 'Go to Roo Settings panel' },
      { step: 2, title: 'Select Provider', desc: 'Choose API Provider → Ollama' },
      { step: 3, title: 'Base URL', value: '{{baseUrl}}', copyable: true },
      { step: 4, title: 'API Key', type: 'apiKeySelector' },
      { step: 5, title: 'Select Model', type: 'modelSelector' },
    ],
  },
  {
    id: 'continue', name: 'Continue', description: 'Continue AI Assistant', image: 'continue', color: '#7C3AED', form: 'guide',
    guideSteps: [
      { step: 1, title: 'Open Config', desc: 'Open Continue configuration file' },
      { step: 2, title: 'API Key', type: 'apiKeySelector' },
      { step: 3, title: 'Select Model', type: 'modelSelector' },
      { step: 4, title: 'Add Model Config', desc: 'Add the following configuration to your models array:' },
    ],
    codeBlock: {
      language: 'json',
      code: `{
  "apiBase": "{{baseUrl}}",
  "title": "{{model}}",
  "model": "{{model}}",
  "provider": "openai",
  "apiKey": "{{apiKey}}"
}`,
    },
  },
  {
    id: 'amp', name: 'Amp CLI', description: 'Sourcegraph Amp coding assistant CLI', image: 'amp', color: '#F97316', form: 'guide',
    notes: [
      { type: 'info', text: 'Use KineRouter model aliases to keep Amp shorthand mappings stable across provider updates.' },
      { type: 'warning', text: 'Suggested shorthand examples: g25p → gemini/gemini-2.5-pro, g25f → gemini/gemini-2.5-flash, cs45 → cc/claude-sonnet-4-5-20250929.' },
    ],
    guideSteps: [
      { step: 1, title: 'Install Amp', desc: 'Install the Amp CLI using the package manager supported by your environment.' },
      { step: 2, title: 'API Key', type: 'apiKeySelector' },
      { step: 3, title: 'Base URL', value: '{{baseUrl}}', copyable: true },
      { step: 4, title: 'Select Model', type: 'modelSelector' },
      { step: 5, title: 'Add Shorthands', desc: 'Map Amp shorthand names such as g25p or cs45 to KineRouter aliases in your local config.' },
    ],
    codeBlock: {
      language: 'bash',
      code: `export OPENAI_API_KEY="{{apiKey}}"
export OPENAI_BASE_URL="{{baseUrl}}"
amp --model "{{model}}"
# Example shorthand aliases you can map locally:
# g25p -> gemini/gemini-2.5-pro
# cs45 -> cc/claude-sonnet-4-5-20250929`,
    },
  },
  {
    id: 'qwen', name: 'Qwen Code', description: 'Alibaba Qwen Code CLI. Supports OpenAI, Anthropic & Gemini providers via KineRouter', image: 'qwen', color: '#10B981', form: 'guide',
    docsUrl: 'https://qwenlm.github.io/qwen-code-docs/en/users/configuration/model-providers/',
    notes: [
      { type: 'info', text: 'Qwen Code supports multiple provider types (openai, anthropic, gemini) via modelProviders in settings.json. KineRouter works as an OpenAI-compatible endpoint.' },
      { type: 'info', text: 'Any model available in KineRouter can be used, not just Qwen models. Select from Qwen, Claude, Gemini, GPT, and more.' },
      { type: 'warning', text: 'Config path: Linux/macOS ~/.qwen/settings.json • Windows %USERPROFILE%\\.qwen\\settings.json' },
      { type: 'error', text: 'Qwen OAuth free tier was discontinued on 2026-04-15. Use KineRouter with alicode/openrouter/anthropic/gemini providers instead.' },
    ],
    guideSteps: [
      { step: 1, title: 'Install Qwen Code', desc: 'npm install -g @qwen-code/qwen-code' },
      { step: 2, title: 'API Key', type: 'apiKeySelector' },
      { step: 3, title: 'Base URL', value: '{{baseUrl}}', copyable: true },
      { step: 4, title: 'Select Model', type: 'modelSelector' },
      { step: 5, title: 'Save Config', desc: 'Copy the JSON below to your ~/.qwen/settings.json file.' },
    ],
    codeBlock: {
      language: 'json',
      code: `{
  "security": {
    "auth": {
      "selectedType": "openai",
      "apiKey": "{{apiKey}}",
      "baseUrl": "{{baseUrl}}"
    }
  },
  "model": {
    "name": "{{model}}"
  }
}`,
    },
  },
  {
    id: 'deepseek-tui', name: 'DeepSeek TUI', description: 'DeepSeek Terminal Coding Agent (Rust TUI)', image: 'deepseek-tui', color: '#4D6BFE', form: 'single',
    docsUrl: 'https://github.com/DeepSeek-TUI/DeepSeek-TUI',
    notes: [
      { type: 'info', text: "DeepSeek TUI uses ~/.deepseek/config.toml for configuration. KineRouter will update the provider to 'openai' mode with your base_url, api_key, and model." },
      { type: 'warning', text: 'Config path: Linux/macOS ~/.deepseek/config.toml • Windows %USERPROFILE%\\.deepseek\\config.toml' },
    ],
  },
  {
    id: 'jcode', name: 'jcode', description: 'High-performance Rust-based coding agent harness', image: 'jcode', color: '#FF6B35', form: 'baseUrl',
    docsUrl: 'https://github.com/1jehuang/jcode',
    notes: [
      { type: 'info', text: 'jcode is a Rust-based coding agent with semantic memory, multi-agent swarms, and extreme performance (27.8 MB RAM, 14ms boot).' },
      { type: 'info', text: "Configure kinerouter as an OpenAI-compatible provider to route all jcode requests through kinerouter's optimization layer." },
      { type: 'warning', text: 'Requires jcode installed. Install via: curl -fsSL https://raw.githubusercontent.com/1jehuang/jcode/master/scripts/install.sh | bash' },
    ],
  },
  {
    id: 'grok-build', name: 'Grok Build', description: 'xAI Grok Build TUI coding agent', image: 'grok-cli', color: '#111111', form: 'single',
    docsUrl: 'https://x.ai/cli',
    notes: [
      { type: 'info', text: 'Grok Build uses ~/.grok/config.toml. KineRouter writes a [model.kinerouter] custom model and sets it as the default.' },
      { type: 'info', text: 'After Apply, run grok (or /model kinerouter) to use the routed model. Switch back anytime with /model grok-build.' },
      { type: 'warning', text: 'Config path: Linux/macOS ~/.grok/config.toml • Windows %USERPROFILE%\\.grok\\config.toml' },
    ],
  },
  {
    id: 'devin', name: 'Devin CLI', description: 'Cognition Devin CLI (ACP/stdio)', image: 'devin-cli', color: '#6E56CF', form: 'guide',
    installUrl: 'https://cli.devin.ai',
    notes: [
      { type: 'info', text: 'This is a local dependency, not a routed CLI. The Devin CLI provider spawns `devin acp --agent-type summarizer` and relays its output.' },
      { type: 'warning', text: 'Install the Devin CLI and run `devin auth login`; without it, the provider returns a spawn error on first request.' },
    ],
    guideSteps: [
      { step: 1, title: 'Install Devin CLI', desc: 'Install via the official installer at cli.devin.ai.', docsUrl: 'https://cli.devin.ai' },
      { step: 2, title: 'Authenticate', desc: 'Log in once so the binary stores its own credentials.' },
      { step: 3, title: 'Use the provider', desc: 'Pick any Devin CLI model under the Providers tab. No API key field needed.' },
    ],
    codeBlock: {
      language: 'bash',
      code: `# Install Devin CLI (see https://cli.devin.ai for options)
devin auth login

# Verify detection (optional)
devin --version`,
    },
  },
  {
    id: 'opendesign', name: 'OpenDesign', description: 'Agent-native design skills pack', image: 'opendesign', color: '#EC4899', form: 'guide',
    docsUrl: 'https://github.com/manalkaff/opendesign',
    notes: [
      { type: 'info', text: "OpenDesign ships as a plugin/skills pack installed into Claude Code, Cursor, OpenAI Codex, Gemini CLI, or OpenCode. It inherits the host agent's model config, so once your host points at KineRouter, /opendesign design sessions route through KineRouter automatically, no extra env vars needed." },
      { type: 'info', text: 'Invoke with /opendesign <brief>. Covers decks, wireframes, interactive prototypes, design-system extraction, and brand systems, with a verifier subagent that checks output against the brief.' },
    ],
    guideSteps: [
      { step: 1, title: 'Install the plugin', desc: 'Pick your host below and run the matching install command from the matrix.' },
      { step: 2, title: 'No config needed', desc: 'OpenDesign runs inside your host agent and uses its model config. If the host already routes through KineRouter, /opendesign traffic does too.' },
      { step: 3, title: 'Start designing', desc: 'Invoke OpenDesign from your agent:', value: '/opendesign make a pitch deck for a seed-stage AI company, 10 slides', copyable: true },
    ],
    codeBlock: {
      language: 'bash',
      code: `# Claude Code
/plugin marketplace add manalkaff/opendesign
/plugin install opendesign@opendesign

# Cursor
/add-plugin opendesign

# OpenAI Codex CLI
/plugins   # search "opendesign" -> Install Plugin

# OpenAI Codex App
# Plugins sidebar -> OpenDesign (Design section) -> +

# Gemini CLI
gemini extensions install https://github.com/manalkaff/opendesign

# OpenCode
# Fetch and follow .opencode/INSTALL.md from the repo`,
    },
  },
  {
    id: 'pi', name: 'Pi', description: 'Minimal, extensible agent harness (pi.dev)', image: 'pi', color: '#0EA5E9', form: 'multi',
    docsUrl: 'https://pi.dev',
    notes: [
      { type: 'info', text: 'Pi uses ~/.pi/agent/models.json. KineRouter is configured under providers.kinerouter as an OpenAI-compatible endpoint.' },
    ],
  },
  {
    id: 'omp', name: 'Oh My Pi', description: 'Oh My Pi terminal AI agent', image: 'omp', color: '#F59E0B', form: 'baseUrl',
    docsUrl: 'https://github.com/can1357/oh-my-pi',
    notes: [
      { type: 'info', text: 'Oh My Pi uses ~/.omp/agent/models.yml and agent.db. KineRouter is configured with proxy discovery so all models appear automatically under /model.' },
    ],
  },
  {
    id: 'crush', name: 'Crush', description: 'Charm Crush terminal AI coding agent', image: 'crush', color: '#A855F7', form: 'single',
    docsUrl: 'https://github.com/charmbracelet/crush',
    notes: [
      { type: 'info', text: 'Crush uses ~/.config/crush/crush.json. KineRouter registers as an openai-compat provider.' },
    ],
  },
  {
    id: 'forge', name: 'ForgeCode', description: 'Antinomy HQ ForgeCode agent harness', image: 'forge', color: '#22C55E', form: 'single',
    docsUrl: 'https://github.com/antinomyhq/forge',
    notes: [
      { type: 'info', text: 'ForgeCode uses ~/.forge/config.toml. KineRouter updates the [openai] section with your baseUrl, apiKey, and model.' },
    ],
  },
  {
    id: 'smelt', name: 'Smelt', description: 'Smelt terminal AI coding assistant', image: 'smelt', color: '#EF4444', form: 'single',
    docsUrl: 'https://github.com/leonardcser/smelt',
    notes: [
      { type: 'info', text: 'Smelt uses ~/.smelt/config.json for OpenAI-compatible endpoint configuration.' },
    ],
  },
  {
    id: 'codewhale', name: 'CodeWhale', description: 'CodeWhale terminal coding agent', image: 'codewhale', color: '#3B82F6', form: 'single',
    docsUrl: 'https://github.com/Hmbown/CodeWhale',
    notes: [
      { type: 'info', text: 'CodeWhale uses ~/.codewhale/config.toml. KineRouter configures the [openai] provider with your base_url, api_key, and model.' },
    ],
  },
]

export type MitmModel = { id: string; name: string; alias: string }
export type MitmTool = {
  id: string
  name: string
  description: string
  image: string
  hosts: string[]
  defaultModels: MitmModel[]
}

/**
 * MITM tools, mirrored from MITM_TOOLS + TOOL_HOSTS in the reference
 * (src/shared/constants/cliTools.js, mitmToolHosts.js). Hosts must match
 * internal/mitm/toolhosts.go; the alias PUT is rejected while DNS is off.
 */
export const MITM_TOOLS: MitmTool[] = [
  {
    id: 'antigravity',
    name: 'Antigravity',
    description: 'Google Antigravity IDE with MITM',
    image: 'antigravity',
    hosts: ['daily-cloudcode-pa.googleapis.com', 'cloudcode-pa.googleapis.com'],
    defaultModels: [
      { id: 'gemini-3.8-flash-high', name: 'Gemini 3.8 Flash (High)', alias: 'gemini-3.8-flash-high' },
      { id: 'gemini-3.8-flash-medium', name: 'Gemini 3.8 Flash (Medium) / Default', alias: 'gemini-3.8-flash-medium' },
      { id: 'gemini-3.8-flash-low', name: 'Gemini 3.8 Flash (Low)', alias: 'gemini-3.8-flash-low' },
      { id: 'gemini-3.7-flash-high', name: 'Gemini 3.7 Flash (High)', alias: 'gemini-3.7-flash-high' },
      { id: 'gemini-3.7-flash-medium', name: 'Gemini 3.7 Flash (Medium)', alias: 'gemini-3.7-flash-medium' },
      { id: 'gemini-3.7-flash-low', name: 'Gemini 3.7 Flash (Low)', alias: 'gemini-3.7-flash-low' },
      { id: 'gemini-3.6-flash-high', name: 'Gemini 3.6 Flash (High)', alias: 'gemini-3.6-flash-high' },
      { id: 'gemini-3.6-flash-medium', name: 'Gemini 3.6 Flash (Medium)', alias: 'gemini-3.6-flash-medium' },
      { id: 'gemini-3.6-flash-low', name: 'Gemini 3.6 Flash (Low)', alias: 'gemini-3.6-flash-low' },
      { id: 'gemini-3.1-pro-high', name: 'Gemini 3.1 Pro (High)', alias: 'gemini-3.1-pro-high' },
      { id: 'gemini-3.1-pro-low', name: 'Gemini 3.1 Pro (Low)', alias: 'gemini-3.1-pro-low' },
      { id: 'gemini-pro-agent', name: 'Gemini 3.1 Pro (High) / Legacy', alias: 'gemini-pro-agent' },
      { id: 'claude-sonnet-5-5', name: 'Claude Sonnet 5.5 (Thinking)', alias: 'claude-sonnet-5-5' },
      { id: 'claude-sonnet-5-5-low', name: 'Claude Sonnet 5.5 (Low)', alias: 'claude-sonnet-5-5-low' },
      { id: 'claude-sonnet-5-5-medium', name: 'Claude Sonnet 5.5 (Medium)', alias: 'claude-sonnet-5-5-medium' },
      { id: 'claude-sonnet-5-5-high', name: 'Claude Sonnet 5.5 (High)', alias: 'claude-sonnet-5-5-high' },
      { id: 'claude-opus-5-5', name: 'Claude Opus 5.5 (Thinking)', alias: 'claude-opus-5-5' },
      { id: 'claude-opus-5-5-low', name: 'Claude Opus 5.5 (Low)', alias: 'claude-opus-5-5-low' },
      { id: 'claude-opus-5-5-medium', name: 'Claude Opus 5.5 (Medium)', alias: 'claude-opus-5-5-medium' },
      { id: 'claude-opus-5-5-high', name: 'Claude Opus 5.5 (High)', alias: 'claude-opus-5-5-high' },
      { id: 'claude-sonnet-4-6', name: 'Claude Sonnet 4.6 (Thinking)', alias: 'claude-sonnet-4-6' },
      { id: 'claude-opus-4-6-thinking', name: 'Claude Opus 4.6 (Thinking)', alias: 'claude-opus-4-6-thinking' },
      { id: 'gpt-oss-120b-medium', name: 'GPT-OSS 120B (Medium)', alias: 'gpt-oss-120b-medium' },
    ],
  },
  {
    id: 'kiro',
    name: 'Kiro',
    description: 'Kiro IDE with MITM',
    image: 'kiro',
    hosts: ['runtime.us-east-1.kiro.dev', 'q.us-east-1.amazonaws.com', 'codewhisperer.us-east-1.amazonaws.com'],
    // "auto" and "simple-task" are what Kiro's agent mode actually sends.
    defaultModels: [
      { id: 'auto', name: 'Auto (Kiro Agent)', alias: 'auto' },
      { id: 'claude-sonnet-5', name: 'Claude Sonnet 5', alias: 'claude-sonnet-5' },
      { id: 'claude-sonnet-4.5', name: 'Claude Sonnet 4.5', alias: 'claude-sonnet-4.5' },
      { id: 'claude-sonnet-4', name: 'Claude Sonnet 4', alias: 'claude-sonnet-4' },
      { id: 'claude-haiku-4.5', name: 'Claude Haiku 4.5', alias: 'claude-haiku-4.5' },
      { id: 'deepseek-3.2', name: 'DeepSeek 3.2', alias: 'deepseek-3.2' },
      { id: 'minimax-m2.1', name: 'MiniMax M2.1', alias: 'minimax-m2.1' },
      { id: 'gpt-5.6-sol', name: 'GPT 5.6 Sol', alias: 'gpt-5.6-sol' },
      { id: 'gpt-5.6-terra', name: 'GPT 5.6 Terra', alias: 'gpt-5.6-terra' },
      { id: 'gpt-5.6-luna', name: 'GPT 5.6 Luna', alias: 'gpt-5.6-luna' },
      { id: 'simple-task', name: 'Qwen3 Coder Next', alias: 'simple-task' },
    ],
  },
]

export const cliTool = (id: string) => CLI_TOOLS.find((t) => t.id === id)

/** Claude's env block: one model per alias, token comes from the picked key. */
export const CLAUDE_MODEL_KEYS = [
  { alias: 'opus', tier: 'opus', env: 'ANTHROPIC_DEFAULT_OPUS_MODEL' },
  { alias: 'sonnet', tier: 'sonnet', env: 'ANTHROPIC_DEFAULT_SONNET_MODEL' },
  { alias: 'fable', tier: 'fable', env: 'ANTHROPIC_DEFAULT_FABLE_MODEL' },
  { alias: 'haiku', tier: 'haiku', env: 'ANTHROPIC_DEFAULT_HAIKU_MODEL' },
]
