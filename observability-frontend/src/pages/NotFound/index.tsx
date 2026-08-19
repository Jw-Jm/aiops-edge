import React from 'react'
import { Button, Result } from 'antd'
import { useNavigate } from 'react-router-dom'

export default function NotFound() {
  const navigate = useNavigate()

  return (
    <Result
      status="404"
      title="页面不存在"
      subTitle="当前地址没有对应的运维页面，请返回工作台或从侧栏选择功能。"
      extra={<Button type="primary" onClick={() => navigate('/overview')}>返回工作台</Button>}
    />
  )
}
