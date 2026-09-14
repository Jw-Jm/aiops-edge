from pathlib import Path
from PIL import Image, ImageDraw, ImageFont

ROOT = Path('/Users/mssc/Documents/Code/agent/aiops')
OUT = ROOT / 'docs/design/ui-renders'
OUT.mkdir(parents=True, exist_ok=True)

FONT = '/Library/Fonts/Arial Unicode.ttf'
COLORS = {
    'bg': '#F4F6F8', 'surface': '#FFFFFF', 'text': '#17202A', 'muted': '#667281',
    'line': '#D9E0E8', 'blue': '#1768E5', 'blue_soft': '#EAF2FF',
    'green': '#167252', 'green_soft': '#EAF8F2', 'amber': '#A65B00',
    'amber_soft': '#FFF3DC', 'red': '#C93B3B', 'red_soft': '#FFF0F0',
}


def font(size, bold=False):
    return ImageFont.truetype(FONT, size=size, index=0)


def text(draw, xy, value, size=28, color='text', anchor=None, bold=False, spacing=8):
    draw.multiline_text(xy, value, font=font(size, bold), fill=COLORS.get(color, color), anchor=anchor, spacing=spacing)


def box(draw, rect, title, body='', tone='blue', title_size=28, body_size=21):
    x1, y1, x2, y2 = rect
    draw.rounded_rectangle(rect, radius=18, fill=COLORS['surface'], outline=COLORS['line'], width=2)
    draw.rectangle((x1, y1, x1 + 7, y2), fill=COLORS[tone])
    text(draw, (x1 + 28, y1 + 24), title, title_size, 'text', bold=True)
    if body:
        text(draw, (x1 + 28, y1 + 68), body, body_size, 'muted', spacing=6)


def arrow(draw, start, end, color='muted', width=4):
    draw.line((start, end), fill=COLORS[color], width=width)
    ex, ey = end
    draw.polygon([(ex, ey), (ex - 15, ey - 9), (ex - 15, ey + 9)], fill=COLORS[color])


