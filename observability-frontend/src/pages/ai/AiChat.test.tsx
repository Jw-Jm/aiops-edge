import { describe, expect, it } from 'vitest'
import source from './AiChat.tsx?raw'

describe('AI chat resource scope', () => {
  it('shows the active typed resource and transfers only a draft URL', () => {
    expect(source).toContain('Chat 活动资源')
    expect(source).toContain('resourceType')
    expect(source).toContain("navigate(`/investigation/new?")
    expect(source).not.toContain('createRun(')
  })
})
