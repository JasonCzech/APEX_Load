import {readFile} from 'node:fs/promises'
import {test,expect} from './support/fixtures'
import {studio,startRecording,stopRecording,snapshot,preview,verifyOutputs,saveRecording,editor,debug} from './support/journey'

test('HAR host filtering preserves browser actions and disabled WebSocket payloads stay absent',async({page,harness:h},info)=>{
  await studio(page,h)
  const rec=await startRecording(page,h,false,{excludeHosts:'localhost',ws:false})
  try{
    await rec.target.getByRole('button',{name:'Excluded host event',exact:true}).click()
    await expect.poll(()=>h.target.events.filter(e=>e.query.name==='filtered').length).toBe(1)
    await rec.target.getByRole('button',{name:'Open socket',exact:true}).click()
    await expect(rec.target.locator('#ws-result')).toHaveText('fixture-ready')
    await stopRecording(page)
    const captured=await snapshot(h,rec.id,true)
    expect(captured.websocket_count).toBeGreaterThan(0)
    const downloaded=page.waitForEvent('download')
    await page.getByRole('button',{name:'Download HAR',exact:true}).click()
    const har=JSON.parse(await readFile((await (await downloaded).path())!,'utf8'))
    await info.attach('filtered-recording.har', {body:JSON.stringify(har,null,2),contentType:'application/json'})
    expect(har.log.entries.length).toBeGreaterThan(0)
    expect(har.log.entries.some((e:{request:{url:string}})=>new URL(e.request.url).hostname==='localhost')).toBe(false)
    const converted=await preview(page,info)
    const socketWaits=converted.script.actions.filter((a:{type:string})=>a.type==='browser_ws_wait')
    expect(socketWaits).toHaveLength(1)
    expect(socketWaits[0].params.direction).toBe('open')
    expect(socketWaits[0].params.match).toBeUndefined()
    await verifyOutputs(page,1)
    expect(h.target.events.filter(e=>e.query.name==='filtered')).toHaveLength(2)
  }finally{await rec.browser.close().catch(()=>{})}
})

test('transaction splitting accounts for every output',async({page,harness:h},info)=>{
  await studio(page,h)
  const rec=await startRecording(page,h)
  try{
    for(const name of ['First','Second']){
      await page.getByLabel('Transaction name',{exact:true}).fill(name)
      await page.getByRole('button',{name:'Start transaction',exact:true}).click()
      await rec.target.getByLabel('Customer',{exact:true}).fill(`${name}-customer`)
      await rec.target.getByRole('button',{name:'Record event',exact:true}).click()
      await expect.poll(()=>h.target.events.filter(e=>e.query.value===`${name}-customer`).length).toBe(1)
      // Wait for the debounced application click before closing its transaction.
      await expect.poll(async()=>(await snapshot(h,rec.id)).steps.filter((s:{kind:string})=>s.kind==='click').length).toBe(name==='First'?1:2)
      await page.getByRole('button',{name:`End transaction ${name}`,exact:true}).click()
    }
    await stopRecording(page)
    await page.getByLabel('Split into one script per transaction',{exact:true}).check()
    const split=await preview(page,info)
    expect(split.scripts).toHaveLength(2)
    await verifyOutputs(page,2)
    const ids=await saveRecording(page)
    expect(ids).toHaveLength(2)
    for(const id of ids){
      const response=await h.request.get(`/api/v1/scripts/${id}`);const stored=await response.json()
      expect(stored.project_id).toBe(h.workspace.projectId)
      await editor(page,h,id);await debug(page,info)
    }
    expect(h.target.events.map(e=>e.query.value)).toEqual(['First-customer','Second-customer','First-customer','Second-customer','First-customer','Second-customer'])
  }finally{await rec.browser.close().catch(()=>{})}
})

test('recording verification promotes a working fallback selector and re-verifies the changed conversion',async({page,harness:h},info)=>{
  await studio(page,h)
  const rec=await startRecording(page,h)
  try{
    await rec.target.getByRole('button',{name:'Record event',exact:true}).click()
    await expect.poll(()=>h.target.events.length).toBe(1)
    await stopRecording(page)
    const original=(await snapshot(h,rec.id)).steps.find((s:{kind:string})=>s.kind==='click')
    expect(original.candidates[0].value).toContain('event')
    h.target.renameEventControl()
    await preview(page,info);await verifyOutputs(page,1)
    const suggestion=page.locator('.brp-repair-suggestion').filter({hasText:'Promote self-healed selector'}).first()
    await suggestion.getByRole('button',{name:'Accept',exact:true}).click()
    await expect.poll(async()=>(await snapshot(h,rec.id)).steps.find((s:{kind:string})=>s.kind==='click')?.candidates[0].value).not.toBe(original.candidates[0].value)
    await expect(page.getByText('Verification passed for this conversion',{exact:true})).toBeVisible({timeout:60_000})
    const ids=await saveRecording(page);await editor(page,h,ids[0]);await debug(page,info)
    expect(h.target.events).toHaveLength(4)
  }finally{await rec.browser.close().catch(()=>{})}
})

test('parameterization review applies a recorded input and saves it into the selected folder',async({page,harness:h},info)=>{
  await studio(page,h)
  const rec=await startRecording(page,h)
  try{
    await rec.target.getByLabel('Customer',{exact:true}).fill('review-customer')
    await rec.target.getByRole('button',{name:'Record event',exact:true}).click()
    await expect.poll(()=>h.target.events.length).toBe(1)
    await stopRecording(page)
  await page.getByPlaceholder('Imported API scripts').fill('Recorded journeys')
  await page.getByRole('button',{name:'Create and select folder',exact:true}).click()
  const destination=page.getByLabel('Destination folder for saved scripts',{exact:true})
  await expect(destination).not.toHaveValue('')
  const folder=await destination.inputValue()
    await preview(page,info)
    const review=page.locator('.paramize-review')
    await review.locator('input[type=checkbox]').first().check()
    await review.getByRole('button',{name:/^Apply Selected/}).click()
    await expect(review.locator('.paramize-applied-badge')).toContainText('Applied 3 change(s)')
    await verifyOutputs(page,1)
    const [id]=await saveRecording(page)
    const response=await h.request.get(`/api/v1/scripts/${id}`),stored=await response.json()
    expect(stored.project_id).toBe(h.workspace.projectId);expect(stored.folder_id).toBe(folder)
    expect(stored.script.parameters).toHaveLength(1)
    await editor(page,h,id)
    await debug(page,info)
    expect(h.target.events.map(e=>e.query.value)).toEqual(['review-customer','review-customer','review-customer'])
  }finally{await rec.browser.close().catch(()=>{})}
})
