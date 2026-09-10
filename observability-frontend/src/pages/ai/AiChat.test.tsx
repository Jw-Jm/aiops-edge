import { describe, expect, it } from 'vitest'
import source from './AiChat.tsx?raw'

describe('AI chat resource scope', () => {
  it('shows the active typed resource and transfers only a draft URL', () => {
    expect(source).toContain('Chat 活动资源')
    expect(source).toContain('resourceType')
    expect(source).toContain("navigate(`/investigation/new?")
    expect(source).not.toContain('createRun(')
  })

  it('renders the evidence-grounded assistant workspace instead of an unscoped free-text reply', () => {
    expect(source).toContain('<AssistantContextPanel')
    expect(source).toContain('freezeAssistantScope')
    expect(source).toContain('AssistantAnswerCard')
    expect(source).toContain('AssistantContextPanel')
    expect(source).toContain('收集事实')
    expect(source).toContain('检索知识')
    expect(source).toContain('组织回答')
    expect(source).not.toContain('立即执行')
  })
})
