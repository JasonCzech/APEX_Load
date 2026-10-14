import { test, expect } from './support/fixtures'
import { studio, startRecording, stopRecording, preview, verifyOutputs, saveRecording, editor, editSource, debug, expectDebugOutcome, executeSaved, recordSensitive, type Script } from './support/journey'
import type { Page, TestInfo } from '@playwright/test'
import type { Harness } from './support/fixtures'

async function recordedPair(page:Page,h:Harness,info:TestInfo) {
  await studio(page,h)
  const rec=await startRecording(page,h)
  try {
    await rec.target.getByLabel('Customer',{exact:true}).fill('recorded-seed')
    await rec.target.getByRole('button',{name:'Record event',exact:true}).click()
    await expect.poll(()=>h.target.events.length).toBe(1)
    await stopRecording(page)
    await page.getByLabel('Also create a protocol-level HTTP script from the recorded HAR',{exact:true}).check()
    const converted=await preview(page,info)
    expect(converted.scripts).toHaveLength(2)
    await verifyOutputs(page,2)
    const ids=await saveRecording(page)
    expect(ids).toHaveLength(2)
    const saved=await Promise.all(ids.map(async id=>{
      const result=await h.request.get(`/api/v1/scripts/${id}`);expect(result.ok()).toBeTruthy()
      return {id,script:(await result.json()).script as Script}
    }))
    return saved
  } finally {await rec.browser.close().catch(()=>{})}
}

