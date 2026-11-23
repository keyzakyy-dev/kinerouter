import { useState } from 'react'
import { PageHeader } from '@/components/layout'
import { Icon } from '@/components/icon'
import {
  SKILLS,
  SKILLS_BLOB_BASE,
  SKILLS_REPO_URL,
  getSkillBlobUrl,
  getSkillRawUrl,
  type Skill,
} from '@/lib/skills'
import { cn } from '@/lib/utils'

function CopyButton({ value, label = 'Copy', title }: { value: string; label?: string; title?: string }) {
  const [copied, setCopied] = useState(false)

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(value)
    } catch {
      const el = document.createElement('textarea')
      el.value = value
      document.body.appendChild(el)
      el.select()
      document.execCommand('copy')
      el.remove()
    }
    setCopied(true)
    setTimeout(() => setCopied(false), 2000)
  }

  return (
    <button
      onClick={copy}
      title={title ?? value}
      className="inline-flex shrink-0 cursor-pointer items-center gap-1.5 rounded-md bg-surface-2 px-2.5 py-1 text-[11px] font-medium text-text-main transition-colors hover:bg-card"
    >
      <Icon name={copied ? 'check' : 'content_copy'} className="text-[13px]" />
      {copied ? 'Copied' : label}
    </button>
  )
}

function Chip({ children }: { children: React.ReactNode }) {
  return (
    <span className="inline-flex items-center rounded bg-surface-2 px-1.5 py-0.5 font-mono text-[10px] text-text-muted">
      {children}
    </span>
  )
}

function SkillRow({ skill, first }: { skill: Skill; first: boolean }) {
  const url = getSkillRawUrl(skill.id)

  return (
    <div className={cn('flex items-start gap-3 px-5 py-4', !first && 'border-t border-border', skill.isEntry && 'bg-surface-2/40')}>
      <Icon name={skill.icon} className="text-[18px] text-text-muted" />

      <div className="min-w-0 flex-1">
        <div className="flex flex-wrap items-center gap-2">
          <h3 className="text-sm font-semibold text-text-main">{skill.name}</h3>
          {skill.isEntry && <Chip>start here</Chip>}
          {skill.endpoint && <Chip>{skill.endpoint}</Chip>}
        </div>
        <p className="mt-0.5 text-xs leading-relaxed text-text-muted">{skill.description}</p>
        <a
          href={getSkillBlobUrl(skill.id)}
          target="_blank"
          rel="noreferrer"
          className="mt-1 inline-flex items-center gap-1 break-all font-mono text-[11px] text-text-muted hover:text-text-main"
        >
          {url}
          <Icon name="open_in_new" className="shrink-0 text-[12px]" />
        </a>
      </div>

      <CopyButton value={url} />
    </div>
  )
}

export default function SkillsPage() {
  const prompt = `Read this skill and use it: ${getSkillRawUrl('kinerouter')}`

  return (
    <div className="mx-auto max-w-4xl space-y-5">
      <PageHeader
        title="Skills"
        description="Agent skills you can hand to any AI tool. Copy a link and paste it into the chat."
      />

      <div className="flex flex-wrap items-center justify-between gap-3 rounded-xl bg-surface px-4 py-3">
        <div className="min-w-0">
          <p className="text-[11px] font-medium uppercase tracking-wide text-text-muted">Paste this to your AI</p>
          <p className="mt-1 break-all font-mono text-xs text-text-main">{prompt}</p>
        </div>
        <CopyButton value={prompt} label="Copy prompt" title="Copy the prompt above" />
      </div>

      <section className="overflow-hidden rounded-xl bg-surface">
        {SKILLS.map((skill, i) => (
          <SkillRow key={skill.id} skill={skill} first={i === 0} />
        ))}
      </section>

      <div className="flex flex-wrap items-center justify-between gap-3 px-1 text-xs text-text-muted">
        <p>Browse source, README, and examples in the repo.</p>
        <a
          href={`${SKILLS_BLOB_BASE}`}
          target="_blank"
          rel="noreferrer"
          className="inline-flex items-center gap-1 font-medium text-text-main hover:underline"
        >
          <Icon name="open_in_new" className="text-[14px]" />
          View on GitHub
        </a>
      </div>
    </div>
  )
}
