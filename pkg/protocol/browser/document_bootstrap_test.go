package browser

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestBrowserVitalsPinnedLibraryDigest(t *testing.T) {
	digest := sha256.Sum256([]byte(browserVitalsLibrary))
	if hex.EncodeToString(digest[:]) != protocol.BrowserVitalsAlgorithmSHA256 {
		t.Fatal("reviewed web-vitals source digest changed")
	}
}
func TestBrowserDocumentBootstrapOfficialSessionWindowAndInteractionRank(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node is unavailable for the isolated algorithm adapter unit test")
	}
	harness := `const vm=require('node:vm'); const assert=require('node:assert/strict');const observers=[],listeners=new Map(),payloads=[];
 class NativeObserver {static supportedEntryTypes=['paint','layout-shift','event','largest-contentful-paint'];constructor(callback){this.callback=callback;this.records=[];observers.push(this)}observe(options){this.types??=[];this.types.push(options.type)}disconnect(){}takeRecords(){return this.records.splice(0)}}
 const performance={timeOrigin:1710000000000,now:()=>10000,interactionCount:49,getEntriesByType:type=>type==='navigation'?[{responseStart:50,activationStart:0}]:[]};
 class PerformanceEventTiming{};PerformanceEventTiming.prototype.interactionId=0;
 const context={console,performance,PerformanceObserver:NativeObserver,PerformanceEventTiming,Uint8Array,crypto:require('node:crypto').webcrypto,document:{visibilityState:'visible',readyState:'complete'},addEventListener:(name,fn)=>{if(!listeners.has(name))listeners.set(name,[]);listeners.get(name).push(fn)},removeEventListener:()=>{},requestAnimationFrame:fn=>setTimeout(fn,0),setTimeout,queueMicrotask,Promise,Map,Set,Math,Date,Infinity,__binding:payload=>{payloads.push(payload);return Promise.resolve(true)}};
 // A native Window getter rejects an inherited receiver. This fixture
 // enforces that brand without replacing the pinned metric algorithm.
 Object.defineProperty(context,'performance',{get(){if(Object.getPrototypeOf(this)===context)throw new Error('Illegal Window getter receiver');return performance}});
 context.window=context;context.globalThis=context;vm.createContext(context);
 vm.runInContext(SCRIPT,context);
 const tick=()=>new Promise(resolve=>setTimeout(resolve,10));
 const deliver=(type,entries)=>{for(const observer of [...observers])if(observer.types?.includes(type))observer.callback({getEntries:()=>entries})};
 (async()=>{deliver('paint',[{name:'first-contentful-paint',startTime:100}]);await tick();
 deliver('layout-shift',[{hadRecentInput:false,value:0.1,startTime:100},{hadRecentInput:false,value:0.2,startTime:500},{hadRecentInput:false,value:0.5,startTime:2000}]);
 deliver('event',[{entryType:'event',interactionId:7,duration:1000,startTime:10},{entryType:'event',interactionId:70,duration:300,startTime:20},{entryType:'event',interactionId:70,duration:400,startTime:20},{entryType:'event',interactionId:350,duration:350,startTime:30}]);await tick();
 const latest=metric=>payloads.filter(p=>p.metric===metric).at(-1);
 assert.equal(latest('cls').value,0.5,'CLS must retain the largest session, not sum all shifts');
 assert.equal(latest('inp').value,1000,'49 interactions retain the highest grouped interaction; IDs need not be contiguous');
 assert.equal(latest('inp').interaction_count,49);
 context.document.visibilityState='hidden';for(const listener of listeners.get('visibilitychange')||[])listener({type:'visibilitychange',isTrusted:true,timeStamp:10000});await tick();
 assert.equal(latest('cls').state,'provisional');assert.equal(latest('inp').state,'provisional');
 performance.interactionCount=50; // native count settles after the final event callback, with no new entry
 await context.__finalizer('graceful_end');assert.equal(latest('cls').state,'final');assert.equal(latest('inp').state,'final');assert.equal(latest('inp').value,400,'final drain must recompute the pinned retained rank at the settled native count');
 const finals=payloads.filter(p=>p.kind==='metric'&&p.state==='final').length;await context.__finalizer('graceful_end');assert.equal(payloads.filter(p=>p.kind==='metric'&&p.state==='final').length,finals,'repeat finalization must not republish');
 const oldLife=latest('ttfb').lifecycle_token, oldDocument=latest('ttfb').document_token;
 context.document.visibilityState='visible';for(const listener of listeners.get('pageshow')||[])listener({persisted:true,timeStamp:9500});await tick();
 assert.equal(latest('ttfb').value,0,'native restored TTFB zero must be observed');assert.equal(latest('ttfb').restored,true);assert.notEqual(latest('ttfb').lifecycle_token,oldLife,'restored callback must receive new lifecycle before synchronous metric');assert.equal(latest('ttfb').document_token,oldDocument,'BFCache retains its physical document identity');
 await context.__finalizer('graceful_end');assert.equal(latest('inp').reason,'no_interaction','new lifecycle cannot inherit old interaction count');
 console.log('official-algorithm-adapter-unit PASS');})().catch(error=>{console.error(error);process.exitCode=1});`
	encoded := strings.ReplaceAll(harness, "SCRIPT", javascriptSourceQuote(browserDocumentBootstrap("__binding", "__finalizer", "__loader", true, false)))
	output, err := exec.Command(node, "-e", encoded).CombinedOutput()
	if err != nil {
		t.Fatalf("algorithm adapter unit: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "official-algorithm-adapter-unit PASS") {
		t.Fatal("missing explicit unit completion")
	}
}

func javascriptSourceQuote(s string) string { b, _ := json.Marshal(s); return string(b) }