for (const protocol of ['browser','http']) {
  test(`${protocol}: recorded actions exercise branches and every loop mode`,async({page,harness:h},info)=>{
    test.setTimeout(240_000)
    const saved=await recordedPair(page,h,info)
    const entry=saved.find(e=>e.script.protocol===protocol)!
    const script=structuredClone(entry.script)
    // Retain capture-derived selectors, metadata and initial navigation. Change
    // data and nest the captured action through the product's source editor.
    const fill=script.actions.find((a:Script)=>a.type==='browser_fill')
    const click=script.actions.find((a:Script)=>a.type==='browser_click')
    const request=script.actions.find((a:Script)=>a.type==='request'&&String(a.target).includes('/event'))
    if(request){
      // The event endpoint is intentionally anonymous. Remove its captured
      // session cookie before moving the request into an unrelated workflow.
      delete request.headers.Cookie
      request.header_list=request.header_list.filter((header:Script)=>header.name.toLowerCase()!=='cookie')
    }
    expect(protocol==='browser'?fill&&click:request).toBeTruthy()
    const emit=(value:string):Script[]=>protocol==='browser'
      ? [{...fill,name:`Value ${value}`,value},{...click,name:`Emit ${value}`}]
      : [{...request,name:`Emit ${value}`,target:`/event?name=control&value=${value.replace('-{{var.loop_index}}','&index={{var.loop_index}}')}`}]
    const cond=(left:string,op:string,right:string)=>({left,op,right})
    const loop=(name:string,control:Script)=>({name,type:'control_loop',control})
    const branch=(name:string,condition:Script,steps:Script[],otherwise:Script[]=[])=>({name,type:'control_if',control:{condition,steps,else:otherwise}})
    script.data_sources=[{name:'rows',type:'csv',inline_data:'value\nrow-a\nrow-b',first_row_header:true,strategy:'sequential'}]
    script.actions=[
      ...(protocol==='browser'?script.actions.filter((a:Script)=>a.type==='browser_navigate'):[]),
      branch('True branch',cond('yes','eq','yes'),emit('if-true'),emit('wrong')),
      branch('False branch',cond('yes','eq','no'),emit('wrong'),emit('if-false')),
      {name:'Switch matching',type:'control_switch',control:{cases:[{condition:cond('a','eq','a'),steps:emit('switch')}],default:emit('wrong')}},
      {name:'Switch default',type:'control_switch',control:{cases:[{condition:cond('a','eq','b'),steps:emit('wrong')}],default:emit('default')}},
      loop('Counted',{count:3,steps:emit('count-{{var.loop_index}}')}),
      loop('While',{condition:cond('{{var.loop_index}}','lte','2'),max_iterations:4,steps:emit('while-{{var.loop_index}}')}),
      loop('Do while',{condition:cond('false','eq','true'),do_while:true,max_iterations:3,steps:emit('do-once')}),
      loop('Safety bound',{condition:cond('yes','eq','yes'),max_iterations:2,steps:emit('bounded')}),
      loop('Rows',{for_each:{source:'rows',var:'row'},steps:emit('{{var.row.value}}')}),
      loop('Outer',{count:2,steps:[...emit('outer-{{var.loop_index}}'),loop('Inner',{count:2,steps:emit('inner-{{var.loop_index}}')}),...emit('restored-{{var.loop_index}}')]}),
      loop('Continue',{count:3,steps:[branch('Skip second',cond('{{var.loop_index}}','eq','2'),[{name:'Continue',type:'control_continue'}]),...emit('continue-{{var.loop_index}}')]}),
      loop('Break',{count:5,steps:[...emit('break-{{var.loop_index}}'),branch('Break second',cond('{{var.loop_index}}','eq','2'),[{name:'Break',type:'control_break'}])]}),
      ...(protocol==='http'?[{name:'Parallel',type:'control_parallel',control:{concurrency:2,branches:[{name:'left',steps:emit('parallel-left')},{name:'right',steps:emit('parallel-right')}]}}]:[]),
    ]
    await editor(page,h,entry.id)
    await editSource(page,script)
    const before=h.target.events.length
    const report=await debug(page,info)
    const values=h.target.events.slice(before).map(e=>e.query.value+(e.query.index?`-${e.query.index}`:''))
    expect(values.slice(0,26)).toEqual(['if-true','if-false','switch','default',
      'count-1','count-2','count-3','while-1','while-2','do-once','bounded','bounded',
      'row-a','row-b','outer-1','inner-1','inner-2','restored-1',
      'outer-2','inner-1','inner-2','restored-2','continue-1','continue-3','break-1','break-2'])
    expect(values.slice(26).sort()).toEqual(protocol==='http'?['parallel-left','parallel-right']:[])
    expect(report.events.some((e:Script)=>e.iteration_path?.length>1)).toBe(true)
    expect(values.filter(v=>v==='bounded')).toHaveLength(2)
    expect(report.final_variables?.loop_index).toBeUndefined()
    expect(report.final_variables?.['row.value']).toBeUndefined()
  })
}

