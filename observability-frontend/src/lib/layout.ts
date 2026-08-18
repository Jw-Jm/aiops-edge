/**
 * 图布局共享模块（B8）：确定性力导向布局。
 * 两处（ServiceObservability / KnowledgeGraph）原先各自手写 O(n²)×300 迭代且用
 * Math.random() 抖动，导致布局不可复现。此处统一实现：
 * - mulberry32 seeded PRNG 替代 Math.random（同数据同参数布局可复现）；
 * - 斥力 + 弹簧力 + 引力 + 硬性边界 clamp，与旧行为保持一致；
 * - 已收敛的稳定坐标作为种子输入，复用稳定位置。
 */

/**
 * mulberry32 确定性伪随机数生成器。
 * 同 seed 产生相同序列，替代布局中的 Math.random() 抖动。
 */
export function mulberry32(seed: number): () => number {
  let a = seed >>> 0
  return () => {
    a |= 0
    a = (a + 0x6d2b79f5) | 0
    let t = Math.imul(a ^ (a >>> 15), 1 | a)
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296
  }
}

/** 布局输入节点：id 必填；x/y 为可选稳定种子坐标（已收敛节点复用） */
export interface LayoutNode {
  id: string
  x?: number
  y?: number
}

/** 布局输入边：source/target 为节点 id 字符串 */
export interface LayoutEdge {
  source: string
  target: string
}

export interface ForceLayoutOptions {
  /** 画布宽度（用于半径 / 引力中心 / clamp），默认 800 */
  width?: number
  /** 画布高度（用于半径 / 引力中心 / clamp），默认 500 */
  height?: number
  /** 力导向迭代轮数，默认 300 */
  iterations?: number
  /** 四周内边距，默认 24 */
  padding?: number
  /** 底部额外内边距（知识图谱给图例让位），默认等于 padding */
  paddingBottom?: number
  /** 伪随机种子，默认固定值保证可复现 */
  seed?: number
}

export type ForceLayoutPositions = Record<string, { x: number; y: number }>

/**
 * 自研力导向布局 + 硬性边界约束：
 * 斥力(320/d²) + 弹簧(理想距离 180) + 引力(拉回画布中心) + 每轮 clamp 到画布内。
 * 返回 { nodeId -> {x,y} } 位置表。
 */
export function computeForceLayout(
  nodes: LayoutNode[],
  edges: LayoutEdge[],
  options: ForceLayoutOptions = {}
): ForceLayoutPositions {
  const width = options.width || 800
  const height = options.height || 500
  const iterations = options.iterations || 300
  const pad = options.padding ?? 24
  const padBottom = options.paddingBottom ?? pad
  // 固定种子：同数据、同画布尺寸时布局完全可复现
  const rand = mulberry32(options.seed ?? 0x9e3779b9)
  const cx = width / 2
  const cy = height / 2
  const R = Math.min(width, height) * 0.32
  const N = Math.max(1, nodes.length)

  // 初始位置：有稳定种子坐标（已收敛）则复用，否则用环形种子位置
  const positions: ForceLayoutPositions = {}
  nodes.forEach((n, i) => {
    const prev = typeof n.x === 'number' && typeof n.y === 'number'
      ? { x: n.x, y: n.y }
      : { x: cx + R * Math.cos((2 * Math.PI * i) / N), y: cy + R * Math.sin((2 * Math.PI * i) / N) }
    positions[n.id] = { x: prev.x, y: prev.y }
  })
  const nodeIds = nodes.map((n) => n.id)
  const edgeArr = edges.map((e) => ({ s: String(e.source), t: String(e.target) }))

  for (let iter = 0; iter < iterations; iter++) {
    // 斥力：所有节点两两排斥（O(n²)）
    for (let i = 0; i < nodeIds.length; i++) {
      for (let j = i + 1; j < nodeIds.length; j++) {
        const a = positions[nodeIds[i]]
        const b = positions[nodeIds[j]]
        let dx = a.x - b.x
        let dy = a.y - b.y
        let d2 = dx * dx + dy * dy
        // 完全重叠时用 seeded 抖动打破对称（原实现为 Math.random()）
        if (d2 < 1) {
          dx = (rand() - 0.5) * 2
          dy = (rand() - 0.5) * 2
          d2 = 1
        }
        const d = Math.sqrt(d2)
        const force = 320 / d2 // 斥力与距离平方成反比
        const fx = (dx / d) * force
        const fy = (dy / d) * force
        a.x += fx
        a.y += fy
        b.x -= fx
        b.y -= fy
      }
    }
    // 弹簧力：有边的节点拉到理想距离
    for (const e of edgeArr) {
      const a = positions[e.s]
      const b = positions[e.t]
      if (!a || !b) continue
      let dx = b.x - a.x
      let dy = b.y - a.y
      const d = Math.max(0.01, Math.sqrt(dx * dx + dy * dy))
      const ideal = 180
      const f = (d - ideal) * 0.02
      a.x += (dx / d) * f
      a.y += (dy / d) * f
      b.x -= (dx / d) * f
      b.y -= (dy / d) * f
    }
    // 引力：拉到画布中心
    for (const id of nodeIds) {
      const p = positions[id]
      p.x += (cx - p.x) * 0.08
      p.y += (cy - p.y) * 0.08
    }
    // 硬性边界约束：clamp 到画布内
    for (const id of nodeIds) {
      const p = positions[id]
      p.x = Math.max(pad, Math.min(width - pad, p.x))
      p.y = Math.max(pad, Math.min(height - padBottom, p.y))
    }
  }
  return positions
}
