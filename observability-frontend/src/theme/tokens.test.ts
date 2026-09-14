import { describe, expect, it } from 'vitest'
import { HEALTH_LABELS, operationsPalette, token } from './tokens'

describe('IA semantic tokens', () => {
  it('keeps action blue distinct from severity colors', () => {
    expect(token.colorPrimary).toBe('#3157d5')
    expect(token.colorError).toBe('#c9362b')
    expect(token.colorWarning).toBe('#c46816')
    expect(token.colorSuccess).toBe('#18864b')
    expect(new Set([token.colorPrimary, token.colorError, token.colorWarning, token.colorSuccess]).size).toBe(4)
  })
  it('keeps compact cards and click targets usable', () => {
    expect(token.radiusCard).toBe(10)
    expect(token.minClickTarget).toBeGreaterThanOrEqual(36)
    expect(token.tableRowHeight).toBe(44)
  })
  it('uses the four-state health vocabulary without a synthetic score', () => {
    expect(HEALTH_LABELS).toEqual({ healthy: '健康', degraded: '降级', critical: '严重', unknown: '未知' })
  })
  it('exposes one operations palette for CSS and Ant components', () => {
    expect(operationsPalette).toMatchObject({ canvas: '#F5F7FA', surface: '#FFFFFF', interaction: '#3157D5', critical: '#C9362B', degraded: '#C46816', risk: '#A46F0A', healthy: '#18864B' })
  })
})
