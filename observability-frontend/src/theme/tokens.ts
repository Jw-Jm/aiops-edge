import { theme, type ThemeConfig } from 'antd'

export const token = {
  // Product design tokens (kept explicit so CSS and Ant components share one palette).
  colorBg: '#f5f7fa',
  colorSurface: '#ffffff',
  colorSurfaceSecondary: '#f8fafc',
  colorBgLayout: '#f5f7fa',
  colorBgContainer: '#ffffff',
  colorBgElevated: '#ffffff',
  colorText: '#172033',
  colorTextSecondary: '#5b667a',
  colorTextTertiary: '#5b667a',
  colorBorder: '#dde3ea',
  colorBorderSecondary: '#dde3ea',
  colorSplit: '#dde3ea',
  colorPrimary: '#3157d5',
  colorInfo: '#3157d5',
  colorError: '#c9362b',
  colorWarning: '#c46816',
  colorRisk: '#a46f0a',
  colorSuccess: '#18864b',
  colorLink: '#3157d5',
  radiusCard: 8,
  borderRadius: 8,
  borderRadiusLG: 8,
  borderRadiusSM: 6,
  controlHeight: 36,
  minClickTarget: 36,
} as const

export function getThemeConfig(): ThemeConfig {
  return {
    algorithm: theme.defaultAlgorithm,
    token,
    components: {
      Layout: { siderBg: 'transparent', headerBg: 'transparent', bodyBg: 'transparent', triggerBg: 'transparent', triggerColor: '#5b667a' },
      Menu: { itemSelectedBg: '#eef2ff', itemSelectedColor: '#172033', itemColor: '#5b667a' },
      Card: { borderRadiusLG: 8 },
      Table: { headerBg: '#f8fafc', rowHoverBg: '#f8fafc' },
      Button: { primaryShadow: 'none' },
    },
  }
}

export default token
