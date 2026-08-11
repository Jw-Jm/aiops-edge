// =====================================================================
//  设计令牌 v3.0 —— 纯亮色极简（白天友好）· 冷静靛蓝单主色
//  AIOps 可观测平台 · 符合运维用户习惯：信息密度优先、专注扫视
// =====================================================================
import { theme, type ThemeConfig } from 'antd'

export const token: Record<string, any> = {
  colorPrimary: '#2f54eb',
  colorInfo: '#2f54eb',
  colorSuccess: '#16a34a',
  colorWarning: '#d97706',
  colorError: '#dc2626',
  // 文字
  colorText: '#1f2d3d',
  colorTextSecondary: '#52606d',
  colorTextTertiary: '#7a8794',
  colorTextQuaternary: '#a3aebe',
  // 表面 / 背景
  colorBgLayout: '#f4f6f9',
  colorBgContainer: '#ffffff',
  colorBgElevated: '#ffffff',
  colorBgSpotlight: '#eef2f7',
  // 边框
  colorBorder: '#e5e9f0',
  colorBorderSecondary: '#eef2f7',
  colorSplit: '#e5e9f0',
  // 品牌相关
  colorPrimaryHover: '#1d39c4',
  colorPrimaryActive: '#2139a1',
  colorPrimaryBg: 'rgba(47,84,235,.08)',
  colorPrimaryBgHover: 'rgba(47,84,235,.14)',
  colorLink: '#2f54eb',
  colorLinkHover: '#1d39c4',
  // 几何 / 字体
  borderRadius: 8,
  borderRadiusLG: 12,
  borderRadiusSM: 6,
  fontSize: 13,
  controlHeight: 36,
  fontFamily: '"Inter", -apple-system, BlinkMacSystemFont, "Segoe UI", "PingFang SC", "Microsoft YaHei", "Hiragino Sans GB", sans-serif',
}

export function getThemeConfig(): ThemeConfig {
  return {
    algorithm: theme.defaultAlgorithm,
    token,
    components: {
      Layout: { siderBg: 'transparent', headerBg: 'transparent', bodyBg: 'transparent', triggerBg: 'transparent', triggerColor: '#7a8794' },
      Menu: {
        itemSelectedBg: 'rgba(47,84,235,.10)',
        itemSelectedColor: '#1f2d3d',
        itemColor: '#52606d',
      },
      Card: { borderRadiusLG: 12 },
      Table: { headerBg: 'rgba(0,0,0,.02)', rowHoverBg: 'rgba(47,84,235,.04)' },
      Button: { primaryShadow: '0 2px 6px rgba(47,84,235,.20)' },
    },
  }
}
