import React, { useState } from 'react'
import { Card, Form, Input, Button, Typography, message } from 'antd'
import { UserOutlined, LockOutlined, LoginOutlined, ThunderboltOutlined, DatabaseOutlined, SafetyCertificateOutlined } from '@ant-design/icons'
import { useNavigate } from 'react-router-dom'
import { login } from '../../api/client'

const { Title, Text } = Typography

const Login: React.FC = () => {
  const navigate = useNavigate()
  const [loading, setLoading] = useState(false)

  const onFinish = async (values: { username: string; password: string }) => {
    setLoading(true)
    try {
      const res = await login(values.username, values.password)
      const token = res.data?.token || res.data?.data?.token
      if (token) {
        localStorage.setItem('token', token)
        message.success('登录成功')
        navigate('/')
      } else {
        message.error('登录失败：未收到 token')
      }
    } catch (err: any) {
      const msg = err?.response?.data?.message || err?.response?.data?.error || '登录失败，请检查用户名和密码'
      message.error(msg)
    } finally {
      setLoading(false)
    }
  }

  return (
    <div
      style={{
        minHeight: '100vh',
        display: 'flex',
        justifyContent: 'center',
        alignItems: 'center',
        background: 'radial-gradient(circle at 20% 20%, rgba(22,119,255,0.15), transparent 45%), radial-gradient(circle at 80% 80%, rgba(114,46,209,0.18), transparent 45%), #0a0f1c',
      }}
    >
      <Card
        style={{
          width: 400,
          boxShadow: '0 12px 40px rgba(0, 0, 0, 0.45)',
          borderRadius: 16,
          background: 'rgba(18,24,38,0.9)',
          border: '1px solid rgba(255,255,255,0.1)',
          backdropFilter: 'blur(12px)',
        }}
        styles={{ body: { padding: '40px 36px' } }}
      >
        <div style={{ textAlign: 'center', marginBottom: 32 }}>
          <div
            style={{
              width: 56,
              height: 56,
              background: 'linear-gradient(135deg, #1677ff, #722ed1)',
              borderRadius: 14,
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
              margin: '0 auto 16px',
              boxShadow: '0 6px 20px rgba(22,119,255,0.4)',
            }}
          >
            <ThunderboltOutlined style={{ color: '#fff', fontSize: 26 }} />
          </div>
          <Title level={3} style={{ margin: 0, color: '#fff' }}>
            AIOps 智能运维平台
          </Title>
          <Text style={{ color: 'rgba(255,255,255,0.5)' }}>统一可观测性 · 智能诊断 · 自动化运维</Text>
        </div>

        <Form
          name="login"
          onFinish={onFinish}
          layout="vertical"
          size="large"
          autoComplete="off"
        >
          <Form.Item
            name="username"
            rules={[{ required: true, message: '请输入用户名' }]}
          >
            <Input
              prefix={<UserOutlined style={{ color: 'rgba(255,255,255,0.35)' }} />}
              placeholder="用户名"
              autoComplete="username"
              style={{ background: 'rgba(255,255,255,0.06)', border: '1px solid rgba(255,255,255,0.12)' }}
            />
          </Form.Item>

          <Form.Item
            name="password"
            rules={[{ required: true, message: '请输入密码' }]}
          >
            <Input.Password
              prefix={<LockOutlined style={{ color: 'rgba(255,255,255,0.35)' }} />}
              placeholder="密码"
              autoComplete="current-password"
              style={{ background: 'rgba(255,255,255,0.06)', border: '1px solid rgba(255,255,255,0.12)' }}
            />
          </Form.Item>

          <Form.Item style={{ marginBottom: 12 }}>
            <Button
              type="primary"
              htmlType="submit"
              icon={<LoginOutlined />}
              loading={loading}
              block
              style={{
                height: 44,
                background: 'linear-gradient(135deg, #1677ff, #722ed1)',
                border: 'none',
                borderRadius: 8,
                boxShadow: '0 6px 16px rgba(22,119,255,0.3)',
              }}
            >
              登录
            </Button>
          </Form.Item>

          <div style={{ display: 'flex', justifyContent: 'center', gap: 16, marginTop: 8 }}>
            <Text style={{ color: 'rgba(255,255,255,0.3)', fontSize: 12 }}>
              <DatabaseOutlined /> 指标 · 链路 · 日志
            </Text>
            <Text style={{ color: 'rgba(255,255,255,0.3)', fontSize: 12 }}>
              <SafetyCertificateOutlined /> 安全接入
            </Text>
          </div>
        </Form>
      </Card>
    </div>
  )
}

export default Login
