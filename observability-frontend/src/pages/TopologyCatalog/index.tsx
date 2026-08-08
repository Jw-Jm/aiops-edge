import React, { useEffect, useState } from 'react'
import {
  Card, Table, Button, Tag, Modal, Form, Input, Select, InputNumber, Switch, Space, Typography, Popconfirm, Tabs, message,
} from 'antd'
import { PlusOutlined, ReloadOutlined } from '@ant-design/icons'
import {
  topoListNodes, topoCreateNode, topoUpdateNode, topoDeleteNode,
  topoListRelations, topoCreateRelation, topoDeleteRelation,
  topoListNodeTypes, topoCreateNodeType, topoDeleteNodeType,
  topoListRelationTypes, topoCreateRelationType, topoDeleteRelationType,
} from '../../api/client'

const { Text } = Typography

const TIER_LABEL: Record<number, string> = {
  0: '应用(顶层)', 1: '服务', 2: '集群', 3: '设备', 4: '机架(底层)', 99: '其他(独立行)',
}

const TopologyCatalog: React.FC = () => {
  const [activeTab, setActiveTab] = useState('nodes')
  const [nodes, setNodes] = useState<any[]>([])
  const [relations, setRelations] = useState<any[]>([])
  const [nodeTypes, setNodeTypes] = useState<any[]>([])
  const [relationTypes, setRelationTypes] = useState<any[]>([])
  const [loading, setLoading] = useState(false)
  const [nodeModal, setNodeModal] = useState<{ open: boolean; edit?: any }>({ open: false })
  const [relModal, setRelModal] = useState<{ open: boolean }>({ open: false })
  const [ntModal, setNtModal] = useState<{ open: boolean }>({ open: false })
  const [rtModal, setRtModal] = useState<{ open: boolean }>({ open: false })
  const [nodeForm] = Form.useForm()
  const [relForm] = Form.useForm()
  const [ntForm] = Form.useForm()
  const [rtForm] = Form.useForm()

  const loadAll = async () => {
    setLoading(true)
    try {
      const [nr, rr, ntr, rtr] = await Promise.all([
        topoListNodes(),
        topoListRelations(),
        topoListNodeTypes(),
        topoListRelationTypes(),
      ])
      setNodes(nr.data?.items || [])
      setRelations(rr.data?.items || [])
      setNodeTypes(ntr.data?.items || [])
      setRelationTypes(rtr.data?.items || [])
    } catch (e) {
      message.error('加载拓扑目录失败')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { loadAll() }, [])

  // 节点 CRUD
  const openNodeModal = (edit?: any) => {
    setNodeModal({ open: true, edit })
    if (edit) {
      let props: any = {}
      try { props = edit.props_json ? JSON.parse(edit.props_json) : {} } catch { /* ignore */ }
      nodeForm.setFieldsValue({ type: edit.type, name: edit.name, props: JSON.stringify(props, null, 2) })
    } else {
      nodeForm.resetFields()
    }
  }
  const submitNode = async () => {
    const v = await nodeForm.validateFields()
    let props = ''
    try { props = JSON.stringify(JSON.parse(v.props || '{}')) } catch { message.error('props 必须为合法 JSON'); return }
    const data = { type: v.type, name: v.name, props_json: props }
    if (nodeModal.edit) {
      await topoUpdateNode(nodeModal.edit.id, data)
    } else {
      await topoCreateNode(data)
    }
    message.success('节点已保存')
    setNodeModal({ open: false })
    loadAll()
  }

  // 关系 CRUD
  const submitRelation = async () => {
    const v = await relForm.validateFields()
    let props = ''
    try { props = JSON.stringify(JSON.parse(v.props || '{}')) } catch { message.error('props 必须为合法 JSON'); return }
    await topoCreateRelation({ src_id: v.src_id, dst_id: v.dst_id, type: v.type, props_json: props })
    message.success('关系已创建')
    setRelModal({ open: false })
    loadAll()
  }

  // 节点类型 CRUD
  const submitNodeType = async () => {
    const v = await ntForm.validateFields()
    await topoCreateNodeType(v)
    message.success('节点类型已创建')
    setNtModal({ open: false })
    loadAll()
  }

  // 关系类型 CRUD
  const submitRelationType = async () => {
    const v = await rtForm.validateFields()
    await topoCreateRelationType(v)
    message.success('关系类型已创建')
    setRtModal({ open: false })
    loadAll()
  }

  const nodeColumns = [
    { title: 'ID', dataIndex: 'id', width: 60 },
    { title: '名称', dataIndex: 'name' },
    { title: '类型', dataIndex: 'type', render: (t: string) => <Tag color="blue">{t}</Tag> },
    { title: 'Props', dataIndex: 'props_json', ellipsis: true, render: (p: string) => p || '-' },
    {
      title: '操作', width: 160,
      render: (_: any, r: any) => (
        <Space>
          <Button size="small" onClick={() => openNodeModal(r)}>编辑</Button>
          <Popconfirm title="删除该节点？" onConfirm={async () => { await topoDeleteNode(r.id); message.success('已删除'); loadAll() }}>
            <Button size="small" danger>删除</Button>
          </Popconfirm>
        </Space>
      ),
    },
  ]

  const relationColumns = [
    { title: 'ID', dataIndex: 'id', width: 60 },
    { title: '源节点', dataIndex: 'src_id', render: (id: number) => nodes.find(n => n.id === id)?.name || `#${id}` },
    { title: '目标节点', dataIndex: 'dst_id', render: (id: number) => nodes.find(n => n.id === id)?.name || `#${id}` },
    { title: '关系类型', dataIndex: 'type', render: (t: string) => <Tag color="green">{t}</Tag> },
    {
      title: '操作', width: 100,
      render: (_: any, r: any) => (
        <Popconfirm title="删除该关系？" onConfirm={async () => { await topoDeleteRelation(r.id); message.success('已删除'); loadAll() }}>
          <Button size="small" danger>删除</Button>
        </Popconfirm>
      ),
    },
  ]

  const nodeTypeColumns = [
    { title: '名称', dataIndex: 'name', render: (n: string) => <Text code>{n}</Text> },
    { title: '显示名', dataIndex: 'display_name' },
    { title: '层级(tier)', dataIndex: 'tier', render: (t: number) => TIER_LABEL[t] || t },
    { title: '内置', dataIndex: 'builtin', render: (b: boolean) => (b ? <Tag color="gold">内置</Tag> : <Tag>自定义</Tag>) },
    { title: '描述', dataIndex: 'description', ellipsis: true },
    {
      title: '操作', width: 100,
      render: (_: any, r: any) => r.builtin ? <Text type="secondary">内置不可删</Text> : (
        <Popconfirm title="删除该类型？" onConfirm={async () => { await topoDeleteNodeType(r.name); message.success('已删除'); loadAll() }}>
          <Button size="small" danger>删除</Button>
        </Popconfirm>
      ),
    },
  ]

  const relationTypeColumns = [
    { title: '名称', dataIndex: 'name', render: (n: string) => <Text code>{n}</Text> },
    { title: '显示名', dataIndex: 'display_name' },
    { title: '方向', dataIndex: 'direction', render: (d: string) => <Tag color="geekblue">{d}</Tag> },
    { title: '语义', dataIndex: 'semantics_tag', render: (s: string) => <Tag color="purple">{s}</Tag> },
    { title: '传播故障', dataIndex: 'propagates_failure', render: (p: boolean) => (p ? '是' : '否') },
    { title: '内置', dataIndex: 'builtin', render: (b: boolean) => (b ? <Tag color="gold">内置</Tag> : <Tag>自定义</Tag>) },
    {
      title: '操作', width: 100,
      render: (_: any, r: any) => r.builtin ? <Text type="secondary">内置不可删</Text> : (
        <Popconfirm title="删除该类型？" onConfirm={async () => { await topoDeleteRelationType(r.name); message.success('已删除'); loadAll() }}>
          <Button size="small" danger>删除</Button>
        </Popconfirm>
      ),
    },
  ]

  const tabs = [
    {
      key: 'nodes', label: `节点 (${nodes.length})`, children: (
        <Card
          title="拓扑节点"
          extra={<Space>
            <Button icon={<ReloadOutlined />} onClick={loadAll}>刷新</Button>
            <Button type="primary" icon={<PlusOutlined />} onClick={() => openNodeModal()}>新增节点</Button>
          </Space>}
        >
          <Table rowKey="id" size="small" loading={loading} columns={nodeColumns} dataSource={nodes} pagination={{ pageSize: 10 }} />
        </Card>
      ),
    },
    {
      key: 'relations', label: `关系 (${relations.length})`, children: (
        <Card
          title="拓扑关系"
          extra={<Space>
            <Button icon={<ReloadOutlined />} onClick={loadAll}>刷新</Button>
            <Button type="primary" icon={<PlusOutlined />} onClick={() => { relForm.resetFields(); setRelModal({ open: true }) }}>新增关系</Button>
          </Space>}
        >
          <Table rowKey="id" size="small" loading={loading} columns={relationColumns} dataSource={relations} pagination={{ pageSize: 10 }} />
        </Card>
      ),
    },
    {
      key: 'nodeTypes', label: `节点类型 (${nodeTypes.length})`, children: (
        <Card
          title="节点类型目录"
          extra={<Space>
            <Button icon={<ReloadOutlined />} onClick={loadAll}>刷新</Button>
            <Button type="primary" icon={<PlusOutlined />} onClick={() => { ntForm.resetFields(); ntForm.setFieldsValue({ tier: 99 }); setNtModal({ open: true }) }}>新增类型</Button>
          </Space>}
        >
          <Table rowKey="name" size="small" loading={loading} columns={nodeTypeColumns} dataSource={nodeTypes} pagination={false} />
        </Card>
      ),
    },
    {
      key: 'relationTypes', label: `关系类型 (${relationTypes.length})`, children: (
        <Card
          title="关系类型目录"
          extra={<Space>
            <Button icon={<ReloadOutlined />} onClick={loadAll}>刷新</Button>
            <Button type="primary" icon={<PlusOutlined />} onClick={() => { rtForm.resetFields(); rtForm.setFieldsValue({ direction: 'src_to_dst', semantics_tag: 'observation' }); setRtModal({ open: true }) }}>新增类型</Button>
          </Space>}
        >
          <Table rowKey="name" size="small" loading={loading} columns={relationTypeColumns} dataSource={relationTypes} pagination={false} />
        </Card>
      ),
    },
  ]

  return (
    <>
      <Card title="拓扑目录管理" style={{ marginBottom: 16 }} styles={{ body: { paddingTop: 8 } }}>
        <Text type="secondary">
          拓扑为带类型的属性图（typed property graph）：节点（应用/服务/集群/设备/机架）+ 关系（依赖/部署于/路由到…），
          类型目录驱动图谱分层。内置类型不可删除，可注册自定义类型。
        </Text>
      </Card>
      <Tabs activeKey={activeTab} onChange={setActiveTab} items={tabs} />

      {/* 节点 Modal */}
      <Modal title={nodeModal.edit ? '编辑节点' : '新增节点'} open={nodeModal.open} onCancel={() => setNodeModal({ open: false })} onOk={submitNode} width={520}>
        <Form form={nodeForm} layout="vertical">
          <Form.Item name="type" label="类型" rules={[{ required: true }]}>
            <Select
              showSearch
              placeholder="选择或输入节点类型"
              options={nodeTypes.map(nt => ({ value: nt.name, label: `${nt.name} (${nt.display_name})` }))}
            />
          </Form.Item>
          <Form.Item name="name" label="名称" rules={[{ required: true }]}>
            <Input placeholder="order-api" />
          </Form.Item>
          <Form.Item name="props" label="Props (JSON)">
            <Input.TextArea rows={4} placeholder='{"team":"ops"}' />
          </Form.Item>
        </Form>
      </Modal>

      {/* 关系 Modal */}
      <Modal title="新增关系" open={relModal.open} onCancel={() => setRelModal({ open: false })} onOk={submitRelation} width={520}>
        <Form form={relForm} layout="vertical">
          <Form.Item name="src_id" label="源节点" rules={[{ required: true }]}>
            <Select showSearch optionFilterProp="label" placeholder="选择源节点" options={nodes.map(n => ({ value: n.id, label: `${n.name} (#${n.id})` }))} />
          </Form.Item>
          <Form.Item name="dst_id" label="目标节点" rules={[{ required: true }]}>
            <Select showSearch optionFilterProp="label" placeholder="选择目标节点" options={nodes.map(n => ({ value: n.id, label: `${n.name} (#${n.id})` }))} />
          </Form.Item>
          <Form.Item name="type" label="关系类型" rules={[{ required: true }]}>
            <Select showSearch placeholder="选择关系类型" options={relationTypes.map(rt => ({ value: rt.name, label: `${rt.name} (${rt.display_name})` }))} />
          </Form.Item>
          <Form.Item name="props" label="Props (JSON)">
            <Input.TextArea rows={3} />
          </Form.Item>
        </Form>
      </Modal>

      {/* 节点类型 Modal */}
      <Modal title="新增节点类型" open={ntModal.open} onCancel={() => setNtModal({ open: false })} onOk={submitNodeType} width={520}>
        <Form form={ntForm} layout="vertical">
          <Form.Item name="name" label="名称" rules={[{ required: true }]}><Input placeholder="vm / datacenter" /></Form.Item>
          <Form.Item name="display_name" label="显示名" rules={[{ required: true }]}><Input placeholder="虚拟机" /></Form.Item>
          <Form.Item name="tier" label="层级 (tier)" rules={[{ required: true }]}><InputNumber min={0} style={{ width: '100%' }} /></Form.Item>
          <Form.Item name="description" label="描述"><Input.TextArea rows={2} /></Form.Item>
        </Form>
      </Modal>

      {/* 关系类型 Modal */}
      <Modal title="新增关系类型" open={rtModal.open} onCancel={() => setRtModal({ open: false })} onOk={submitRelationType} width={520}>
        <Form form={rtForm} layout="vertical">
          <Form.Item name="name" label="名称" rules={[{ required: true }]}><Input placeholder="serves / hosts" /></Form.Item>
          <Form.Item name="display_name" label="显示名" rules={[{ required: true }]}><Input placeholder="服务" /></Form.Item>
          <Form.Item name="direction" label="方向" rules={[{ required: true }]}>
            <Select options={[
              { value: 'src_to_dst', label: 'src→dst' },
              { value: 'dst_to_src', label: 'dst→src' },
              { value: 'bidirectional', label: '双向' },
            ]} />
          </Form.Item>
          <Form.Item name="semantics_tag" label="语义标签" rules={[{ required: true }]}>
            <Select options={[
              { value: 'hard_dep', label: '硬依赖' },
              { value: 'runtime_dep', label: '运行时依赖' },
              { value: 'aggregation', label: '聚合' },
              { value: 'redundancy', label: '冗余' },
              { value: 'observation', label: '观测' },
              { value: 'traffic', label: '流量' },
              { value: 'annotation', label: '注解' },
            ]} />
          </Form.Item>
          <Form.Item name="propagates_failure" label="传播故障" valuePropName="checked"><Switch /></Form.Item>
          <Form.Item name="description" label="描述"><Input.TextArea rows={2} /></Form.Item>
        </Form>
      </Modal>
    </>
  )
}

export default TopologyCatalog
