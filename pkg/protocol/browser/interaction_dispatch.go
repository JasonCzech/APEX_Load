package browser

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// browserInteractionDispatchPage is an observer's first relevant trusted input.
// Geometry is read in that event's capture listener, before target handlers.
type browserInteractionDispatchPage struct {
	Action  string  `json:"action"`
	X       float64 `json:"x"`
	Y       float64 `json:"y"`
	Pointer string  `json:"pointer"`

	Origin    float64 `json:"origin"`
	Offset    float64 `json:"offset"`
	Document  string  `json:"document"`
	Lifecycle string  `json:"lifecycle"`
	VW        int     `json:"vw"`
	VH        int     `json:"vh"`
	SX        float64 `json:"sx"`
	SY        float64 `json:"sy"`
	DPR       float64 `json:"dpr"`
	Path      string  `json:"path"`
	Layout    string  `json:"layout"`
	Role      string  `json:"role"`
	Label     string  `json:"label"`
}

func (b *Browser) enrichBrowserInteractionDispatch(i *protocol.BrowserInteraction, action *protocol.Action, page browserInteractionPage, start *browserInteractionStart) {
	if !protocol.BrowserInteractionHasDispatch(b.interactionVersion) {
		return
	}
	i.Version = b.interactionVersion
	i.AuthoredStep = action.BrowserAuthoredStep.Clone()
	if i.AuthoredStep != nil {
		i.Execution = action.BrowserActionExecution.Clone()
	}
	i.DispatchUnavailableReason = "not_observed"
	if i.FrameScope == "child_frame" {
		i.DispatchUnavailableReason = protocol.InteractionUnavailableChildFrame
		return
	}
	if b.syntheticGesture(i.Kind) {
		i.DispatchUnavailableReason = protocol.InteractionUnavailableSyntheticDispatch
		return
	}
	if start == nil || !start.read {
		i.DispatchUnavailableReason = protocol.InteractionUnavailablePageState
		return
	}
	if page.Dispatch == nil {
		if i.UnavailableReason == protocol.InteractionUnavailableDocumentChanged || i.UnavailableReason == protocol.InteractionUnavailablePageState {
			i.DispatchUnavailableReason = i.UnavailableReason
		}
		return
	}
	d := page.Dispatch
	if d.Action != i.ActionID {
		i.DispatchUnavailableReason = protocol.InteractionDispatchActionUnavailable
		return
	}
	at, ok := browserEvidenceTime(d.Origin, d.Offset)
	if !ok || math.IsNaN(d.Origin+d.Offset) {
		i.DispatchUnavailableReason = protocol.InteractionDispatchClockUnavailable
		return
	}
	var documentID, lifecycleID string
	b.mu.Lock()
	for _, document := range b.evidence.documents {
		if document.token == d.Document && document.lifecycleToken == d.Lifecycle {
			documentID, lifecycleID = document.documentID, document.lifecycleID
			break
		}
	}
	b.mu.Unlock()
	dispatch := &protocol.BrowserInteractionDispatch{At: at, TimeOriginMS: d.Origin, OffsetMS: d.Offset, DocumentID: documentID, LifecycleID: lifecycleID}
	if !dispatch.Valid(i.SourceGeneration) {
		i.DispatchUnavailableReason = protocol.InteractionDispatchDocumentUnavailable
		return
	}
	i.Dispatch, i.DispatchUnavailableReason, i.DocumentID = dispatch, "", documentID
	i.ViewportWidth, i.ViewportHeight, i.ScrollX, i.ScrollY, i.DeviceScaleFactor = d.VW, d.VH, d.SX, d.SY, d.DPR
	i.LayoutRevision = d.Layout
	if protocol.BrowserInteractionPointerKind(i.Kind) && len(i.Points) > 0 {
		i.Points[0] = protocol.BrowserInteractionPoint{X: d.X, Y: d.Y}
		i.PointerType = d.Pointer
	}
	i.Route = sanitizeBrowserRoute(b.redactRuntimeText(d.Path))
	if i.Kind != "scroll" && !protocol.BrowserInteractionPointerKind(i.Kind) || i.Kind == "check" {
		i.Target = nil
		if d.Role != "" {
			i.Target = &protocol.BrowserInteractionTarget{Role: d.Role}
			if d.Label != "" {
				sum := sha256.Sum256([]byte(b.redactRuntimeText(d.Label)))
				i.Target.LabelSHA256 = hex.EncodeToString(sum[:])
			}
			if i.UnavailableReason == protocol.InteractionUnavailableTarget {
				i.UnavailableReason = ""
			}
		}
	}
}

