import { useState } from 'react'
import { PageHeader } from '@/components/layout'
import { Icon } from '@/components/icon'
import { Badge } from '@/components/ui/badge'
import { Card } from '@/components/ui/card'
import {
  SKILLS,
  SKILLS_REPO_URL,
  getSkillBlobUrl,
  getSkillRawUrl,
  type Skill,
} from '@/lib/skills'
import { cn } from '@/lib/utils'

function CopyLinkButton({ value, label = 'Copy link' }: { value: string; label?: string }) {
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
      title={value}
      className="inline-flex shrink-0 cursor-pointer items-center gap-1 rounded-md bg-primary px-2 py-1 text-[11px] font-medium text-white transition-colors hover:bg-primary/90"
    >
      <Icon name={copied ? 'check' : 'content_copy'} className="text-[12px]" />
      {copied ? 'Copied!' : label}
    </button>
  )
}

function SkillRow({ skill }: { skill: Skill }) {
  const url = getSkillRawUrl(skill.id)
  return (
    <div
      className={cn(
        'flex items-start gap-3 rounded-[14px] border p-4 shadow-[var(--shadow-soft)] transition-colors',
        skill.isEntry
          ? 'border-brand-500/40 bg-brand-500/5'
          : 'border-border-subtle bg-surface hover:bg-surface-2',
      )}
    >
      <div
        className={cn(
          'flex size-9 shrink-0 items-center justify-center rounded-lg',
          skill.isEntry ? 'bg-primary text-white' : 'bg-primary/10 text-primary',
        )}
      >
        <Icon name={skill.icon} className="text-[18px]" />
      </div>

      <div className="min-w-0 flex-1">
        <div className="flex flex-wrap items-center gap-2">
          <h3 className="text-sm font-semibold text-text-main">{skill.name}</h3>
          {skill.isEntry && <Badge>START HERE</Badge>}
          {skill.endpoint && (
            <Badge variant="outline">
              <code className="text-[10px]">{skill.endpoint}</code>
            </Badge>
          )}
        </div>
        <p className="mt-0.5 text-xs text-text-muted">{skill.description}</p>
        <a
          href={getSkillBlobUrl(skill.id)}
          target="_blank"
          rel="noreferrer"
          className="mt-1 inline-flex items-center gap-1 break-all text-[11px] text-text-muted hover:text-primary"
        >
          {url}
          <Icon name="open_in_new" className="text-[12px]" />
        </a>
      </div>

      <CopyLinkButton value={url} />
    </div>
  )
}

export default function SkillsPage() {
  return (
    <div className="mx-auto max-w-4xl space-y-6">
      <PageHeader
        title="Skills"
        description="Agent skills you can hand to any AI tool. Copy a link and paste it into the chat."
      />

      <Card className="p-4">
        <div className="mb-2 text-xs text-text-muted">Paste this to your AI:</div>
        <div className="rounded bg-surface-2 px-3 py-2 font-mono text-[12px] text-text-main">
          Read this skill and use it: {getSkillRawUrl('kinerouter')}
        </div>
      </Card>

      <div className="space-y-2">
        {SKILLS.map((skill) => (
          <SkillRow key={skill.id} skill={skill} />
        ))}
      </div>

      <Card className="p-4">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div>
            <h2 className="text-sm font-semibold text-text-main">More on GitHub</h2>
            <p className="mt-0.5 text-xs text-text-muted">Browse source, README, and examples.</p>
          </div>
          <a
            href={`${SKILLS_REPO_URL}/tree/master/skills`}
            target="_blank"
            rel="noreferrer"
            className="inline-flex items-center gap-1 text-sm text-primary hover:underline"
          >
            <Icon name="open_in_new" className="text-[16px]" />
            View on GitHub
          </a>
        </div>
      </Card>
    </div>
  )
}
