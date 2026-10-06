package browser

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
	playwright "github.com/mxschmitt/playwright-go"
)

// The actual generated bootstrap runs in an isolated JS VM. Binding gates
// model transport acknowledgments; the harness has no network/native driver.
func TestBrowserDocumentBootstrapJoinsLifecycleReceipts(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		if os.Getenv("APEXLOAD_BROWSER_TESTS") == "require" {
			t.Fatal("required bootstrap VM Node runtime unavailable")
		}
		t.Skip("Node is unavailable for isolated bootstrap receipt test")
	}
	for _, mode := range []string{"begin", "join", "restore", "begin_false"} {
		t.Run(mode, func(t *testing.T) {
			harness := `const vm=require('node:vm'),assert=require('node:assert/strict');
const mode=MODE,listeners=new Map(),payloads=[];
const deferred=()=>{let resolve;const promise=new Promise(r=>resolve=r);return {promise,resolve}};
const begin=deferred(),metric=deferred();let heldMetric=false;
const context={performance:{timeOrigin:1710000000000,now:()=>100,interactionCount:0},Uint8Array,crypto:require('node:crypto').webcrypto,document:{visibilityState:'visible',readyState:'complete'},addEventListener:(name,fn)=>{if(!listeners.has(name))listeners.set(name,[]);listeners.get(name).push(fn)},removeEventListener:()=>{},setTimeout,queueMicrotask,Promise,Map,Set,Math,Date,Infinity,__binding:p=>{payloads.push(p);if(p.kind==='begin'&&mode==='begin')return begin.promise;if(p.kind==='begin'&&mode==='begin_false')return Promise.resolve(false);if(p.kind==='metric'&&p.state==='final'&&!heldMetric&&(mode==='join'||mode==='restore')){heldMetric=true;return metric.promise}return Promise.resolve(true)}};
context.window=context;context.globalThis=context;vm.createContext(context);vm.runInContext(SCRIPT,context);
const tick=()=>new Promise(resolve=>setImmediate(resolve));
const event=name=>{for(const listener of listeners.get(name)||[])listener({persisted:true,isTrusted:true})};
const finals=life=>payloads.filter(p=>p.kind==='metric'&&p.state==='final'&&(!life||p.lifecycle_token===life));
const ends=life=>payloads.filter(p=>p.kind==='end'&&(!life||p.lifecycle_token===life));
(async()=>{try {
 if(mode==='begin') {const finished=context.__finalizer('graceful_end');await tick();assert.equal(finals().length,0,'final metrics overtook begin admission');assert.equal(ends().length,0);begin.resolve(true);await finished;assert.equal(finals().length,5);assert.equal(ends().length,1)}
 if(mode==='begin_false'){await context.__finalizer('graceful_end');assert.equal(finals().length,0,'rejected begin produced final receipts');assert.equal(ends().length,0)}
 if(mode==='join'){event('pagehide');await tick();assert.equal(finals().length,1,'first finish must be suspended on a metric acknowledgment');let joined=false;const finishing=context.__finalizer('graceful_end').then(()=>joined=true);await tick();assert.equal(joined,false,'second finish returned before the active finish joined');metric.resolve(true);await finishing;assert.equal(finals().length,5);assert.equal(ends().length,1);assert.equal(finals().every(p=>p.boundary==='pagehide'),true,'late join revised the first boundary')}
 if(mode==='restore'){event('pagehide');await tick();assert.equal(finals().length,1);const oldLife=payloads[0].lifecycle_token;event('pageshow');await tick();const newLife=payloads.filter(p=>p.kind==='begin').at(-1).lifecycle_token;assert.notEqual(newLife,oldLife);metric.resolve(true);await tick();await tick();assert.equal(finals(newLife).length,0,'old finish emitted under restored lifecycle');assert.equal(ends(newLife).length,0,'old finish terminalized restored lifecycle');await context.__finalizer('graceful_end');assert.equal(finals(newLife).length,5);assert.equal(ends(newLife).length,1)}
 console.log('bootstrap-receipt-'+mode+' PASS');
 }finally{begin.resolve(true);metric.resolve(true)}})().catch(error=>{console.error(error.message);process.exitCode=1});`
			harness = strings.ReplaceAll(harness, "MODE", javascriptSourceQuote(mode))
			harness = strings.ReplaceAll(harness, "SCRIPT", javascriptSourceQuote(browserDocumentBootstrap("__binding", "__finalizer", true, false)))
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			output, err := exec.CommandContext(ctx, node, "-e", harness).CombinedOutput()
			if err != nil {
				t.Fatalf("bootstrap lifecycle %s: %v\n%s", mode, err, output)
			}
			if !strings.Contains(string(output), "bootstrap-receipt-"+mode+" PASS") {
				t.Fatal("bootstrap lifecycle completion absent")
			}
		})
	}
}

