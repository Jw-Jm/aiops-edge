import React, { useState } from 'react'
import { Button, Form, Input, message } from 'antd'
import { LockOutlined, SafetyOutlined } from '@ant-design/icons'
import { useNavigate } from 'react-router-dom'
import { changePassword } from '../../api/client'
import { useAuthStore } from '../../store/authStore'

interface ChangePasswordForm {
  current_password: string
  new_password: string
  confirm_password: string
}

const PASSWORD_ERROR_MESSAGES: Record<string, string> = {
  invalid_current_password: '当前密码不正确',
  password_mismatch: '两次输入的新密码不一致',
  password_policy: '新密码不符合密码策略',
  password_change_required: '请先完成密码修改',
}

export function humanizePasswordError(value: unknown): string {
  const code = typeof value === 'string' ? value : ''
  if (PASSWORD_ERROR_MESSAGES[code]) return PASSWORD_ERROR_MESSAGES[code]
  if (code && !/^[a-z0-9_.-]+$/i.test(code)) return code
  return '密码修改失败，请重试'
}

const ChangePassword: React.FC = () => {
  const navigate = useNavigate()
  const [loading, setLoading] = useState(false)
  const auth = useAuthStore()

  const onFinish = async (values: ChangePasswordForm) => {
    setLoading(true)
    try {
      const res = await changePassword(values)
      if (!(res.data?.authenticated === true || res.data?.data?.authenticated === true)) {
        message.error('密码修改失败：会话未建立')
        return
      }
      // The rotated credential is set as an HttpOnly cookie by the API; only
      // update the non-secret in-memory UI projection here.
      auth.login('cookie-session', auth.username, auth.role, auth.displayName, false)
      message.success('密码修改成功，请继续使用系统')
      navigate('/overview', { replace: true })
    } catch (err: any) {
      message.error(humanizePasswordError(err?.response?.data?.error || err?.response?.data?.message))
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="login-bg">
      <div className="login-card-wrap" style={{ width: '100%' }}>
        <div className="login-card" style={{ maxWidth: 460 }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 12, marginBottom: 12 }}>
            <SafetyOutlined style={{ color: 'var(--primary)', fontSize: 24 }} />
            <h2 style={{ margin: 0 }}>首次登录需要修改密码</h2>
          </div>
          <div className="text-sm secondary" style={{ marginBottom: 24 }}>
            为保护账号安全，请设置一个新的登录密码。
          </div>
          <Form name="change-password" onFinish={onFinish} layout="vertical" autoComplete="off">
            <Form.Item name="current_password" label="当前密码" rules={[{ required: true, message: '请输入当前密码' }]}>
              <Input.Password prefix={<LockOutlined style={{ color: 'var(--text-muted)' }} />} autoComplete="current-password" size="large" />
            </Form.Item>
            <Form.Item
              name="new_password"
              label="新密码"
              rules={[{ required: true, min: 8, message: '新密码至少 8 位' }]}
            >
              <Input.Password prefix={<LockOutlined style={{ color: 'var(--text-muted)' }} />} autoComplete="new-password" size="large" />
            </Form.Item>
            <Form.Item
              name="confirm_password"
              label="确认新密码"
              dependencies={['new_password']}
              rules={[
                { required: true, message: '请再次输入新密码' },
                ({ getFieldValue }) => ({
                  validator(_, value) {
                    if (!value || getFieldValue('new_password') === value) return Promise.resolve()
                    return Promise.reject(new Error('两次输入的新密码不一致'))
                  },
                }),
              ]}
            >
              <Input.Password prefix={<LockOutlined style={{ color: 'var(--text-muted)' }} />} autoComplete="new-password" size="large" />
            </Form.Item>
            <Button type="primary" htmlType="submit" loading={loading} block size="large">
              修改密码并继续
            </Button>
          </Form>
        </div>
      </div>
    </div>
  )
}

export default ChangePassword
