// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it } from 'vitest'
import MessagingDiagnostics from './MessagingDiagnostics'
;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
it('shows observed zero separately from unavailable and marks incomplete coverage', async () => {
 const container=document.createElement('div');document.body.append(container);const root=createRoot(container)
 try {await act(async()=>root.render(<MessagingDiagnostics series={[{name:'receive',messaging:{incomplete:true,dimensions:[{source:'topic',partition:'0',subscription:'sub',operations:2,sent:0,received:2,throttled:0,redelivered:1,age_ms:{count:1,sum:0,max:0},offset_lag:{count:0,sum:0,max:0},ack_ms:{count:1,sum:2,max:2}}]}}]} />));expect(container.textContent).toContain('0.000 ms (max 0.000 ms; 1 observed)');expect(container.textContent).toContain('Unavailable');expect(container.textContent).toContain('Coverage incomplete');expect(container.textContent).toContain('sub');expect(container.textContent).toContain('2.000 ms')}
 finally {act(()=>root.unmount());container.remove()}
})

it('renders an incomplete placeholder without dimensions', async () => {
 const container=document.createElement('div');const root=createRoot(container)
 try {
  await act(async()=>root.render(<MessagingDiagnostics series={[{name:'legacy',messaging:{incomplete:true,dimensions:[]}}]} />))
  expect(container.textContent).toContain('Coverage incomplete; partition skew is unscored.')
 } finally { act(()=>root.unmount()) }
})
