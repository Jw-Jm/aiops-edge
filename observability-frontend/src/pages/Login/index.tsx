import React, { useState } from 'react'
import { Form, Input, Button, message } from 'antd'
import { UserOutlined, LockOutlined, LoginOutlined } from '@ant-design/icons'
import { useNavigate } from 'react-router-dom'
import { login } from '../../api/client'
import { useAuthStore } from '../../store/authStore'

const Login: React.FC = () => {
  const navigate = useNavigate()
  const [loading, setLoading] = useState(false)

  const onFinish = async (values: { username: string; password: string }) => {
    setLoading(true)
    try {
      const res = await login(values.username, values.password)
      const token = res.data?.token || res.data?.data?.token
      if (token) {
        useAuthStore.getState().login(token, res.data?.username || values.username, res.data?.role || 'user', res.data?.display_name || '')
        message.success('登录成功')
        navigate('/overview')
      } else {
        message.error('登录失败：未收到 token')
      }
    } catch (err: any) {
      message.error(err?.response?.data?.message || err?.response?.data?.error || '登录失败，请检查用户名和密码')
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="login-bg">
      <aside className="login-aside">
        <div className="brand" style={{ border: 'none', padding: 0, height: 'auto' }}>
          <div className="brand__logo">观</div>
          <div>
            <div className="brand__name">智能可观测平台</div>
            <div className="brand__sub">AIOps Observability</div>
          </div>
        </div>
        <div className="hero">
          <h1>把复杂留给我们，<br />把清醒还给运维。</h1>
          <p>统一指标、日志、链路与告警，结合 AI 根因分析与一键应急处置，让每一次故障都更快定位、更少误判。</p>
          <div className="feature-list">
            <div className="feature">
              <div className="ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><path d="M22 12h-4l-3 9L9 3l-3 9H2" /></svg></div>
              <div><h4>统一可观测</h4><p>Metrics / Logs / Traces 一张图看全。</p></div>
            </div>
            <div className="feature">
              <div className="ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><path d="M18 8a6 6 0 0 0-12 0c0 7-3 9-3 9h18s-3-2-3-9" /></svg></div>
              <div><h4>智能告警收敛</h4><p>相似告警自动聚合降噪，紧急事件一目了然。</p></div>
            </div>
            <div className="feature">
              <div className="ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z" /></svg></div>
              <div><h4>一键应急处置</h4><p>预置恢复剧本与审批流，闭环可追溯。</p></div>
            </div>
          </div>
        </div>
        <div className="text-sm muted">© 2026 智能可观测平台 · 演示环境</div>
      </aside>

      <div className="login-card-wrap">
        <div className="login-card">
          <h2>欢迎回来</h2>
          <div className="text-sm secondary" style={{ marginBottom: 24 }}>登录以进入运维控制台</div>
          <Form name="login" onFinish={onFinish} layout="vertical" autoComplete="off">
            <Form.Item name="username" rules={[{ required: true, message: '请输入用户名' }]}>
              <Input prefix={<UserOutlined style={{ color: 'var(--text-muted)' }} />} placeholder="用户名" autoComplete="username" size="large"
                style={{ background: 'var(--surface-2)', border: '1px solid var(--border)' }} />
            </Form.Item>
            <Form.Item name="password" rules={[{ required: true, message: '请输入密码' }]}>
              <Input.Password prefix={<LockOutlined style={{ color: 'var(--text-muted)' }} />} placeholder="密码" autoComplete="current-password" size="large"
                style={{ background: 'var(--surface-2)', border: '1px solid var(--border)' }} />
            </Form.Item>
            <Form.Item style={{ marginBottom: 8 }}>
              <Button type="primary" htmlType="submit" icon={<LoginOutlined />} loading={loading} block size="large"
                style={{ height: 44, borderRadius: 8, boxShadow: '0 2px 6px rgba(47,84,235,.20)' }}>
                登 录
              </Button>
            </Form.Item>
          </Form>
          <div className="login-hint">演示账号：admin / admin123</div>
        </div>
      </div>
    </div>
  )
}

export default Login