test('recorded browser inputs use list parameters, CSV rows and session overrides',async({page,harness:h},info)=>{
  const entry=(await recordedPair(page,h,info)).find(e=>e.script.protocol==='browser')!
  const script=structuredClone(entry.script)
  const fill=script.actions.find((a:Script)=>a.type==='browser_fill')
  fill.value='{{param.customer}}'
  script.parameters=[{name:'customer',type:'list',values:['parameter-customer']}]
  await editor(page,h,entry.id)
  await editSource(page,script)
  let before=h.target.events.length
  await debug(page,info)
  expect(h.target.events.slice(before).some(e=>e.query.value==='parameter-customer')).toBe(true)
  fill.value='{{var.customer}}'
  await editSource(page,script)
  await page.getByRole('group',{name:'Script workspace'}).getByRole('button',{name:'Debug',exact:true}).click()
  await page.getByRole('button',{name:/^Debug options/}).click()
  const options=page.getByRole('region',{name:'Session Overrides'})
  await options.getByLabel('Session Variables JSON').fill('{')
  await expect(options.getByLabel('Session Variables JSON')).toHaveAttribute('aria-invalid','true')
  await options.getByLabel('Session Variables JSON').fill('{"customer":"override-customer"}')
  await options.getByRole('button',{name:'Done',exact:true}).click()
  before=h.target.events.length
  await debug(page,info)
  expect(h.target.events.slice(before).some(e=>e.query.value==='override-customer')).toBe(true)
  script.parameters=[]
  script.data_sources=[{name:'customers',type:'csv',inline_data:'name\ncsv-customer',first_row_header:true,strategy:'sequential'}]
  fill.value='{{data.customers.name}}'
  await editSource(page,script)
  before=h.target.events.length
  await debug(page,info)
  expect(h.target.events.slice(before).some(e=>e.query.value==='csv-customer')).toBe(true)
  fill.value='{{secret.customer_value}}'
  await editSource(page,script)
  await page.getByRole('group',{name:'Script workspace'}).getByRole('button',{name:'Debug',exact:true}).click()
  await expect(page.locator('.topbar-debug-action-run')).toBeDisabled()
  await page.getByRole('button',{name:'Configure secrets',exact:true}).first().click()
  const bindings=page.getByRole('dialog',{name:/^Secret Bindings/})
  await expect(bindings).toContainText('Missing value')
  recordSensitive('vault-customer')
  await bindings.getByLabel('Secret Value',{exact:true}).fill('vault-customer')
  await bindings.getByRole('button',{name:'Configure exact reference',exact:true}).click()
  await expect(bindings).toContainText('Configured')
  await bindings.getByRole('button',{name:'Close',exact:true}).click()
  before=h.target.events.length
  const secretReport=await debug(page,info)
  expect(h.target.events.slice(before).some(e=>e.query.value==='vault-customer')).toBe(true)
  expect(JSON.stringify(secretReport)).not.toContain('vault-customer')

})

