import { describe, expect, it } from 'vitest'
import { ALLOWED_PATH_CATEGORIES, riskScore, selectPath, type ObservabilityPath } from './pathSelection'

const p = (over: Partial<ObservabilityPath> & { pathId: string }): ObservabilityPath => ({
  name: over.pathId,
  category: 'Pod 网络',
  status: 'degraded',
  severity: 2,
  affectedResources: 5,
  deviation: 0.5,
  durationSeconds: 600,
  quality: 'healthy',
  ...over,
})

describe('全链路路径选择契约', () => {
  it('风险分对严重度、影响、偏离、持续与质量单调', () => {
    const low = p({ pathId: 'a', severity: 1 })
    const high = p({ pathId: 'b', severity: 4 })
    expect(riskScore(high)).toBeGreaterThan(riskScore(low))
    expect(riskScore(p({ pathId: 'c', affectedResources: 20 }))).toBeGreaterThan(
      riskScore(p({ pathId: 'd', affectedResources: 1 })),
    )
    expect(riskScore(p({ pathId: 'e', quality: 'healthy' }))).toBeGreaterThan(
      riskScore(p({ pathId: 'f', quality: 'failed' })),
    )
  })

  it('风险分被归一化到 0-1，非法输入不产生 NaN', () => {
    const score = riskScore(p({ pathId: 'x', severity: Number.NaN, deviation: -5, durationSeconds: -1 }))
    expect(score).toBeGreaterThanOrEqual(0)
    expect(score).toBeLessThanOrEqual(1)
  })

  it('直接进入时按确定性排序选出最高风险路径并给出选择原因', () => {
    const result = selectPath([
      p({ pathId: 'low', severity: 1, affectedResources: 1 }),
      p({ pathId: 'high', severity: 4, affectedResources: 18, deviation: 0.9, durationSeconds: 3600 }),
    ])
    expect(result.selectedPathId).toBe('high')
    expect(result.reason).toContain('确定性风险排序')
    expect(result.factors).toHaveLength(5)
  })

  it('同分时按 pathId 字典序，保证可复现', () => {
    const a = p({ pathId: 'aaa' })
    const b = p({ pathId: 'bbb' })
    expect(selectPath([b, a]).selectedPathId).toBe('aaa')
    expect(selectPath([a, b]).selectedPathId).toBe('aaa')
  })

  it('从其他页面进入时优先继承路径上下文', () => {
    const result = selectPath(
      [p({ pathId: 'low', severity: 1 }), p({ pathId: 'inherited', severity: 2 })],
      'inherited',
    )
    expect(result.selectedPathId).toBe('inherited')
    expect(result.reason).toContain('继承')
  })

  it('继承的 pathId 不存在时退回确定性排序而不是空白面板', () => {
    const result = selectPath([p({ pathId: 'real', severity: 3 })], 'stale-deep-link')
    expect(result.selectedPathId).toBe('real')
  })

  it('路径目录为空时明确表达无法选择，不编造默认路径', () => {
    const result = selectPath([], 'anything')
    expect(result.selectedPathId).toBeNull()
    expect(result.reason).toContain('无法选择')
  })

  it('路径类型白名单只包含云平台基础设施路径', () => {
    expect(ALLOWED_PATH_CATEGORIES).not.toContain('下单')
    expect(ALLOWED_PATH_CATEGORIES).not.toContain('商品查询')
    expect(ALLOWED_PATH_CATEGORIES).toContain('Kubernetes 控制路径')
    expect(ALLOWED_PATH_CATEGORIES).toContain('卷挂载')
    expect(ALLOWED_PATH_CATEGORIES).toContain('DNS')
  })
})
