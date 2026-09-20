import {test,expect} from './support/fixtures'
import {studio,startRecording,stopRecording,preview,verifyOutputs,saveRecording,editor,debug} from './support/journey'

test('conversion preview is ephemeral and changing options invalidates verification before unverified save',async({page,harness:h},info)=>{
  await studio(page,h)
  const rec=await startRecording(page,h)
  try{
    await rec.target.getByLabel('Customer',{exact:true}).fill('review-customer')
    await rec.target.getByRole('button',{name:'Record event',exact:true}).click()
    await expect.poll(()=>h.target.events.length).toBe(1)
    await stopRecording(page)
    await preview(page,info)
    const list=await h.request.get(`/api/v1/scripts?project_id=${h.workspace.projectId}`)
    expect((await list.json()).scripts).toHaveLength(0)
    await verifyOutputs(page,1)
    await page.getByLabel('Parameterize inputs',{exact:true}).check()
    await expect(page.getByText('Verification passed for this conversion',{exact:true})).toHaveCount(0)
    await expect(page.getByRole('button',{name:'Verify output 1',exact:true})).toBeDisabled()
    await page.getByLabel('Parameterize inputs',{exact:true}).uncheck()
    await preview(page,info)
    const [id]=await saveRecording(page,false)
    await editor(page,h,id)
    await debug(page,info)
    expect(h.target.events.filter(e=>e.query.value==='review-customer')).toHaveLength(3)
  }finally{await rec.browser.close().catch(()=>{})}
})
