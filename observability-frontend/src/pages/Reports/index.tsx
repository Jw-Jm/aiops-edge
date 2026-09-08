import React from 'react'
import { PageHeader, Breadcrumb } from '../../components/ui/PageKit'
import Report from '../report/Report'

const Reports: React.FC = () => (
  <div>
    <Breadcrumb items={[{ t: '报告' }, { t: '报告中心' }]} />
    <PageHeader title="报告中心" desc="集中查看调查、巡检与复盘报告，沉淀为可复用知识" />
    <Report />
  </div>
)

export default Reports
