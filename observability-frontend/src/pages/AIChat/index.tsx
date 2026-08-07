import React, { useState, useRef, useEffect } from 'react';
import {
  Card,
  Input,
  Button,
  Select,
  Typography,
  Space,
  Spin,
  Collapse,
  Tag,
  Alert,
  List, Tooltip, Popconfirm, Empty, Badge,
} from "antd";
import {
  SendOutlined,
  RobotOutlined,
  UserOutlined,
  LoadingOutlined,
  BulbOutlined,
  WarningOutlined,
  SettingOutlined,
  DeleteOutlined, PlusOutlined, HistoryOutlined, MessageOutlined,
} from '@ant-design/icons';
import api from "../../api/client";
import { getLLMSettings } from '../../api/client';
import { useNavigate } from 'react-router-dom';
import { fmtLocalTime } from '../../utils/date';

interface SessionInfo {
  session_id: string;
  preview: string;
  intent: string;
  created_at: string;
}

const { Title, Text } = Typography;
const { TextArea } = Input;
const { Panel } = Collapse;

interface ChatMessage {
  id: string;
  role: 'user' | 'assistant';
  content: string;
  timestamp: string;
  thinking?: string;
  expert?: string;
}

const EXPERT_OPTIONS = [
  { value: 'inspection', label: '巡检' },
  { value: 'diagnosis', label: '诊断' },
  { value: 'query', label: '问数' },
  { value: 'ops', label: '运维' },
];

