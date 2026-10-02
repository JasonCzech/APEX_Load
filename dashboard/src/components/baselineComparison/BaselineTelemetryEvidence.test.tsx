// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { it, expect } from 'vitest'
import BaselineTelemetryEvidence from './BaselineTelemetryEvidence'
it('preserves measured zero, missing metrics and legacy snapshots',async()=>{
 const node=document.createElement('div');const root=createRoot(node)
 try{
 await act(async()=>root.render(<BaselineTelemetryEvidence evidence={[{run_id:'current',metric_intervals:1,retained_intervals:2,health_intervals:1,health_coverage_pct:50,breached_interval_pct:0,dropped_results:0,max_sample_age_ms:0,warnings:['Partial classification']},{run_id:'legacy',metric_intervals:0,retained_intervals:1,health_intervals:0,warnings:['Missing evidence']}]} />))
 expect(node.textContent).toContain('0.0%');expect(node.textContent).toContain('0 ms');expect(node.textContent).toContain('Unavailable');expect(node.textContent).toContain('Partial classification')
 expect(node.querySelector('a')?.getAttribute('href')).toContain('current')
 await act(async()=>root.render(<BaselineTelemetryEvidence />));expect(node.textContent).toContain('No retained telemetry comparison evidence')
 }finally{await act(async()=>root.unmount())}
})
