import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { describe, expect, it, vi } from 'vitest'
import Resources from './index'

vi.mock('./ResourceDirectory', () => ({ default: ({ group }: { group?: string }) => <div>{group === 'kubevirt' ? 'KubeVirt 资源目录' : '容器资源目录'}</div> }))
vi.mock('../observability/ResourceRelationships', () => ({ default: () => <div>资源关系</div> }))
vi.mock('./ResourceCenter', () => ({ default: ({ children }: { children: React.ReactNode }) => <div>{children}</div> }))

describe('Resources carrier entry', () => {
  it('exposes only container and KubeVirt carrier tabs plus relations', () => {
    render(<MemoryRouter initialEntries={['/clusters/cluster-a/resources']}><Resources /></MemoryRouter>)
    expect(screen.getByText('容器资源')).toBeInTheDocument()
    expect(screen.getByText('KubeVirt 虚拟机')).toBeInTheDocument()
    expect(screen.getByText('资源关系')).toBeInTheDocument()
    expect(screen.queryByText('应用服务')).not.toBeInTheDocument()
    expect(screen.queryByText('计算')).not.toBeInTheDocument()
    expect(screen.queryByText('Pod/Container')).not.toBeInTheDocument()
  })
})