const AIChat: React.FC = () => {
  const navigate = useNavigate();
  const [messages, setMessages] = useState<ChatMessage[]>([]);
  const [input, setInput] = useState('');
  const [loading, setLoading] = useState(false);
  const [progressText, setProgressText] = useState('');
  const [toolCards, setToolCards] = useState<Array<{ tool_call_id: string; name: string; status: string; result?: string }>>([]);
  const [expert, setExpert] = useState('diagnosis');
  const [apiKeyConfigured, setApiKeyConfigured] = useState<boolean | null>(null);
  const [sessions, setSessions] = useState<SessionInfo[]>([]);
  const [activeSession, setActiveSession] = useState('');
  const [sessionsLoading, setSessionsLoading] = useState(false);
  const messagesEndRef = useRef<HTMLDivElement>(null);

  // Check API key status on mount
  useEffect(() => {
    const checkApiKey = async () => {
      try {
        const res = await getLLMSettings();
        const data = res.data?.data || res.data || {};
        if (data && (data.api_key || data.apiKey)) {
          setApiKeyConfigured(true);
        } else {
          setApiKeyConfigured(false);
        }
      } catch {
        setApiKeyConfigured(false);
      }
    };
    checkApiKey();
    loadSessions();
  }, []);

  useEffect(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' });
  }, [messages]);

  // ── Sessions ──

  const loadSessions = async () => {
    setSessionsLoading(true);
    try { const r = await api.get('/ai/sessions'); setSessions(r.data?.sessions || []); } catch {}
    setSessionsLoading(false);
  };

  const newSession = () => { setMessages([]); setActiveSession(''); };

  const loadSession = async (sid: string) => {
    try {
      const r = await api.get(`/ai/session/${sid}`);
      const d = r.data; const msgs: ChatMessage[] = [];
      (d?.messages || []).forEach((m: any, i: number) => {
        if (m.role === 'user') msgs.push({ id: `s-${sid}-${i}`, role: 'user', content: m.content, timestamp: d.created_at || '', expert: d.intent });
        else if (m.role === 'assistant') msgs.push({ id: `s-${sid}-${i}`, role: 'assistant', content: m.content, timestamp: d.created_at || '' });
      });
      if (msgs.length === 0 && d?.user_message && d?.final_response) {
        msgs.push({ id: `s-${sid}-u`, role: 'user', content: d.user_message, timestamp: d.created_at || '', expert: d.intent });
        msgs.push({ id: `s-${sid}-a`, role: 'assistant', content: d.final_response, timestamp: d.created_at || '' });
      }
      if (msgs.length > 0) { setMessages(msgs); setActiveSession(sid); }
    } catch {}
  };

  const deleteSession = async (sid: string, e?: React.MouseEvent) => {
    e?.stopPropagation();
    try { await api.delete(`/ai/session/${sid}`); setSessions(p => p.filter(s => s.session_id !== sid)); if (activeSession === sid) newSession(); } catch {}
  };

  const handleSend = async () => {
    const text = input.trim();
    if (!text) return;

    const userMsg: ChatMessage = {
      id: `msg-${Date.now()}`,
      role: 'user',
      content: text,
      timestamp: new Date().toISOString(),
      expert,
    };

    setMessages((prev) => [...prev, userMsg, { id: `ai-${Date.now()}`, role: 'assistant', content: '', timestamp: new Date().toISOString() }]);
    setInput('');
    setLoading(true);
    setProgressText('分析开始...');
    setToolCards([]);

    try {
      const baseURL = api.defaults.baseURL || '/api/v1';
      const sessionId = activeSession || Date.now().toString(36);
      const controller = new AbortController();
      const timeoutId = setTimeout(() => controller.abort(), 120000);

      const resp = await fetch(`${baseURL}/ai/chat`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', 'X-Tenant-ID': 'default', Authorization: api.defaults.headers.common['Authorization'] as string || '' },
        body: JSON.stringify({ intent: expert, service: '', message: text, stream: true, session_id: sessionId }),
        signal: controller.signal,
      });
      clearTimeout(timeoutId);

      if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
      const reader = resp.body?.getReader();
      if (!reader) throw new Error('No stream');
      const decoder = new TextDecoder(); let buf = ''; let fullText = '';
      let toolCardsLocal: Array<{ tool_call_id: string; name: string; status: string; result?: string }> = [];
      const dispatchEvent = (evName: string, ev: any) => {
        switch (evName) {
          case 'progress': if (ev.text) setProgressText(ev.text); break;
          case 'chunk': if (ev.text) fullText += ev.text; break;
          case 'assistant': fullText = ev.content ?? ev.text ?? fullText; break;
          case 'tool_start': toolCardsLocal.push({ tool_call_id: ev.tool_call_id, name: ev.name, status: 'pending' }); break;
          case 'tool_end':
            toolCardsLocal = toolCardsLocal.map((t) => (t.tool_call_id === ev.tool_call_id ? { ...t, status: ev.status, result: ev.result } : t));
            break;
          case 'approval_pending': break;
          case 'done':
            if (!fullText) fullText = ev.text ?? ev.assistant_message?.content ?? '';
            setToolCards(toolCardsLocal);
            break;
          case 'error': fullText = `⚠️ ${ev.error ?? ev.text ?? ''}`; setToolCards(toolCardsLocal); break;
          default: break;
        }
      };
      while (true) {
        const { done, value } = await reader.read();
        if (done) break;
        buf += decoder.decode(value, { stream: true });
        // 按 \n\n 空行切出完整帧
        const frames = buf.split('\n\n'); buf = frames.pop() || '';
        for (const frame of frames) {
          if (!frame.trim()) continue;
          let evName = 'message';
          const dataLines: string[] = [];
          for (const l of frame.split('\n')) {
            if (l.startsWith('event: ')) evName = l.slice(7).trim();
            else if (l.startsWith('data: ')) dataLines.push(l.slice(6));
          }
          if (dataLines.length === 0) continue;
          try { dispatchEvent(evName, JSON.parse(dataLines.join('\n'))); } catch {}
        }
      }
      const aiText = fullText || 'LLM 分析未返回结果，请检查配置后重试。';
      setMessages((prev) => {
        const updated = [...prev];
        const last = updated[updated.length - 1];
        if (last && last.role === 'assistant') {
          updated[updated.length - 1] = { ...last, content: aiText };
        }
        return updated;
      });
      if (fullText) { setActiveSession(sessionId); loadSessions(); }
    } catch (err: any) {
      const errMsg = err.name === 'AbortError' ? '⏱️ 请求超时 (120s)，请稍后重试' : `❌ 请求失败\n\n${err?.message || ''}`;
      const errorMsg: ChatMessage = { id: `msg-${Date.now()}-ai`, role: 'assistant', content: errMsg, timestamp: new Date().toISOString() };
      setMessages((prev) => [...prev, errorMsg]);
    } finally {
      setLoading(false);
      setProgressText('');
    }
  };

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault();
      handleSend();
    }
  };

  const getExpertLabel = (value: string) => {
    return EXPERT_OPTIONS.find((o) => o.value === value)?.label || value;
  };

  return (
    <div style={{ height: 'calc(100vh - 140px)', display: 'flex', gap: 16 }}>
      <Card size='small' title={<span><HistoryOutlined /> 历史记录</span>}
        extra={<Tooltip title='新建'><Button type='text' size='small' icon={<PlusOutlined />} onClick={newSession} /></Tooltip>}
        style={{ width: 240, flexShrink: 0, display: 'flex', flexDirection: 'column' }}
        styles={{ body: { flex: 1, overflow: 'auto', padding: '8px 12px' } }}
      >
        {sessions.length === 0
          ? <Empty description='暂无' image={Empty.PRESENTED_IMAGE_SIMPLE} style={{ marginTop: 40 }} />
          : <List loading={sessionsLoading} dataSource={sessions} size='small' renderItem={s => (
              <List.Item onClick={() => loadSession(s.session_id)}
                style={{ cursor: 'pointer', padding: '8px 4px', borderRadius: 6, background: activeSession === s.session_id ? '#e6f4ff' : 'transparent', marginBottom: 2 }}
                actions={[
                  <Popconfirm key='del' title='删除？' onConfirm={(e: any) => { e?.stopPropagation(); deleteSession(s.session_id); }} onCancel={(e: any) => e?.stopPropagation()}>
                    <Button type='text' size='small' danger icon={<DeleteOutlined />} onClick={(e: any) => e.stopPropagation()} />
                  </Popconfirm>,
                ]}>
                <List.Item.Meta avatar={<MessageOutlined style={{ color: '#1677ff' }} />}
                  title={<Text ellipsis style={{ fontSize: 13 }}>{s.preview || s.session_id}</Text>}
                  description={<Space size={4}>{s.intent && <Tag style={{ fontSize: 10 }}>{s.intent}</Tag>}<Text style={{ fontSize: 10, color: '#bbb' }}>{s.session_id.slice(0, 6)}</Text></Space>} />
              </List.Item>
            )} />}
      </Card>
      <div style={{ flex: 1, display: 'flex', flexDirection: 'column' }}>
      {/* API Key Warning Banner */}
      {apiKeyConfigured === false && (
        <Alert
          message="LLM API Key 未配置"
          description={
            <span>
              请先配置 LLM API Key 以启用 AI 诊断功能。
              <Button
                type="link"
                size="small"
                icon={<SettingOutlined />}
                onClick={() => navigate('/settings')}
                style={{ padding: '0 4px' }}
              >
                前往设置
              </Button>
            </span>
          }
          type="warning"
          showIcon
          icon={<WarningOutlined />}
          style={{ marginBottom: 16 }}
          closable
        />
      )}

      {/* Header */}
      <div
        style={{
          display: 'flex',
          justifyContent: 'space-between',
          alignItems: 'center',
          marginBottom: 16,
        }}
      >
        <Title level={3} style={{ margin: 0 }}>
          <RobotOutlined /> AI Assistant
        </Title>
        <Select
          value={expert}
          onChange={setExpert}
          options={EXPERT_OPTIONS}
          style={{ width: 120 }}
          size="middle"
        />
      </div>

      {/* Messages */}
      <Card
        style={{
          flex: 1,
          overflow: 'auto',
          marginBottom: 16,
          backgroundColor: '#f5f5f5',
        }}
        bodyStyle={{ padding: 16 }}
      >
        {messages.length === 0 && (
          <div
            style={{
              textAlign: 'center',
              padding: 80,
              color: '#999',
            }}
          >
            <RobotOutlined style={{ fontSize: 48, marginBottom: 16 }} />
            <br />
            <Text type="secondary" style={{ fontSize: 16 }}>
              选择一个专家模式，开始对话
            </Text>
            <br />
            <Space style={{ marginTop: 16 }}>
              {EXPERT_OPTIONS.map((opt) => (
                <Button
                  key={opt.value}
                  type={expert === opt.value ? 'primary' : 'default'}
                  size="small"
                  onClick={() => setExpert(opt.value)}
                >
                  {opt.label}
                </Button>
              ))}
            </Space>
          </div>
        )}

        {messages.map((msg) => (
          <div
            key={msg.id}
            style={{
              display: 'flex',
              justifyContent: msg.role === 'user' ? 'flex-end' : 'flex-start',
              marginBottom: 16,
            }}
          >
            <div
              style={{
                maxWidth: '80%',
                minWidth: 200,
              }}
            >
              <div
                style={{
                  display: 'flex',
                  alignItems: 'center',
                  gap: 8,
                  marginBottom: 4,
                  justifyContent: msg.role === 'user' ? 'flex-end' : 'flex-start',
                }}
              >
                {msg.role === 'assistant' && <RobotOutlined style={{ color: '#1677ff' }} />}
                <Text style={{ fontSize: 12, color: '#999' }}>
                  {msg.role === 'user' ? 'You' : 'AI Assistant'}
                  {msg.expert && msg.role === 'user' && (
                    <Tag style={{ marginLeft: 4 }} color="blue">
                      {getExpertLabel(msg.expert)}
                    </Tag>
                  )}
                </Text>
                <Text style={{ fontSize: 11, color: '#ccc' }}>
                  {fmtLocalTime(msg.timestamp, '', 'HH:mm:ss')}
                </Text>
                {msg.role === 'user' && <UserOutlined style={{ color: '#52c41a' }} />}
              </div>

              <div
                style={{
                  backgroundColor: msg.role === 'user' ? '#1677ff' : '#fff',
                  color: msg.role === 'user' ? '#fff' : '#333',
                  padding: '12px 16px',
                  borderRadius: 12,
                  borderTopRightRadius: msg.role === 'user' ? 4 : 12,
                  borderTopLeftRadius: msg.role === 'assistant' ? 4 : 12,
                  boxShadow: '0 1px 3px rgba(0,0,0,0.1)',
                  wordBreak: 'break-word',
                }}
              >
                {msg.thinking && (
                  <Collapse
                    ghost
                    size="small"
                    style={{ marginBottom: 8 }}
                    expandIconPosition="end"
                  >
                    <Panel
                      header={
                        <Text style={{ fontSize: 12, color: msg.role === 'user' ? 'rgba(255,255,255,0.7)' : '#999' }}>
                          <BulbOutlined /> 思考过程
                        </Text>
                      }
                      key="thinking"
                    >
                      <Text
                        style={{
                          fontSize: 12,
                          color: msg.role === 'user' ? 'rgba(255,255,255,0.8)' : '#666',
                          whiteSpace: 'pre-wrap',
                        }}
                      >
                        {msg.thinking}
                      </Text>
                    </Panel>
                  </Collapse>
                )}

                <div style={{ whiteSpace: 'pre-wrap', lineHeight: 1.6 }}>
                  {msg.content.split('\n').map((line, i) => {
                    const boldRegex = /\*\*(.*?)\*\*/g;
                    const parts: React.ReactNode[] = [];
                    let lastIndex = 0;
                    let match: RegExpExecArray | null;

                    const regex = new RegExp(boldRegex);
                    while ((match = regex.exec(line)) !== null) {
                      if (match.index > lastIndex) {
                        parts.push(line.substring(lastIndex, match.index));
                      }
                      parts.push(<strong key={`b-${i}-${match.index}`}>{match[1]}</strong>);
                      lastIndex = match.index + match[0].length;
                    }
                    if (lastIndex < line.length) {
                      parts.push(line.substring(lastIndex));
                    }

                    return (
                      <span key={i}>
                        {parts.length > 0 ? parts : line}
                        {i < msg.content.split('\n').length - 1 && <br />}
                      </span>
                    );
                  })}
                </div>
              </div>
            </div>
          </div>
        ))}

        {toolCards.length > 0 && (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 6, marginBottom: 12 }}>
            {toolCards.map((t) => (
              <div
                key={t.tool_call_id}
                style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '6px 12px', background: 'var(--surface-2)', borderRadius: 8 }}
              >
                <span style={{ fontSize: 12, color: 'var(--text)' }}>⚙️ {t.name}</span>
                <span style={{ fontSize: 11, color: t.status === 'success' ? '#22c55e' : t.status === 'pending' ? 'var(--text-muted)' : '#ef4444' }}>{t.status}</span>
                {t.result && <span style={{ fontSize: 10, color: 'var(--text-muted)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', maxWidth: 300 }}>{String(t.result).slice(0, 80)}</span>}
              </div>
            ))}
          </div>
        )}

        {loading && (
          <div style={{ display: 'flex', justifyContent: 'flex-start', marginBottom: 16 }}>
            <div
              style={{
                backgroundColor: '#fff',
                padding: '12px 20px',
                borderRadius: 12,
                borderTopLeftRadius: 4,
                boxShadow: '0 1px 3px rgba(0,0,0,0.1)',
              }}
            >
              <Space>
                <Spin indicator={<LoadingOutlined style={{ fontSize: 16 }} spin />} />
                <Text type="secondary">{progressText || 'AI 正在思考...'}</Text>
              </Space>
            </div>
          </div>
        )}

        <div ref={messagesEndRef} />
      </Card>

      {/* Input area */}
      <Card bodyStyle={{ padding: '12px 16px' }}>
        <div style={{ display: 'flex', gap: 12, alignItems: 'flex-end' }}>
          <TextArea
            value={input}
            onChange={(e) => setInput(e.target.value)}
            onKeyDown={handleKeyDown}
            placeholder="输入您的问题... (Shift+Enter 换行，Enter 发送)"
            autoSize={{ minRows: 1, maxRows: 4 }}
            disabled={loading}
            style={{ flex: 1 }}
          />
          <Button
            type="primary"
            icon={<SendOutlined />}
            onClick={handleSend}
            loading={loading}
            disabled={!input.trim()}
            size="large"
          >
            发送
          </Button>
        </div>
      </Card>
      </div>
    </div>
  );
};

export default AIChat;