test('debugger steps a recorded HTTP script, diagnoses failure and repairs it',async({page,harness:h},info)=>{
  const entry=(await recordedPair(page,h,info)).find(e=>e.script.protocol==='http')!
  const script=structuredClone(entry.script)
  const request=script.actions.find((a:Script)=>a.type==='request'&&String(a.target).includes('/event'))
    if(request){
      // The event endpoint is intentionally anonymous. Remove its captured
      // session cookie before moving the request into an unrelated workflow.
      delete request.headers.Cookie
      request.header_list=request.header_list.filter((header:Script)=>header.name.toLowerCase()!=='cookie')
    }
  script.actions=[{...request,name:'Get customer',target:'/event?name=extract',extractors:[{name:'customer',source:'body',type:'jsonpath',expression:'$.customer'}]},
    {...request,name:'Use customer',target:'/event?name=use&value={{var.customer}}',assertions:[{type:'status_code',expected:201}]},
    {...request,name:'Finish',target:'/event?name=finish'}]
  await editor(page,h,entry.id);await editSource(page,script)
  await page.getByRole('group',{name:'Script workspace'}).getByRole('button',{name:'Debug',exact:true}).click()
  await page.getByRole('button',{name:/^Debug options/}).click()
  const options=page.getByRole('region',{name:'Session Overrides'})
  await options.getByLabel('Step through (start paused)').check()
  await options.getByLabel('Break on failure',{exact:true}).check()
  await options.getByLabel('Profile name',{exact:true}).fill('Recording diagnosis')
  await options.getByRole('button',{name:'Save profile',exact:true}).click()
  await options.getByLabel('Step through (start paused)').uncheck()
  await options.getByRole('combobox',{name:/^Run profile/}).selectOption('Recording diagnosis')
  await expect(options.getByLabel('Step through (start paused)')).toBeChecked()
  await options.getByRole('button',{name:'Done',exact:true}).click()
  await page.locator('.topbar-debug-action-run').click()
  await expect(page.getByRole('status').filter({hasText:'Paused before: Get customer'})).toBeVisible()
  await page.getByRole('button',{name:'Step',exact:true}).click()
  // Following execution moves the view on to the next action; the stepped
  // action keeps its evidence.
  await expect(page.getByRole('status').filter({hasText:'Paused before: Use customer'})).toBeVisible()
  const actions=page.getByRole('region',{name:'Debugger actions'})
  await actions.getByRole('button',{name:/^Action 1 Get customer/}).click()
  await expect(page.getByRole('region',{name:'Response evidence'})).toContainText('Body captured')
  await page.getByRole('button',{name:'Continue',exact:true}).click()
  await expect(page.getByRole('status').filter({hasText:'Paused after failure: Use customer'})).toBeVisible()
  await page.getByRole('button',{name:'Go to current action',exact:true}).click()
  await expect(page.getByRole('region',{name:'Response evidence'})).toContainText('expected status 201, got 200')
  await page.getByRole('tab',{name:/^Problems/}).click()
  await expect(page.getByRole('tabpanel',{name:'Problems'})).toContainText('1 runtime failures')
  await page.getByRole('button',{name:'Continue',exact:true}).click()
  await expectDebugOutcome(page, false)
  await actions.getByRole('button',{name:/^Action 2 Use customer/}).click()
  await page.getByRole('button',{name:'Edit this action',exact:true}).click()
  await page.getByLabel('Check 1 status',{exact:true}).fill('200')
  await page.locator('.topbar-debug-action-run').click()
  await expect(page.getByRole('button',{name:'Continue',exact:true})).toBeEnabled()
  await page.getByRole('button',{name:'Continue',exact:true}).click()
  await expectDebugOutcome(page)
  expect(h.target.events.some(e=>e.query.value==='fresh-customer')).toBe(true)
  await page.getByRole('tab',{name:'Variables',exact:true}).click()
  await page.getByRole('button',{name:'Pin customer',exact:true}).click()
  await page.getByLabel('Search variables',{exact:true}).fill('customer')
  await expect(page.getByRole('region',{name:'Variable watches'})).toContainText('fresh-customer')
  await page.getByText('Recent runs (1) · Compare evidence',{exact:true}).click()
  await page.getByRole('button',{name:'Pin comparison baseline'}).click()
  await page.getByText('Compare baseline with current run',{exact:true}).click()
  await expect(page.locator('.debug-run-history')).toContainText('Outcome')
  await page.getByText('Recent runs (1) · Compare evidence',{exact:true}).click()
  await page.getByRole('tab',{name:'Timeline',exact:true}).click()
  await page.getByLabel('Find execution').fill('Use customer')
  await page.locator('.debug-execution-timeline').getByRole('button',{name:/Passed · Use customer/}).click()
  await expect(page.locator('.debug-selection-status')).toContainText('Inspecting action 2')
  await page.locator('.topbar-debug-action-step').click()
  await expect(page.getByRole('status').filter({hasText:'Paused before: Get customer'})).toBeVisible()
  await page.locator('.topbar-debug-action-stop').click()
  await expect(page.locator('.topbar-debug-action-stop')).toHaveCount(0)
  await page.getByRole('button',{name:'Add breakpoint on Finish',exact:true}).click()
  await page.getByRole('region',{name:'Debugger actions'}).getByRole('button',{name:/Action 2.*Use customer/}).click()
  const before=h.target.events.length
  await page.getByRole('button',{name:'Run to selected action',exact:true}).click()
  await expect(page.getByRole('status').filter({hasText:'Paused before: Use customer'})).toBeVisible()
  expect(h.target.events.slice(before).map(e=>e.query.name)).toEqual(['extract'])
  await page.getByRole('button',{name:'Continue',exact:true}).click()
  await expect(page.getByRole('status').filter({hasText:'Paused before: Finish'})).toBeVisible()
  await page.getByRole('button',{name:'Continue',exact:true}).click()
  await expectDebugOutcome(page)
  script.actions=[{...request,name:'Slow action',target:'/event?name=slow&delay_ms=1500'},
    {...request,name:'After pause',target:'/event?name=after-pause'}]
  await editSource(page,script)
  await page.getByRole('group',{name:'Script workspace'}).getByRole('button',{name:'Debug',exact:true}).click()
  await page.getByRole('button',{name:/^Debug options/}).click()
  await options.getByLabel('Step through (start paused)').uncheck()
  await options.getByRole('button',{name:'Done',exact:true}).click()
  const pausedBefore=h.target.events.length
  await page.locator('.topbar-debug-action-run').click()
  await expect.poll(()=>h.target.events.slice(pausedBefore).map(e=>e.query.name)).toEqual(['slow'])
  await page.locator('.topbar-debug-action-pause').click()
  await expect(page.getByRole('status').filter({hasText:'Paused before: After pause'})).toBeVisible()
  expect(h.target.events.slice(pausedBefore).map(e=>e.query.name)).toEqual(['slow'])
  await page.locator('.topbar-debug-action-stop').click()
  await expect(page.locator('.topbar-debug-action-stop')).toHaveCount(0)
  const restartedBefore=h.target.events.length
  await debug(page,info)
  expect(h.target.events.slice(restartedBefore).map(e=>e.query.name)).toEqual(['slow','after-pause'])
  await info.attach('debugger-outcomes',{body:JSON.stringify(h.target.events),contentType:'application/json'})
})

