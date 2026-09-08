export interface OperationalIssue {
  id: string
  title: string
  severity: 'critical' | 'warning' | 'info'
  affectedServices: number
  durationMinutes: number
  recentChange: boolean
  runId?: string
  resourceId: string
  symptom: string
  startedAt?: string
}

const SEVERITY_SCORE: Record<OperationalIssue['severity'], number> = { critical: 1_000_000, warning: 100_000, info: 10_000 }

function score(item: OperationalIssue): number {
  return SEVERITY_SCORE[item.severity] + item.affectedServices * 1_000 + item.durationMinutes * 10 + (item.recentChange ? 500 : 0)
}

export function rankOperationalIssues(items: OperationalIssue[]): OperationalIssue[] {
  return [...items].sort((a, b) => score(b) - score(a) || String(b.startedAt || '').localeCompare(String(a.startedAt || '')))
}

export function issueAction(issue: OperationalIssue): { label: string; href: string } {
  return issue.runId ? { label: '查看调查', href: `/investigation/${issue.runId}` } : { label: '开始调查', href: `/investigation/new?source=overview&resource=${encodeURIComponent(issue.resourceId)}&symptom=${encodeURIComponent(issue.symptom)}` }
}
