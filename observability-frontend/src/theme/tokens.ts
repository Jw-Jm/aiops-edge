import { theme, type ThemeConfig } from 'antd'

export const operationsPalette = {
  canvas: '#F5F7FA',
  surface: '#FFFFFF',
  surfaceSubtle: '#F8FAFC',
  text: '#172033',
  textSecondary: '#5B667A',
  border: '#DDE3EA',
  interaction: '#3157D5',
  critical: '#C9362B',
  degraded: '#C46816',
  risk: '#A46F0A',
  healthy: '#18864B',
} as const

export const token = {
  // Product design tokens (kept explicit so CSS and Ant components share one palette).
  colorBg: operationsPalette.canvas,
  colorSurface: operationsPalette.surface,
  colorSurfaceSecondary: operationsPalette.surfaceSubtle,
  colorBgLayout: operationsPalette.canvas,
  colorBgContainer: operationsPalette.surface,
  colorBgElevated: operationsPalette.surface,
  colorText: operationsPalette.text,
  colorTextSecondary: operationsPalette.textSecondary,
  colorTextTertiary: operationsPalette.textSecondary,
  colorBorder: operationsPalette.border,
  colorBorderSecondary: operationsPalette.border,
  colorSplit: operationsPalette.border,
  colorPrimary: operationsPalette.interaction.toLowerCase(),
  colorInfo: operationsPalette.interaction.toLowerCase(),
  colorError: operationsPalette.critical.toLowerCase(),
  colorWarning: operationsPalette.degraded.toLowerCase(),
  colorRisk: operationsPalette.risk.toLowerCase(),
  colorSuccess: operationsPalette.healthy.toLowerCase(),
  colorLink: operationsPalette.interaction.toLowerCase(),
  radiusCard: 8,
  borderRadius: 8,
  borderRadiusLG: 8,
  borderRadiusSM: 6,
  controlHeight: 36,
  minClickTarget: 36,
  tableRowHeight: 44,
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
