// @vitest-environment node
import { afterEach, expect, it } from 'vitest'
import { mkdtemp, readFile, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { freePort, healthy, startProcess, stopProcess, portIsListening } from './process'
import type { ChildProcess } from 'node:child_process'
const children: ChildProcess[]=[]
const dirs:string[]=[]
afterEach(async()=>{for(const child of children.splice(0))await stopProcess(child);for(const dir of dirs.splice(0))await rm(dir,{recursive:true,force:true})})
it('stops a real service and releases its listening socket; cleanup is idempotent',async()=>{
  const dir=await mkdtemp(`${tmpdir()}/recording-process-test-`);dirs.push(dir)
  const port=await freePort()
  const child=startProcess(process.execPath,['-e',`require('node:http').createServer((q,s)=>s.end('ok')).listen(${port},'127.0.0.1')`],dir,process.env,`${dir}/service.log`);children.push(child)
  await healthy(`http://127.0.0.1:${port}`,child)
  expect(await portIsListening(port)).toBe(true)
  await stopProcess(child)
  expect(await portIsListening(port)).toBe(false)
  await expect(fetch(`http://127.0.0.1:${port}`)).rejects.toThrow()
  await stopProcess(child)
})
it('reports startup failure and redacts capability values split between output chunks',async()=>{
  const dir=await mkdtemp(`${tmpdir()}/recording-process-test-`);dirs.push(dir)
  const child=startProcess(process.execPath,['-e',`process.stdout.write('{"pairing_token": "sensitive-');setTimeout(()=>{process.stdout.write('capability"}\\n');process.exit(1)},10)`],dir,process.env,`${dir}/service.log`);children.push(child)
  await expect(healthy('http://127.0.0.1:1',child,2000)).rejects.toThrow('Service exited')
  const log=await readFile(`${dir}/service.log`,'utf8')
  expect(log).toContain('[redacted]');expect(log).not.toContain('sensitive-capability')
})
