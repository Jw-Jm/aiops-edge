import React, { useEffect, useRef, useState } from 'react'
import { Card, Alert, Space, Button, Tag, Typography } from 'antd'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import '@xterm/xterm/css/xterm.css'
import { ReloadOutlined, SafetyCertificateOutlined } from '@ant-design/icons'

const { Text } = Typography

const Shell: React.FC = () => {
  const termRef = useRef<HTMLDivElement>(null)
  const wsRef = useRef<WebSocket | null>(null)
  const termInstance = useRef<Terminal | null>(null)
  const resizeHandler = useRef<(() => void) | null>(null)
  const [connected, setConnected] = useState(false)
  const [error, setError] = useState('')

  // 清理上一次连接创建的终端/resize 监听/websocket，避免重复监听与内存泄漏
  const cleanup = () => {
    if (resizeHandler.current) {
      window.removeEventListener('resize', resizeHandler.current)
      resizeHandler.current = null
    }
    if (wsRef.current) {
      wsRef.current.close()
      wsRef.current = null
    }
    if (termInstance.current) {
      try { termInstance.current.dispose() } catch { /* ignore */ }
      termInstance.current = null
    }
  }

  const connect = () => {
    if (!termRef.current) return
    setError('')

    cleanup()

    // 清空并重建终端
    termRef.current.innerHTML = ''
    const term = new Terminal({
      cursorBlink: true,
      fontSize: 13,
      theme: { background: '#1a1a1a', foreground: '#e8e8e8' },
      convertEol: true,
    })
    termInstance.current = term
    const fit = new FitAddon()
    term.loadAddon(fit)
    term.open(termRef.current)
    fit.fit()
    term.writeln('AIOps WebShell — 只读命令优先，写操作需白名单')
    term.writeln('输入命令后回车执行，按 Ctrl+C 停止当前命令')
    term.write('\r\n$ ')

    const proto = location.protocol === 'https:' ? 'wss' : 'ws'
    const ws = new WebSocket(`${proto}://${location.host}/api/v1/shell/ws`)
    wsRef.current = ws

    ws.onopen = () => { setConnected(true); term.writeln('（已连接）') }
    ws.onmessage = (e) => {
      term.write(typeof e.data === 'string' ? e.data : '')
      term.write('\r\n$ ')
    }
    ws.onclose = () => { setConnected(false); term.writeln('\r\n（连接已断开）') }
    ws.onerror = () => { setConnected(false); setError('WebSocket 连接失败'); term.writeln('\r\n（连接错误）') }

    term.onData((data) => {
      if (ws.readyState === WebSocket.OPEN) ws.send(data)
    })
    const onResize = () => fit.fit()
    resizeHandler.current = onResize
    window.addEventListener('resize', onResize)
  }

  useEffect(() => {
    connect()
    return () => cleanup()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  return (
    <Card
      title="WebShell 终端"
      extra={<Space>
        {connected ? <Tag color="green">已连接</Tag> : <Tag color="red">未连接</Tag>}
        <Button size="small" icon={<ReloadOutlined />} onClick={connect}>重连</Button>
      </Space>}
    >
      <Alert type="info" showIcon icon={<SafetyCertificateOutlined />} style={{ marginBottom: 12 }}
        message={<Text>安全模式：仅执行白名单内命令（kubectl 只读/受控写操作）。危险命令（rm -rf、sudo、shutdown 等）会被拦截。</Text>} />
      {error && <Alert type="warning" message={error} style={{ marginBottom: 12 }} closable onClose={() => setError('')} />}
      <div ref={termRef} style={{ background: '#1a1a1a', borderRadius: 8, height: 460, padding: 8 }} />
    </Card>
  )
}

export default Shell
