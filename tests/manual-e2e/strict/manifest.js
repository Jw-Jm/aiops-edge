const PAGE_IDS = [
  'PF-PAGE-001', 'PF-PAGE-002', 'PF-PAGE-003', 'PF-PAGE-004', 'PF-PAGE-005', 'PF-PAGE-006',
  'PF-PAGE-007', 'PF-PAGE-008', 'PF-PAGE-009', 'PF-PAGE-010', 'PF-PAGE-011', 'PF-PAGE-012',
  'PF-PAGE-013', 'PF-PAGE-014', 'PF-PAGE-015', 'PF-PAGE-016', 'PF-PAGE-017', 'PF-PAGE-018',
  'PF-PAGE-019', 'PF-PAGE-020', 'PF-PAGE-021', 'PF-PAGE-022', 'PF-PAGE-023', 'PF-PAGE-024',
  'PF-PAGE-025', 'PF-PAGE-026',
]
const UI_IDS = ['PF-UI-001', 'PF-UI-002', 'PF-UI-003', 'PF-UI-004', 'PF-UI-005', 'PF-UI-006']
const LOGIC_IDS = [
  'PF-LOGIC-001', 'PF-LOGIC-002', 'PF-LOGIC-003', 'PF-LOGIC-004', 'PF-LOGIC-005', 'PF-LOGIC-006',
  'PF-LOGIC-007', 'PF-LOGIC-008', 'PF-LOGIC-009', 'PF-LOGIC-010', 'PF-LOGIC-011', 'PF-LOGIC-012',
  'PF-LOGIC-013', 'PF-LOGIC-014', 'PF-LOGIC-015',
]
const DATA_IDS = [
  'PF-DATA-001', 'PF-DATA-002', 'PF-DATA-003', 'PF-DATA-004', 'PF-DATA-005', 'PF-DATA-006',
  'PF-DATA-007', 'PF-DATA-008', 'PF-DATA-009', 'PF-DATA-010', 'PF-DATA-011', 'PF-DATA-012',
]
const AI_IDS = [
  'PF-AI-001', 'PF-AI-002', 'PF-AI-003', 'PF-AI-004', 'PF-AI-005', 'PF-AI-006', 'PF-AI-007',
  'PF-AI-008', 'PF-AI-009', 'PF-AI-010',
]
const BACKEND_IDS = [
  'PF-BE-001', 'PF-BE-002', 'PF-BE-003', 'PF-BE-004', 'PF-BE-005', 'PF-BE-006', 'PF-BE-007',
  'PF-BE-008', 'PF-BE-009', 'PF-BE-010',
]
const FLOW_IDS = [
  'PF-FLOW-001', 'PF-FLOW-002', 'PF-FLOW-003', 'PF-FLOW-004', 'PF-FLOW-005', 'PF-FLOW-006',
  'PF-FLOW-007', 'PF-FLOW-008',
]

const TELEMETRY_IDS = new Set([
  'PF-PAGE-003', 'PF-PAGE-004', 'PF-PAGE-006', 'PF-PAGE-007', 'PF-PAGE-010', 'PF-PAGE-011',
  'PF-LOGIC-003', 'PF-LOGIC-004', 'PF-LOGIC-005', 'PF-LOGIC-006', 'PF-LOGIC-007', 'PF-LOGIC-008',
  'PF-LOGIC-009', 'PF-LOGIC-010', 'PF-LOGIC-011', 'PF-DATA-001', 'PF-DATA-002', 'PF-DATA-003',
  'PF-DATA-004', 'PF-DATA-005', 'PF-DATA-006', 'PF-DATA-007', 'PF-DATA-009', 'PF-DATA-010',
  'PF-DATA-011', 'PF-DATA-012', 'PF-AI-001', 'PF-AI-002', 'PF-AI-003', 'PF-AI-004', 'PF-AI-005',
  'PF-AI-006', 'PF-AI-007', 'PF-AI-008', 'PF-AI-009', 'PF-AI-010', 'PF-BE-002', 'PF-BE-003',
  'PF-BE-004', 'PF-BE-005', 'PF-BE-006', 'PF-BE-008', 'PF-BE-009', 'PF-FLOW-001', 'PF-FLOW-002',
  'PF-FLOW-003', 'PF-FLOW-004', 'PF-FLOW-005', 'PF-FLOW-007', 'PF-FLOW-008',
])
const EMPTY_ALLOWED_IDS = new Set(['PF-DATA-008'])
const TWO_CLUSTER_IDS = new Set(['PF-LOGIC-002', 'PF-BE-010', 'PF-FLOW-007'])
const ACTION_IDS = new Set(['PF-LOGIC-009', 'PF-LOGIC-010', 'PF-AI-008', 'PF-AI-009', 'PF-FLOW-005'])
const REAL_LLM_IDS = new Set([
  'PF-LOGIC-005', 'PF-LOGIC-006', 'PF-LOGIC-007', 'PF-LOGIC-008', 'PF-LOGIC-009', 'PF-LOGIC-011',
  ...AI_IDS, 'PF-BE-006', 'PF-FLOW-002', 'PF-FLOW-003', 'PF-FLOW-004', 'PF-FLOW-008',
])

function makeSpec(id) {
  const category = id.split('-')[1].toLowerCase()
  return {
    id,
    category,
    requires: {
      telemetry: TELEMETRY_IDS.has(id),
      twoClusters: TWO_CLUSTER_IDS.has(id),
      realLLM: REAL_LLM_IDS.has(id),
      action: ACTION_IDS.has(id),
      knowledgePersistence: ['PF-LOGIC-011', 'PF-AI-010', 'PF-BE-006', 'PF-FLOW-008'].includes(id),
    },
    allowEmpty: EMPTY_ALLOWED_IDS.has(id),
  }
}

const MANIFEST = [...PAGE_IDS, ...UI_IDS, ...LOGIC_IDS, ...DATA_IDS, ...AI_IDS, ...BACKEND_IDS, ...FLOW_IDS].map(makeSpec)

if (MANIFEST.length !== 87) throw new Error(`manual manifest must contain 87 cases, got ${MANIFEST.length}`)

module.exports = { MANIFEST, makeSpec }
