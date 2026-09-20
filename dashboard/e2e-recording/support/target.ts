import { createServer, type IncomingMessage, type ServerResponse } from 'node:http'
import { createServer as createHTTPSServer } from 'node:https'
import { createHash, randomUUID } from 'node:crypto'
import { execFileSync } from 'node:child_process'
import { readFileSync } from 'node:fs'
import type { AddressInfo } from 'node:net'

export interface TargetEvent { path: string; method: string; body: string; query: Record<string, string>; accepted: boolean }
export async function createTarget(dir: string) {
  const events: TargetEvent[] = []
  const tokens = new Map<string,{order:string;session:string}>()
  let renamed = false
  const sensitiveValues = new Set<string>(['synthetic-password', 'fixture-ws-secret'])
  const handler = async (req: IncomingMessage, res: ServerResponse) => {
    const url = new URL(req.url!, 'http://fixture')
    const chunks: Buffer[] = []
    for await (const chunk of req) chunks.push(Buffer.from(chunk))
    const body = Buffer.concat(chunks).toString()
    if (url.pathname === '/token') {
      const token = randomUUID(), order = randomUUID(), session = randomUUID(); tokens.set(token,{order,session});
      [token,order,session].forEach(value=>sensitiveValues.add(value))
      res.setHeader('Content-Type', 'application/json')
      // Only checkout authenticates. Public iframe/event traffic must remain
      // anonymous regardless of whether its request races this response.
      res.setHeader('Set-Cookie', `fixture_session=${session}; Path=/checkout; SameSite=Lax`)
      res.end(JSON.stringify({ csrf_token: token, order_id: order })); return
    }
    if (url.pathname === '/checkout') {
      const data = JSON.parse(body || '{}') as { csrf_token?: string; order_id?: string }
      const expectedOrder=tokens.get(data.csrf_token || '')
      const fresh = tokens.delete(data.csrf_token || '')
      const accepted = fresh && Boolean(expectedOrder && req.headers.cookie?.includes(`fixture_session=${expectedOrder.session}`)) && (!data.order_id || data.order_id === expectedOrder?.order)
      events.push({ path: url.pathname, method: req.method!, body, query: Object.fromEntries(url.searchParams), accepted })
      res.writeHead(accepted ? 200 : 409, { 'Content-Type': 'application/json' })
      res.end(JSON.stringify({ accepted, message: accepted ? 'Order accepted' : 'Stale correlation token' })); return
    }
    if (url.pathname === '/event' || url.pathname === '/upload') {
      events.push({ path: url.pathname, method: req.method!, body, query: Object.fromEntries(url.searchParams), accepted: true })
      const delay=Math.min(2000,Math.max(0,Number(url.searchParams.get('delay_ms'))||0))
      if(delay) await new Promise(resolve=>setTimeout(resolve,delay))
      res.setHeader('Content-Type', 'application/json'); res.end(JSON.stringify({ ok: true, customer: 'fresh-customer', path: url.pathname, count: events.length })); return
    }
    if (url.pathname === '/redirect') { res.writeHead(302, { Location: '/?redirected=1' }); res.end(); return }
    if (url.pathname === '/noise.css') { res.setHeader('Content-Type', 'text/css'); res.end('body { font-family: sans-serif }'); return }
    if (url.pathname === '/download') { res.setHeader('Content-Disposition', 'attachment; filename=fixture.txt'); res.end('synthetic fixture download'); return }
    if (url.pathname === '/frame') { res.end('<button id="frame-action" onclick="fetch(\'/event?name=frame\')">Frame action</button>'); return }
    if (url.pathname === '/popup') { res.end('<title>Fixture popup</title><button id="popup-action" onclick="fetch(\'/event?name=popup\')">Popup action</button>'); return }
    res.setHeader('Content-Type', 'text/html')
    res.end(`<!doctype html><html><head><title>Recording fixture</title><link rel="stylesheet" href="/noise.css"></head><body>
<style>body{padding:20px;max-width:700px}label{display:block;margin:10px}button{margin:8px}#drag,#drop{display:inline-block;padding:20px;border:1px solid}#spacer{height:1000px}</style>
<h1>Recording fixture</h1><button id="remember" onclick="localStorage.setItem('saved-choice','remembered-by-recording');document.querySelector('#stored-state').textContent='remembered-by-recording'">Remember session</button><span id="stored-state"></span><div id="result">Ready for checkout</div>
<form id="order"><label>Customer <input id="customer" name="customer" value="captured-customer"></label>
<label>Password <input id="password" name="password" type="password"></label>
<label for="plan">Plan</label><select id="plan" size="2"><option value="basic" selected>Basic</option><option value="premium">Premium</option></select>
<label><input id="agree" type="checkbox">Agree</label><label><input id="radio" type="radio" name="choice">Choice</label>
<button id="checkout" type="submit">Checkout</button></form>
<button id="${renamed ? 'event-renamed' : 'event'}" onclick="fetch('/event?name=click&value='+encodeURIComponent(document.querySelector('#customer').value))">Record event</button>
<button id="double" ondblclick="fetch('/event?name=double')">Double action</button>
<button id="hover" onmouseenter="this.title='Hovered'">Hover action</button>
<button id="dialog" onclick="alert('Fixture dialog')">Dialog action</button>
<button id="popup" onclick="window.open('/popup','fixture-popup')">Open popup</button>
<button id="spa" onclick="history.pushState({},'', '/journey');fetch('/event?name=spa')">SPA route</button>
<button id="socket" onclick="window.socket=new WebSocket(location.origin.replace('http','ws')+'/ws');socket.onmessage=e=>document.querySelector('#ws-result').textContent=JSON.parse(e.data).message">Open socket</button><span id="ws-result"></span>
<button id="filtered" onclick="fetch(location.origin.replace('127.0.0.1','localhost')+'/event?name=filtered',{mode:'no-cors'})">Excluded host event</button>
<form method="post" action="/event?name=form"><label>Form value <input name="form_value" value="form-customer"></label><button type="submit">Submit form</button></form>
<a href="/download" download>Download file</a><input id="file" type="file" onchange="fetch('/upload',{method:'POST',body:this.files[0]})">
<div id="drag" draggable="true" ondragstart="event.dataTransfer.setData('text/plain','fixture')">Drag item</div><div id="drop" ondragover="event.preventDefault()" ondrop="event.preventDefault();fetch('/event?name=drop')">Drop here</div>
<div id="shadow"></div><iframe id="frame" name="fixture-frame" src="/frame"></iframe><div id="spacer"></div><button id="bottom" onclick="fetch('/event?name=bottom')">Bottom action</button>
<script>
document.querySelector('#stored-state').textContent=localStorage.getItem('saved-choice')||'empty';
window.ready = fetch('/token').then(r=>r.json()).then(data=>{window.token=data.csrf_token;window.orderID=data.order_id;document.body.dataset.token=data.csrf_token;localStorage.setItem('fixture-state','ready')});
document.querySelector('#order').onsubmit=async e=>{e.preventDefault();await window.ready;const response=await fetch('/checkout',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({csrf_token:window.token,order_id:window.orderID,customer:document.querySelector('#customer').value,plan:document.querySelector('#plan').value})});document.querySelector('#result').textContent=(await response.json()).message};
const shadowButton=document.createElement('button');shadowButton.id='shadow-action';shadowButton.textContent='Shadow action';shadowButton.onclick=()=>fetch('/event?name=shadow');document.querySelector('#shadow').attachShadow({mode:'open'}).append(shadowButton);
</script></body></html>`)
  }
  const server = createServer((req, res) => { void handler(req, res).catch(error => { res.statusCode = 500; res.end(String(error)) }) })
  const sockets = new Set<import('node:stream').Duplex>()
  server.on('upgrade', (req, socket) => {
    sockets.add(socket); socket.on('close', () => sockets.delete(socket))
    const accept = createHash('sha1').update(String(req.headers['sec-websocket-key']) + '258EAFA5-E914-47DA-95CA-C5AB0DC85B11').digest('base64')
    socket.write(`HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: ${accept}\r\n\r\n`)
    for(const frame of [{message:'fixture-ready'},{message:'fixture-ready',password:'fixture-ws-secret'}]) {
      const payload = Buffer.from(JSON.stringify(frame))
      socket.write(Buffer.concat([Buffer.from([0x81, payload.length]), payload]))
    }
    socket.on('data', () => { /* fixture only sends an observer message */ })
  })
  const key = `${dir}/target.key`, cert = `${dir}/target.pem`
  execFileSync('openssl', ['req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-keyout', key, '-out', cert, '-days', '1', '-subj', '/CN=localhost', '-addext', 'subjectAltName=DNS:localhost,IP:127.0.0.1'], { stdio: 'ignore' })
  const secure = createHTTPSServer({ key: readFileSync(key), cert: readFileSync(cert) }, (req, res) => { void handler(req, res) })
  await Promise.all([server, secure].map(s => new Promise<void>(resolve => s.listen(0, '127.0.0.1', resolve))))
  return { url: `http://127.0.0.1:${(server.address() as AddressInfo).port}`, httpsURL: `https://127.0.0.1:${(secure.address() as AddressInfo).port}`, events, sensitiveValues,
    rotateTokens: () => tokens.clear(),
    renameEventControl: () => { renamed = true },
    async close() { sockets.forEach(s => s.destroy()); await Promise.all([server, secure].map(s => { s.closeAllConnections(); return new Promise<void>(resolve => s.close(() => resolve())) })) },
  }
}
