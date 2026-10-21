package browser

import (
	_ "embed"
	"fmt"
	"strconv"
)

// Unmodified Apache-2.0 web-vitals 5.1.0 IIFE. Its digest is in the evidence
// contract. The lexical adapter drains only observations already recorded by
// the library's own native observers; it never dispatches target events.
//
//go:embed third_party/web_vitals/web-vitals-5.1.0.iife.js
var browserVitalsLibrary string

// resources reports each Resource Timing entry's name hash, start,
// nextHopProtocol and transferSize, the only document association and
// protocol evidence WebKit exposes; it never sends the URL itself.
// loader names the evidence CDP session's binding: a new document reports its
// token there once, from its own context, and removes the binding.
func browserDocumentBootstrap(binding, finalizer, loader string, metrics, resources bool) string {
	return fmt.Sprintf(`(() => {
 "use strict";
 const binding=%s, finalizer=%s, loader=%s, enabled=%t, resources=%t;
 const token=()=>{const a=new Uint8Array(16);crypto.getRandomValues(a);return Array.from(a,x=>x.toString(16).padStart(2,'0')).join('')};
 const documentToken=token(); let lifecycleToken=token(), restored=false, terminal=false, boundary='progress', beginReceipt=null, finishReceipt=null;
 const values=new Map(), observers=new Set(), idle=new Set(); let firstInput=false, lcpFinal=false;
 const supported=new Set(globalThis.PerformanceObserver?.supportedEntryTypes||[]);
 let minInteraction=Infinity,maxInteraction=0,baseInteraction=0;
 const interactionCount=()=>Math.max(0, ('interactionCount' in performance ? performance.interactionCount : maxInteraction ? (maxInteraction-minInteraction)/7+1 : 0)-baseInteraction);
 const updateInteractions=entries=>{for(const entry of entries)if(entry.interactionId){minInteraction=Math.min(minInteraction,entry.interactionId);maxInteraction=Math.max(maxInteraction,entry.interactionId)}};
 const send=(kind, metric, state, reason='', metricBoundary=boundary)=>{
  const value=values.get(metric);
  const payload={kind,document_token:documentToken,lifecycle_token:lifecycleToken,restored,time_origin:performance.timeOrigin,offset:performance.now(),metric,state,boundary:metricBoundary,reason,interaction_count:interactionCount(),value:value===undefined?null:value};
  const deliver=()=>{try{return globalThis[binding](payload).catch(()=>false)}catch{return Promise.resolve(false)}};
  if(kind==='begin')return deliver();
  const expectedLife=lifecycleToken, admission=beginReceipt;
  return Promise.resolve(admission).then(accepted=>accepted===true&&expectedLife===lifecycleToken?deliver():false);
 };
 const report=(metric,native)=>{if(terminal||metric==='lcp'&&lcpFinal)return;values.set(metric,native.value);let state='provisional';
  if(metric==='fcp'||metric==='ttfb'){state='final';boundary='metric_observed'}
  if(metric==='lcp'&&(firstInput||document.visibilityState==='hidden')){state='final';boundary=firstInput?'first_input':'hidden';lcpFinal=true}
  send('metric',metric,state);
 };
 beginReceipt=send('begin','','provisional');
 try{const reportLoader=globalThis[loader];if(typeof reportLoader==='function'){reportLoader(documentToken);delete globalThis[loader]}}catch{}
 const resourceHash=s=>{let h1=0xdeadbeef,h2=0x41c6ce57;for(let i=0;i<s.length;i++){const c=s.charCodeAt(i);h1=Math.imul(h1^c,2654435761);h2=Math.imul(h2^c,1597334677)}h1=Math.imul(h1^(h1>>>16),2246822507);h1^=Math.imul(h2^(h2>>>13),3266489909);h2=Math.imul(h2^(h2>>>16),2246822507);h2^=Math.imul(h1^(h1>>>13),3266489909);return(4294967296*(2097151&h2)+(h1>>>0)).toString(16)};
 if(resources&&globalThis.PerformanceObserver&&supported.has('resource')){
  let sentResources=0;
  const sendResource=entry=>{if(sentResources>=512)return;sentResources++;
   const expectedLife=lifecycleToken, admission=beginReceipt;
   const payload={kind:'resource',document_token:documentToken,lifecycle_token:lifecycleToken,restored,time_origin:performance.timeOrigin,offset:performance.now(),metric:resourceHash(entry.name),state:'provisional',boundary,reason:'',interaction_count:0,value:entry.startTime,protocol:typeof entry.nextHopProtocol==='string'?entry.nextHopProtocol.slice(0,32):'',transfer_size:typeof entry.transferSize==='number'?entry.transferSize:null};
   Promise.resolve(admission).then(accepted=>{if(accepted===true&&expectedLife===lifecycleToken){try{globalThis[binding](payload).catch(()=>false)}catch{}}});
  };
  new globalThis.PerformanceObserver(list=>{for(const entry of list.getEntries())sendResource(entry)}).observe({type:'resource',buffered:true});
 }
 // Establish restored identity before the official library's synchronous
 // BFCache callbacks report the restored TTFB and other observations.
 addEventListener('pageshow',event=>{if(event.persisted){lifecycleToken=token();restored=true;terminal=false;finishReceipt=null;firstInput=false;lcpFinal=false;boundary='bfcache_restore';values.clear();baseInteraction=('interactionCount' in performance?performance.interactionCount:maxInteraction?(maxInteraction-minInteraction)/7+1:0);beginReceipt=send('begin','','provisional')}},{capture:true});
 if(enabled&&window.PerformanceObserver){
  const NativeObserver=window.PerformanceObserver;
  class PerformanceObserver {
   static supportedEntryTypes=NativeObserver.supportedEntryTypes;
   constructor(callback){this.callback=callback;this.native=new NativeObserver(list=>callback(list));observers.add(this)}
   observe(options){this.native.observe(options)}
   disconnect(){this.native.disconnect();observers.delete(this)}
   takeRecords(){return this.native.takeRecords()}
  }
  // Keep the library's deferred processing local and drainable. Its native
  // observer records, interaction IDs and algorithm remain unmodified.
  const realGlobal=window;
  // Window accessors require the native receiver; inherit no Window getters.
  const globalThis={performance:realGlobal.performance,PerformanceEventTiming:realGlobal.PerformanceEventTiming};
  globalThis.requestIdleCallback=(callback)=>{const once=()=>{if(idle.delete(once))callback()};idle.add(once);return realGlobal.setTimeout(once,0)};
  %s
  webVitals.onFCP(m=>report('fcp',m),{reportAllChanges:true});
  webVitals.onLCP(m=>report('lcp',m),{reportAllChanges:true});
  webVitals.onCLS(m=>report('cls',m),{reportAllChanges:true});
  webVitals.onINP(m=>report('inp',m),{reportAllChanges:true});
  webVitals.onTTFB(m=>report('ttfb',m),{reportAllChanges:true});
  if(!('interactionCount' in performance)&&supported.has('event')){const count=new NativeObserver(list=>updateInteractions(list.getEntries()));count.observe({type:'event',buffered:true,durationThreshold:0});observers.add({native:count,callback:list=>updateInteractions(list.getEntries())})}
 }
 const metricReason=metric=>{
  if(metric==='inp'&&(!supported.has('event')||!realPerformanceEventTiming()))return 'unsupported';
  if(metric==='inp'&&interactionCount()===0)return 'no_interaction';
  if(metric==='cls'&&!supported.has('layout-shift')||metric==='lcp'&&!supported.has('largest-contentful-paint')||metric==='fcp'&&!supported.has('paint'))return 'unsupported';
  return document.visibilityState==='hidden'&&metric==='lcp'?'hidden_before_paint':'no_observation';
 };
 const realPerformanceEventTiming=()=>!!globalThis.PerformanceEventTiming&&'interactionId' in globalThis.PerformanceEventTiming.prototype;
 // Empty callback batches let the pinned INP manager recompute its retained
 // rank when the native interaction counter settled after its last callback.
 // They supply no synthetic event or value.
 const drain=async()=>{for(const observer of observers){const entries=observer.native.takeRecords();observer.callback({getEntries:()=>entries})}await Promise.resolve();for(const callback of Array.from(idle))callback();await Promise.resolve()};
 // The library reports LCP at its first-input or hidden boundary only when
 // the value changed there; once its pending work is drained, an unchanged
 // value is final at that boundary too.
 const finalizeLCP=async reason=>{const expectedLife=lifecycleToken;if(lcpFinal||terminal||!values.has('lcp'))return;await drain();if(lcpFinal||terminal||expectedLife!==lifecycleToken||!values.has('lcp'))return;lcpFinal=true;await send('metric','lcp','final','',reason)};
 // Every invocation joins this lifecycle's complete binding receipt chain.
 // A restored lifecycle has its own admission/promise; old continuations may
 // finish awaiting transport but cannot emit under or terminalize the new token.
 const finish=(nextBoundary)=>{if(finishReceipt)return finishReceipt;
  const expectedLife=lifecycleToken, admission=beginReceipt;boundary=nextBoundary;
  finishReceipt=(async()=>{
   if(await admission!==true||expectedLife!==lifecycleToken)return;
   await drain();if(expectedLife!==lifecycleToken)return;
   if(enabled)for(const metric of ['fcp','lcp','cls','inp','ttfb']){
    if(expectedLife!==lifecycleToken)return;
    await send('metric',metric,'final',values.has(metric)?'':metricReason(metric),nextBoundary);
   }
   if(expectedLife!==lifecycleToken)return;
   terminal=true;await send('end','','final','',nextBoundary);
  })();return finishReceipt;
 };
 Object.defineProperty(finish,'identity',{value:()=>({document_token:documentToken,lifecycle_token:lifecycleToken}),configurable:false});
 Object.defineProperty(globalThis,finalizer,{value:finish,configurable:false});
 addEventListener('keydown',event=>{if(event.isTrusted){firstInput=true;boundary='first_input';if(enabled)queueMicrotask(()=>void finalizeLCP('first_input'))}},{capture:true});
 addEventListener('click',event=>{if(event.isTrusted){firstInput=true;boundary='first_input';if(enabled)queueMicrotask(()=>void finalizeLCP('first_input'))}},{capture:true});
 addEventListener('visibilitychange',()=>{if(document.visibilityState==='hidden'){boundary='hidden';queueMicrotask(()=>{if(!terminal&&enabled){for(const metric of ['cls','inp'])if(values.has(metric))send('metric',metric,'provisional');void finalizeLCP('hidden')}})}else boundary='progress'},{capture:true});
 addEventListener('pagehide',()=>{void finish('pagehide')},{capture:true});
})()`, strconv.Quote(binding), strconv.Quote(finalizer), strconv.Quote(loader), metrics, resources, browserVitalsLibrary)
}
