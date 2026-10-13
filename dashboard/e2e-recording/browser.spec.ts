import { test, expect } from './support/fixtures'
import { studio, startRecording, stopRecording, snapshot, preview, verifyOutputs, saveRecording, editor, executeSaved, debug, pairAgent } from './support/journey'

for (const launch of ['controller', 'agent'] as const) {
  test.describe(launch, () => {
    test.use({ agentMode: launch === 'agent' })
    test(`${launch}: capture checkout, verify, save and replay`, async ({ page, harness: h }, info) => {
      test.setTimeout(300_000)
      await studio(page,h)
      if (launch === 'agent') await pairAgent(page,h,info)
      const rec = await startRecording(page,h,launch === 'agent')
      try {
        await rec.target.getByLabel('Customer', { exact: true }).fill('recorded-order')
        await rec.target.getByRole('button', { name: 'Checkout', exact: true }).click()
        await expect(rec.target.locator('#result')).toHaveText('Order accepted')
        await page.getByRole('button', { name: 'Pick Verification', exact: true }).click()
        await rec.target.locator('#result').click()
        await expect(page.getByRole('button', { name: 'Pick Verification', exact: true })).toBeVisible()
        await stopRecording(page)
        if (launch === 'controller') {
          const captured = await snapshot(h,rec.id)
          expect(captured.project_id).toBe(h.workspace.projectId)
          expect(captured.steps.map((s: {kind:string})=>s.kind)).toEqual(expect.arrayContaining(['fill','click','assert']))
          expect(captured.har_available).toBe(true)
        }
        h.target.rotateTokens()
        const converted = await preview(page,info)
        expect(converted.script.protocol).toBe('browser')
        await verifyOutputs(page,1)
        const ids = await saveRecording(page)
        expect(ids).toHaveLength(1)
        await editor(page,h,ids[0])
        await debug(page,info)
        expect(h.target.events.filter(e=>e.path==='/checkout'&&e.accepted)).toHaveLength(3)
        await executeSaved(page,h,ids[0],info)
        const orders=h.target.events.filter(e=>e.path==='/checkout').map(e=>JSON.parse(e.body))
        expect(new Set(orders.map(o=>o.csrf_token)).size).toBe(4)
        expect(new Set(orders.map(o=>o.order_id)).size).toBe(4)
      } finally { await rec.browser.close().catch(()=>{}) }
    })
  })
}

test.describe('agent recovery',()=>{
  test.use({agentMode:true})
  test('unavailable and disconnected agent can be connected again',async({page,harness:h},info)=>{
    const {freePort,stopProcess}=await import('./support/process')
    await studio(page,h)
    await page.getByLabel('Agent address',{exact:true}).fill(`http://127.0.0.1:${await freePort()}`)
    await page.getByRole('button',{name:'Connect',exact:true}).click()
    await expect(page.locator('.rec-bridge-status')).toHaveClass(/error/)
    const agent=await pairAgent(page,h,info)
    await stopProcess(agent)
    await page.getByLabel('Start URL scheme',{exact:true}).selectOption('http://')
    await page.getByLabel('Start URL address',{exact:true}).fill(h.target.url.replace('http://',''))
    await page.getByRole('button',{name:'Start browser recording',exact:true}).click()
    await expect(page.locator('.rec-feedback.error')).toBeVisible()
    await page.getByRole('button',{name:'Disconnect',exact:true}).click()
    await expect(page.getByTestId('browser-recorder-unavailable')).toContainText('disabled via APEXLOAD_BROWSER_RECORDER')
    await pairAgent(page,h,info)
    const rec=await startRecording(page,h,true)
    try{
      await rec.target.getByRole('button',{name:'Record event',exact:true}).click()
      await expect.poll(()=>h.target.events.length).toBe(1)
      await stopRecording(page);await preview(page,info);await verifyOutputs(page,1)
    }finally{await rec.browser.close().catch(()=>{})}
  })
})
