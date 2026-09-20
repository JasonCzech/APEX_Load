// @vitest-environment node
import { expect,it } from 'vitest'
import {mkdtemp,rm} from 'node:fs/promises'
import {tmpdir} from 'node:os'
import {createTarget} from './target'
it('rejects stale and reused tokens and records actual submitted values independently',async()=>{
  const dir=await mkdtemp(`${tmpdir()}/recording-target-test-`)
  const target=await createTarget(dir)
  try {
    const firstResponse=await fetch(target.url+'/token')
    const first=await firstResponse.json()
    let cookie=firstResponse.headers.get('set-cookie')!
    expect(cookie).toContain('Path=/checkout;')
    target.rotateTokens()
    const submit=(token:string)=>fetch(target.url+'/checkout',{method:'POST',headers:{Cookie:cookie},body:JSON.stringify({csrf_token:token,customer:'runtime-value'})})
    expect((await submit(first.csrf_token)).status).toBe(409)
    const nextResponse=await fetch(target.url+'/token')
    const next=await nextResponse.json()
    cookie=nextResponse.headers.get('set-cookie')!
    expect(next.csrf_token).not.toBe(first.csrf_token)
    expect((await submit(next.csrf_token)).status).toBe(200)
    expect((await submit(next.csrf_token)).status).toBe(409)
    expect(target.events.map(e=>e.accepted)).toEqual([false,true,false])
    expect(JSON.parse(target.events[1].body).customer).toBe('runtime-value')
  }finally{await target.close();await rm(dir,{recursive:true,force:true})}
})
