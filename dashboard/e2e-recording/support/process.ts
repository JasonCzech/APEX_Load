import { spawn, type ChildProcess } from 'node:child_process'
import { createServer, createConnection } from 'node:net'
import { appendFileSync } from 'node:fs'

export async function freePort(): Promise<number> {
  const server = createServer()
  await new Promise<void>(resolve => server.listen(0, '127.0.0.1', resolve))
  const address = server.address()
  if (!address || typeof address === 'string') throw new Error('No ephemeral address')
  await new Promise<void>((resolve, reject) => server.close(error => error ? reject(error) : resolve()))
  return address.port
}
export function startProcess(binary: string, args: string[], cwd: string, env: NodeJS.ProcessEnv, log: string): ChildProcess {
  const child = spawn(binary, args, { cwd, env, stdio: ['pipe', 'pipe', 'pipe'] })
  // Pairing tokens are supplied through stdin, never argv/logs. Agent startup
  // JSON includes a token: redact at the line boundary, including split chunks.
  for (const stream of [child.stdout!, child.stderr!]) {
    let pending = ''
    const flush = (line: string) => appendFileSync(log, line.replace(/("pairing_token"\s*:\s*")[^"]+/g, '$1[redacted]') + '\n')
    stream.on('data', chunk => { pending += String(chunk); const lines = pending.split('\n'); pending = lines.pop()!; lines.forEach(flush) })
    stream.on('end', () => { if (pending) flush(pending) })
  }
  return child
}
export async function stopProcess(child: ChildProcess) {
  if (child.exitCode !== null || child.signalCode !== null) return
  await new Promise<void>(resolve => {
    const timer = setTimeout(() => child.kill('SIGKILL'), 8000)
    child.once('exit', () => { clearTimeout(timer); resolve() })
    child.kill('SIGTERM')
  })
}
export async function healthy(url: string, child: ChildProcess, timeout = 30_000) {
  const until = Date.now() + timeout
  while (Date.now() < until) {
    if (child.exitCode !== null || child.signalCode !== null) throw new Error(`Service exited during startup: ${url}`)
    try { if ((await fetch(url, { signal: AbortSignal.timeout(1000) })).ok) return } catch { /* not yet listening */ }
    await new Promise(resolve => setTimeout(resolve, 100))
  }
  throw new Error(`Service did not become healthy: ${url}`)
}

export function portIsListening(port:number):Promise<boolean> {
  return new Promise(resolve=>{
    const socket=createConnection({host:'127.0.0.1',port})
    const finish=(open:boolean)=>{socket.destroy();resolve(open)}
    socket.once('connect',()=>finish(true))
    socket.once('error',()=>finish(false))
    socket.setTimeout(1000,()=>finish(false))
  })
}