def pill(draw, rect, label, tone='blue'):
    draw.rounded_rectangle(rect, radius=20, fill=COLORS[f'{tone}_soft'])
    x1, y1, x2, y2 = rect
    text(draw, ((x1+x2)//2, (y1+y2)//2), label, 20, tone, anchor='mm', bold=True)


def graph_diagram():
    img = Image.new('RGB', (2400, 1160), COLORS['bg'])
    d = ImageDraw.Draw(img)
    text(d, (90, 55), '知识图谱自动更新与消费闭环', 46, 'text', bold=True)
    text(d, (90, 118), '事实源必须自动对账、版本化和暴露质量状态；页面与 AI 只能消费可追溯的 generation。', 25, 'muted')
    sources = ['Catalog', 'Change', 'Hardware', 'Kubernetes', 'KubeVirt', 'Middleware', 'Network', 'Trace']
    sx, sy, w, h, gap = 90, 205, 252, 62, 18
    for i, source in enumerate(sources):
        tone = 'red' if source == 'Kubernetes' else 'green'
        pill(d, (sx+i*(w+gap), sy, sx+i*(w+gap)+w, sy+h), source, tone)
    stages = [
        ('读取完整事实', '按源配置周期\ncomplete 才允许推进'),
        ('身份归一', 'tenant · cluster\ncanonical UID · alias'),
        ('可靠投影', 'lease · outbox\nbatch mutate · retry'),
        ('对账与版本', 'generation · stale\nreconcile · shadow diff'),
        ('质量发布', 'freshness · coverage\nconflict · schema'),
    ]
    bx, by, bw, bh, bgap = 90, 360, 390, 180, 70
    for i, (title_, body_) in enumerate(stages):
        x = bx + i*(bw+bgap)
        box(d, (x, by, x+bw, by+bh), title_, body_, 'blue')
        if i < len(stages)-1:
            arrow(d, (x+bw+12, by+bh//2), (x+bw+bgap-12, by+bh//2))
    text(d, (90, 605), '产品必须显示什么', 31, 'text', bold=True)
    box(d, (90, 665, 1120, 930), '资源与问题页面', '最后成功时间 · 数据年龄 · 覆盖率 · partial/stale/conflict\n每个关系和根因结论显示 graph generation 与影响说明', 'green', 30, 23)
    box(d, (1280, 665, 2310, 930), '能力与设置中的图谱运维', '8 类源状态 · SLO · Outbox · reconcile run · alias · shadow diff\n提供有审计的重试、全量对账、冲突解决与恢复动作', 'amber', 30, 23)
    arrow(d, (1180, 500), (605, 650), 'blue', 5)
    arrow(d, (1220, 500), (1795, 650), 'blue', 5)
    text(d, (90, 1010), '判定原则', 27, 'text', bold=True)
    text(d, (260, 1010), '自动更新链路存在不等于产品合格。用户必须能判断现在是否新鲜、影响哪些功能、下一步做什么。', 25, 'muted')
    img.save(OUT / '10-knowledge-graph-capability.png', quality=95)


def ai_diagram():
    img = Image.new('RGB', (2400, 1280), COLORS['bg'])
    d = ImageDraw.Draw(img)
    text(d, (90, 50), '智能故障定位能力图', 46, 'text', bold=True)
    text(d, (90, 114), 'AI 负责提出和验证解释，确定性控制面负责权限、执行、验证与恢复。', 25, 'muted')
    stages = [
        ('触发', '告警\n用户问题'), ('冻结 Scope', 'tenant\ncluster\nresource\ntime'),
        ('收集证据', '指标 · 日志\nTrace · 事件\n变更 · 图谱'), ('生成假设', '候选根因\n影响路径'),
        ('因果验证', '时间顺序\n依赖传播\n同类对照\n反事实'), ('输出结论', '证据引用\n结论等级\n缺失证据'),
        ('处置建议', '风险 · 预检\n回滚方案'), ('执行验证', '显式确认\n执行 · 观察\n恢复'),
    ]
    bw, bh, gap, start_x, y = 245, 220, 39, 76, 255
    for i, (title_, body_) in enumerate(stages):
        x = start_x + i*(bw+gap)
        tone = 'amber' if i == 6 else ('green' if i == 7 else 'blue')
        box(d, (x, y, x+bw, y+bh), title_, body_, tone, 25, 19)
        if i < len(stages)-1:
            arrow(d, (x+bw+6, y+bh//2), (x+bw+gap-6, y+bh//2), 'muted', 3)
    text(d, (90, 545), '结论状态机', 30, 'text', bold=True)
    states = [('Unknown', '证据不足'), ('Candidate', '有待验证'), ('Supported', '主要证据支持'), ('Confirmed', '因果校验通过')]
    for i, (state, desc) in enumerate(states):
        x = 90 + i*560
        tone = ['red', 'amber', 'blue', 'green'][i]
        pill(d, (x, 605, x+220, 662), state, tone)
        text(d, (x+240, 633), desc, 20, 'muted', anchor='lm')
        if i < len(states)-1:
            arrow(d, (x+485, 633), (x+540, 633), 'muted', 3)
    box(d, (90, 750, 770, 990), '贯穿护栏', 'Scope 隔离 · 工具白名单 · 超时与预算\n敏感信息脱敏 · Prompt Injection 防护\nAI 禁止自动批准或绕过动作控制面', 'red', 29, 22)
    box(d, (860, 750, 1540, 990), '运行可观测性', '模型与提示词版本 · 工具调用 · evidence_id\n步骤 Trace · 时延 · Token/成本 · 错误与重试\n调查可取消、可恢复、可回放', 'blue', 29, 22)
    box(d, (1630, 750, 2310, 990), '质量评测', '真实故障场景 · Top-1/Top-3 · 证据有效率\n影响范围 · 正确拒答 · 重复一致率\nLLM-as-judge 只能作为补充', 'green', 29, 22)
    text(d, (90, 1075), '合理性结论', 29, 'text', bold=True)
    text(d, (90, 1125), '只有同时满足作用域冻结、证据先于结论、反证可降级、允许不知道、断言可回放、AI 与执行隔离、版本可复现，才可判定设计合理。', 24, 'muted')
    img.save(OUT / '11-ai-fault-localization-capability.png', quality=95)


if __name__ == '__main__':
    graph_diagram()
    ai_diagram()