// browserInteractionObserverV2 leaves v1's capture path unchanged. The v2
// fingerprint is bounded before enqueueing children; it never walks an
// unbounded sibling list or records text, URLs, selectors or attribute values.
func browserInteractionObserverV2(name, finalizer string) string {
	return fmt.Sprintf(`(() => {
 "use strict";
 const name=%q, finalizer=%q;
 if(Object.prototype.hasOwnProperty.call(globalThis,name))return;
 const a=new Uint8Array(16);crypto.getRandomValues(a);
 const token=Array.from(a,x=>x.toString(16).padStart(2,'0')).join('');
 let events=[],move=null,first=null,kind='',action='';
 const hash=s=>{let h1=0x811c9dc5,h2=0x050c5d1f;for(let i=0;i<s.length;i++){const c=s.charCodeAt(i);h1=Math.imul(h1^c,0x01000193);h2=Math.imul(h2^c,0x01000193)}return(h1>>>0).toString(16).padStart(8,'0')+(h2>>>0).toString(16).padStart(8,'0')};
 const layout=()=>{
  const parts=[],queue=document.documentElement?[document.documentElement]:[];
  for(let n=0;n<queue.length&&n<1024;n++){
   const el=queue[n],r=el.getBoundingClientRect(),role=(el.getAttribute('role')||'').slice(0,25).toLowerCase(),q=v=>Math.round(v/32);
   parts.push(el.localName.slice(0,64)+'|'+(/^[a-z]{1,24}$/.test(role)?role:'')+'|'+q(r.left+scrollX)+','+q(r.top+scrollY)+','+q(r.width)+','+q(r.height));
   for(let c=0;c<el.children.length&&queue.length<1024;c++)queue.push(el.children[c]);
  }
  return hash(parts.join(';'));
 };
 const labelText=el=>{
  const walk=document.createTreeWalker(el,NodeFilter.SHOW_ALL);let text='';
  for(let n=0;n<64&&text.length<128;n++){const node=walk.nextNode();if(!node)break;if(node.nodeType===3)text+=node.substringData(0,128-text.length)}
  return text;
 };
 const describe=el=>{
  if(!el||!el.getAttribute)return null;
  const tag=el.localName.slice(0,64),type=(el.getAttribute('type')||'text').slice(0,32).toLowerCase(),explicit=(el.getAttribute('role')||'').slice(0,25).toLowerCase();
  const implicit=tag==='input'?({checkbox:'checkbox',radio:'radio',button:'button',submit:'button',reset:'button',range:'slider',file:'button',search:'searchbox'}[type]||'textbox'):({textarea:'textbox',select:'combobox',button:'button',a:'link',option:'option'}[tag]||'generic');
  let label=(el.getAttribute('aria-label')||'').slice(0,128);
  if(!label&&el.labels&&el.labels.length)label=labelText(el.labels[0]);
  if(!label)label=(el.getAttribute('placeholder')||el.getAttribute('name')||el.getAttribute('title')||'').slice(0,128);
  return{role:/^[a-z]{1,24}$/.test(explicit)?explicit:implicit,label:label.toLowerCase().replace(/[0-9]/g,'#').replace(/\s+/g,' ').trim().slice(0,64)};
 };
 const geometry=()=>({vw:innerWidth,vh:innerHeight,sx:scrollX,sy:scrollY,dpr:devicePixelRatio,path:location.pathname,layout:layout()});
 const relevant=e=>kind==='hover'?e.type==='pointermove':kind==='key'?e.type==='keydown':['fill','select','set_files'].includes(kind)?['beforeinput','input','change'].includes(e.type):['click','dblclick','check','drag','tap','long_press','swipe'].includes(kind)&&e.type==='pointerdown';
 const record=e=>{
  if(!e.isTrusted||!kind)return;
  if(e.type.startsWith('pointer')){
   const p={t:e.type,x:e.clientX,y:e.clientY,p:e.pointerType||''};
   if(e.type==='pointermove')move=p;else{if(events.length>=32)events.shift();events.push(p)}
  }
  if(!first&&relevant(e)){
   const identity=globalThis[finalizer]?.identity?.(),target=describe(e.target);
   first={action,...geometry(),x:Number.isFinite(e.clientX)?e.clientX:0,y:Number.isFinite(e.clientY)?e.clientY:0,pointer:e.pointerType||'',origin:performance.timeOrigin,offset:e.timeStamp,document:identity?.document_token||'',lifecycle:identity?.lifecycle_token||'',role:target?.role||'',label:target?.label||''};
  }
 };
 for(const t of ['pointerdown','pointerup','pointermove','keydown','beforeinput','input','change'])addEventListener(t,record,{capture:true,passive:true});
 Object.defineProperty(globalThis,name,{configurable:false,enumerable:false,value:{drain(light,nextKind,nextAction){
  const out={doc:token,events,move,dispatch:first,...(light?{vw:innerWidth,vh:innerHeight,sx:scrollX,sy:scrollY,dpr:devicePixelRatio,path:location.pathname,layout:''}:geometry()),focus:describe(document.activeElement)};
  events=[];move=null;first=null;kind=light?(nextKind||''):'';action=light?(nextAction||''):'';
  return out;
 }}});
})()`, name, finalizer)
}

func (*Browser) SupportsBrowserInteractionEvidenceV2() {}

func (*Browser) SupportsBrowserInteractionEvidenceV3() {}