type browserFinalityCurrentPage struct {
	playwright.Page
	frames []playwright.Frame
}

func (p *browserFinalityCurrentPage) Frames() []playwright.Frame { return p.frames }

type browserFinalityCurrentContext struct {
	playwright.BrowserContext
	pages []playwright.Page
}

func (c *browserFinalityCurrentContext) Pages() []playwright.Page { return c.pages }
func (c *browserFinalityCurrentContext) Close(...playwright.BrowserContextCloseOptions) error {
	return nil
}

// The existing held-worker fixture intentionally has no extra native pages.
func (c *browserFinalizationOwnedContext) Pages() []playwright.Page { return nil }

func TestBrowserDocumentFinalizeIncludesCurrentFrameBeforeBeginAcknowledgment(t *testing.T) {
	page := &browserFinalityCurrentPage{}
	b := &Browser{evidence: browserEvidenceState{finalizer: "synthetic", documents: map[string]*browserDocumentState{}}}
	mainDoc := &browserDocumentState{page: page, observations: map[string]protocol.BrowserDocumentObservation{"inp": {State: "provisional"}}}
	main := &finalizerFrameIsolationFixture{evaluate: func() error {
		mainDoc.observations["inp"] = protocol.BrowserDocumentObservation{State: "final"}
		return nil
	}}
	mainDoc.frame = main
	b.evidence.documents["main"] = mainDoc
	attempts := 0
	child := &finalizerFrameIsolationFixture{evaluate: func() error {
		attempts++
		b.mu.Lock()
		defer b.mu.Unlock()
		b.evidence.documents["late-child"] = &browserDocumentState{page: page, observations: map[string]protocol.BrowserDocumentObservation{"inp": {State: "final", Boundary: "graceful_end", UnavailableReason: "no_interaction"}}}
		return nil
	}}
	page.frames = []playwright.Frame{main, child, main}
	b.pwContext = &browserFinalityCurrentContext{pages: []playwright.Page{page, page}}
	b.finalizeDocumentEvidence(context.Background())
	if attempts != 1 || b.evidence.documents["late-child"].observations["inp"].State != "final" {
		t.Fatal("current native child absent from admitted document map was never finalized")
	}
}

func TestBrowserDocumentCurrentFrameMergeIsBoundedAndDeduplicated(t *testing.T) {
	current := &finalizerFrameIsolationFixture{}
	retained := &finalizerFrameIsolationFixture{}
	page := &browserFinalityCurrentPage{frames: []playwright.Frame{nil, current, current}}
	b := &Browser{}
	merged := b.mergeCurrentDocumentFrames(&browserFinalityCurrentContext{pages: []playwright.Page{nil, page, page}}, []playwright.Frame{nil, retained, current, retained})
	if len(merged) != 2 || merged[0] != current || merged[1] != retained || b.evidence.incomplete {
		t.Fatal("native priority, retained fallback or deduplication failed")
	}
	all := make([]playwright.Frame, browserFrameIdentityLimit+1)
	for i := range all {
		all[i] = &finalizerFrameIsolationFixture{}
	}
	b = &Browser{}
	merged = b.mergeCurrentDocumentFrames(&browserFinalityCurrentContext{pages: []playwright.Page{&browserFinalityCurrentPage{frames: all}}}, []playwright.Frame{retained})
	if len(merged) != browserFrameIdentityLimit || merged[0] != all[0] || merged[len(merged)-1] != all[browserFrameIdentityLimit-1] || !b.evidence.incomplete {
		t.Fatal("native frame limit or coverage indicator missing")
	}
	b = &Browser{}
	merged = b.mergeCurrentDocumentFrames(&browserFinalityCurrentContext{}, all)
	if len(merged) != browserFrameIdentityLimit || cap(merged) > browserFrameIdentityLimit || !b.evidence.incomplete {
		t.Fatal("retained fallback allocation was not bounded")
	}
	b = &Browser{}
	if got := b.mergeCurrentDocumentFrames(&browserFinalityCurrentContext{}, nil); len(got) != 0 || b.evidence.incomplete {
		t.Fatal("empty native context became incomplete")
	}
}