test('saved browser and HTTP recordings execute one VUser through New Test',async({page,harness:h},info)=>{
  test.setTimeout(300_000)
  for(const entry of await recordedPair(page,h,info)) await executeSaved(page,h,entry.id,info)
})

test('recorded HTTP correlation fails with the wrong extraction and succeeds after repair',async({page,harness:h},info)=>{
  const entry=(await recordedPair(page,h,info)).find(e=>e.script.protocol==='http')!
  const script=structuredClone(entry.script)
  const captured=script.actions.find((a:Script)=>a.type==='request'&&String(a.target).includes('/event'))
  delete captured.headers.Cookie
  captured.header_list=captured.header_list.filter((header:Script)=>header.name.toLowerCase()!=='cookie')
  script.actions=[{...captured,name:'Extract token',target:'/event?name=extract',extractors:[{name:'manual_token',type:'jsonpath',source:'body',expression:'$.customer'}]},
    {name:'Correlated checkout',type:'request',method:'POST',target:'/checkout',headers:{'Content-Type':'application/json'},body:'{"csrf_token":"{{var.manual_token}}","customer":"correlation-customer"}',assertions:[{type:'status_code',expected:200}]}]
  await editor(page,h,entry.id);await editSource(page,script)
  h.target.rotateTokens()
  const failed=await debug(page,info,false)
  expect(failed.events.some((e:Script)=>e.error_message?.includes('409'))).toBe(true)
  await page.getByRole('tab',{name:/^Problems/}).click()
  await expect(page.getByRole('tabpanel',{name:'Problems'})).toContainText('1 runtime failures')
  script.actions[0].target='/token'
  script.actions[0].extractors[0].expression='$.csrf_token'
  await editSource(page,script)
  await debug(page,info)
  expect(h.target.events.filter(e=>e.path==='/checkout').map(e=>e.accepted)).toEqual([false,true])
})

test('recorded browser flow consumes a fresh browser extraction result',async({page,harness:h},info)=>{
  const entry=(await recordedPair(page,h,info)).find(e=>e.script.protocol==='browser')!
  const script=structuredClone(entry.script)
  const fill=script.actions.find((a:Script)=>a.type==='browser_fill')
  fill.value='{{var.result}}'
  script.actions.splice(1,0,{name:'Extract live title',type:'browser_evaluate_js',target:'document.title + " extracted"'})
  await editor(page,h,entry.id);await editSource(page,script)
  const before=h.target.events.length
  const report=await debug(page,info)
  expect(h.target.events.slice(before).map(e=>e.query.value)).toEqual(['Recording fixture extracted'])
  expect(report.events.some((e:Script)=>e.extractions?.result==='Recording fixture extracted')).toBe(true)
})
