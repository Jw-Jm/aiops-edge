import { describe, expect, it } from 'vitest'
import { issueAction, rankOperationalIssues, type OperationalIssue } from './priority'

const issue = (overrides: Partial<OperationalIssue>): OperationalIssue => ({
  id: 'issue', title: 'issue', severity: 'warning', affectedServices: 1,
  durationMinutes: 10, recentChange: false, runId: undefined,
  resourceId: 'svc/issue', symptom: 'error rate increased', ...overrides,
})

describe('operational issue priority', () => {
  it('ranks critical issues before wider but less severe warnings', () => {
    const ranked = rankOperationalIssues([
      issue({ id: 'warning', severity: 'warning', affectedServices: 5, durationMinutes: 120 }),
      issue({ id: 'critical', severity: 'critical', affectedServices: 1, durationMinutes: 5 }),
    ])
    expect(ranked.map((item) => item.id)).toEqual(['critical', 'warning'])
  })

  it('selects a lifecycle-aware primary action', () => {
    expect(issueAction(issue({ runId: undefined }))).toEqual({ label: '开始调查', href: '/investigation/new?source=overview&resource=svc%2Fissue&symptom=error%20rate%20increased' })
    expect(issueAction(issue({ runId: 'run-1' }))).toEqual({ label: '查看调查', href: '/investigation/run-1' })
  })
})
