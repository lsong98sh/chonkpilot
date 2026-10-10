const __vite__mapDeps=(i,m=__vite__mapDeps,d=(m.f||(m.f=["./docx-preview-D3ByrUMo.js","./jszip.min-i4C6LgBk.js","./_commonjsHelpers-BosuxZz1.js"])))=>i.map(i=>d[i]);
import{D as $}from"./app-DgrBOy8j.js";import{r as B,a as G}from"./assets-BLedw5Zj.js";import{j as J,r as Y,m as j,N as K,p as Q,q}from"./CodeView-xWZtZ2Mi.js";import{b as ee,f as W,a as oe,g as A}from"./printLayout-DpvXGlU9.js";import"./Tabs-CMXk306k.js";const x={width:794,height:1123},ie=new Set(["file:","about:","data:"]),S=.24,k=3,R=.15,te="0.3.19",re=20555,ne=(()=>{const i={module:null,async load(){return this.module||(this.module=$(()=>import("./docx-preview-D3ByrUMo.js"),__vite__mapDeps([0,1,2]),import.meta.url)),this.module}};return async()=>await i.load()})(),ae=(i,o)=>{if((i.byteLength>=4?new DataView(i).getUint16(0,!1):0)!==re)throw new Error(J(o==null?void 0:o.options)("word.error.invalidDocx"))},y=i=>i.ownerDocument.defaultView,de=i=>{var o,t,r;const a=[i.ownerDocument.URL,(t=(o=y(i))===null||o===void 0?void 0:o.location)===null||t===void 0?void 0:t.href,(r=globalThis.location)===null||r===void 0?void 0:r.href].filter(Boolean);for(const e of a)try{return new URL(e).protocol}catch{}return""},le=(i,o)=>{var t,r;return(o==null?void 0:o.worker)===!1?!1:(o==null?void 0:o.worker)===!0?!0:((r=(t=y(i))===null||t===void 0?void 0:t.Worker)!==null&&r!==void 0?r:globalThis.Worker)?!ie.has(de(i)):!1},se=i=>{var o;const t=y(i),r=(o=t==null?void 0:t.matchMedia)!==null&&o!==void 0?o:globalThis.matchMedia;return typeof r=="function"&&r("(prefers-color-scheme: dark)").matches},ce=(i,o,t)=>{var r;if((t==null?void 0:t.darkMode)!==void 0)return t.darkMode;const a=K((r=o==null?void 0:o.options)===null||r===void 0?void 0:r.theme);return a==="dark"?!0:a==="light"?!1:se(i)},H=(i,o)=>!i||o?i:`${i}${i.includes("?")?"&":"?"}file-viewer-docx=${te}`,ue=(i,o,t)=>{var r,a;const e=(r=o==null?void 0:o.options)===null||r===void 0?void 0:r.docx,c=i.ownerDocument.URL||void 0,l=le(i,e),g=(e==null?void 0:e.visualPagination)===!0,p=ce(i,o,e),s=m=>{(m.phase==="render"||m.phase==="layout"||m.phase==="done")&&t()},d={useWorker:l,breakPages:g,ignoreLastRenderedPageBreak:(a=e==null?void 0:e.ignoreLastRenderedPageBreak)!==null&&a!==void 0?a:!g,darkMode:p,progress:s};return l&&(d.workerUrl=H(B(e,c),!!(e!=null&&e.workerUrl)),d.workerJsZipUrl=H(G(e,c),!!(e!=null&&e.workerJsZipUrl))),(e==null?void 0:e.workerTimeout)!==void 0&&(d.workerTimeout=e.workerTimeout),(e==null?void 0:e.renderPageBatchSize)!==void 0?d.renderPageBatchSize=e.renderPageBatchSize:(e==null?void 0:e.progressive)===!1&&(d.renderPageBatchSize=Number.MAX_SAFE_INTEGER),(e==null?void 0:e.renderYieldEveryMs)!==void 0&&(d.renderYieldEveryMs=e.renderYieldEveryMs),(e==null?void 0:e.strictWordCompatibility)!==void 0&&(d.strictWordCompatibility=e.strictWordCompatibility),(e==null?void 0:e.paginationTolerance)!==void 0&&(d.paginationTolerance=e.paginationTolerance),(e==null?void 0:e.maxDynamicPaginationPasses)!==void 0&&(d.maxDynamicPaginationPasses=e.maxDynamicPaginationPasses),(e==null?void 0:e.awaitLayout)!==void 0&&(d.awaitLayout=e.awaitLayout),(e==null?void 0:e.preserveComplexFieldResults)!==void 0&&(d.preserveComplexFieldResults=e.preserveComplexFieldResults),(e==null?void 0:e.updatePageReferences)!==void 0&&(d.updatePageReferences=e.updatePageReferences),(e==null?void 0:e.hideWebHiddenContent)!==void 0&&(d.hideWebHiddenContent=e.hideWebHiddenContent),d},O=(i,o)=>{var t;const r=(t=y(o))===null||t===void 0?void 0:t.HTMLElement;return r?i instanceof r:i instanceof HTMLElement},fe=`
.docx-fit-viewer {
  box-sizing: border-box;
  height: 100%;
  overflow: auto;
  background: #ececec;
  color-scheme: light;
}
.docx-fit-viewer[data-docx-dark-mode='true'] {
  background: #242424;
  color-scheme: dark;
}
.docx-fit-viewer .docx-wrapper {
  box-sizing: border-box;
  min-width: 0 !important;
  width: 100% !important;
  padding: 24px 14px 40px !important;
  background: #e7e9ec !important;
}
.docx-fit-viewer[data-docx-dark-mode='true'] .docx-wrapper {
  background: #242424 !important;
}
.docx-fit-viewer .docx-page-frame {
  position: relative;
  width: 100%;
  min-width: 0;
  margin: 0 auto 24px;
  overflow: visible;
}
.docx-fit-viewer .docx-flow-frame {
  position: relative;
  width: 100%;
  min-width: 0;
  margin: 0 auto 28px;
  overflow: visible;
}
.docx-fit-viewer .docx-page-frame > section.docx,
.docx-fit-viewer .docx-flow-frame > section.docx {
  position: absolute;
  top: 0;
  left: 50%;
  margin: 0 !important;
  background: #ffffff !important;
  box-shadow: 0 2px 14px rgba(25, 35, 48, 0.18);
  box-sizing: border-box;
  overflow: hidden;
  transform-origin: top center;
}
.docx-fit-viewer[data-docx-dark-mode='true'] .docx-page-frame > section.docx,
.docx-fit-viewer[data-docx-dark-mode='true'] .docx-flow-frame > section.docx {
  background: rgb(51, 51, 51) !important;
  box-shadow: 0 0 10px rgba(0, 0, 0, 0.8);
  outline: 1px solid rgba(255, 255, 255, 0.15);
  outline-offset: -1px;
}
.docx-fit-viewer .docx-flow-frame > section.docx {
  height: auto !important;
  min-height: var(--docx-page-height, auto) !important;
  overflow: visible !important;
}
.docx-fit-viewer .docx-page-frame > section.docx > article,
.docx-fit-viewer .docx-flow-frame > section.docx > article {
  position: relative;
  z-index: 1;
}
`;function me(i){const o=i.ownerDocument.createElement("style");return o.textContent=fe,i.prepend(o),o}function ve(i,o){const t=i.querySelector(".docx-wrapper");return t?Array.from(t.children).flatMap(r=>{if(!O(r,i)||!r.matches("section.docx"))return[];const a=i.ownerDocument.createElement("div");return a.className=o?"docx-page-frame":"docx-flow-frame",r.before(a),a.appendChild(r),[a]}):[]}function he(i,o){var t,r;i.classList.add("docx-fit-viewer");const a=me(i),e=((r=(t=o==null?void 0:o.options)===null||t===void 0?void 0:t.docx)===null||r===void 0?void 0:r.visualPagination)===!0,c=ve(i,e),l=y(i),g=l==null?void 0:l.ResizeObserver;let p=0,s=1,d=1,m=1;const L=Q(),Z=n=>Math.min(k,Math.max(S,Number(n.toFixed(2)))),M=()=>{let n=d,u=m,f=!1;c.forEach(w=>{const h=w.firstElementChild;if(!O(h,i))return;h.style.transform="translateX(-50%)";const b=h.offsetWidth,E=e?h.offsetHeight:Math.max(h.scrollHeight,h.offsetHeight);if(!b||!E)return;const I=Math.max(i.clientWidth-28,120),T=Math.min(1,Math.max(S,I/b)),_=Z(T*s);f||(n=_,u=T,f=!0),h.style.transform=`translateX(-50%) scale(${_})`,w.style.width=`${Math.ceil(Math.max(b*_,i.clientWidth-28,120))}px`,w.style.maxWidth="none",w.style.height=`${Math.ceil(E*_)}px`}),f&&(d=n,m=u,L.emit())},N=()=>{if(!l){M();return}l.cancelAnimationFrame(p),p=l.requestAnimationFrame(()=>{M()})},z=()=>({scale:d,label:`${Math.round(d*100)}%`,canZoomIn:d<k,canZoomOut:d>S,canReset:s!==1,minScale:S,maxScale:k}),P=n=>(s=Math.min(6,Math.max(.2,Number(n.toFixed(2)))),l==null||l.cancelAnimationFrame(p),M(),z()),F=n=>P(n/Math.max(m,.01)),U=()=>{for(const n of c){const u=D(n);if(!u)continue;const f=A(u,x);return{width:u.offsetWidth||f.width||x.width,height:C(n)?x.height:u.offsetHeight||f.height||x.height}}return null},X=n=>{var u,f;const w=U();if(!w)return{applied:!1,mode:n.mode,resize:n.resize,source:n.source,reason:"unmeasurable",provider:"zoom"};const h=n.mode==="auto"?"width":n.mode,b=q({mode:h,viewportWidth:Math.max(1,n.viewportWidth||i.clientWidth||0),viewportHeight:Math.max(1,n.viewportHeight||i.clientHeight||0),contentWidth:w.width,contentHeight:w.height,currentScale:d,minScale:(u=n.minScale)!==null&&u!==void 0?u:S,maxScale:(f=n.maxScale)!==null&&f!==void 0?f:k});if(!b)return{applied:!1,mode:n.mode,resize:n.resize,source:n.source,reason:"unmeasurable",provider:"zoom"};const E=F(b);return{applied:!0,mode:n.mode,resize:n.resize,scale:E.scale,source:n.source,provider:"zoom"}};i.dataset.viewerZoomProvider="docx",Y(i,{zoomIn:()=>P((d+R)/Math.max(m,.01)),zoomOut:()=>P((d-R)/Math.max(m,.01)),resetZoom:()=>P(1),setZoom:F,fit:X,getState:z,subscribe:L.subscribe});const v=g?new g(N):null;return v==null||v.observe(i),c.forEach(n=>{const u=D(n);u&&(v==null||v.observe(u))}),M(),()=>{l==null||l.cancelAnimationFrame(p),v==null||v.disconnect(),j(i),a.remove(),i.classList.remove("docx-fit-viewer")}}function D(i){var o;const t=i.firstElementChild,r=(o=i.ownerDocument.defaultView)===null||o===void 0?void 0:o.HTMLElement;return r&&t instanceof r?t:null}function C(i){return!!(i!=null&&i.classList.contains("docx-flow-frame"))}function V(i){const o=i?D(i):null;if(!o)return x;const t=A(o,x);return C(i)?{width:t.width,height:Math.max(o.scrollHeight||0,o.offsetHeight||0,x.height)}:t}function pe(i,o){const t=C(i),r=W(o.width),a=W(o.height);oe(i,o,{heightMode:t?"min":"fixed"}),i.style.margin="0 auto 18px";const e=D(i);e&&(e.style.position="relative",e.style.top="auto",e.style.left="auto",e.style.width=r,e.style.maxWidth="none",e.style.minHeight=t?"0":a,e.style.height=t?"auto":a,e.style.margin="0 auto",e.style.transform="none",e.style.transformOrigin="top left",e.style.overflow=t?"visible":"hidden",e.style.boxShadow="none")}function we(i){const o=i.querySelector(".docx-page-frame, .docx-flow-frame"),t=V(o||void 0),r=o!=null&&o.classList.contains("docx-flow-frame")?".viewer-export-content .docx-flow-frame":".viewer-export-content .docx-page-frame";return ee({selector:r,width:t.width,height:o!=null&&o.classList.contains("docx-flow-frame")?x.height:t.height,heightMode:o!=null&&o.classList.contains("docx-flow-frame")?"min":"fixed"})}function xe(i){const o=Array.from(i.querySelectorAll(".docx-page-frame, .docx-flow-frame")),t=i.cloneNode(!0),r=i.ownerDocument.createElement("div");r.className="docx-print-document";const a=Array.from(t.querySelectorAll("style")).filter(e=>{var c;return!(!((c=e.textContent)===null||c===void 0)&&c.includes(".docx-fit-viewer"))}).map(e=>e.outerHTML).join("");return t.querySelectorAll(".docx-page-frame, .docx-flow-frame").forEach((e,c)=>{pe(e,V(o[c])),r.appendChild(e.cloneNode(!0))}),r.childElementCount?`${a}${r.outerHTML}`:t.innerHTML}async function Pe(i,o,t){var r;ae(i,t),o.innerHTML="";let a=!1;const e=()=>{var s;a||(a=!0,(s=t==null?void 0:t.onProgressiveRender)===null||s===void 0||s.call(t))},c=ue(o,t,e),{defaultOptions:l,renderAsync:g}=await ne();o.dataset.docxWorker=c.useWorker?"self":"false",o.dataset.docxDarkMode=c.darkMode?"true":"false",await g(i,o,void 0,{...l,...c}),e();const p=he(o,t);return(r=t==null?void 0:t.registerExportAdapter)===null||r===void 0||r.call(t,{includeDocumentStyles:!1,beforeSnapshot:()=>{const s=y(o);s&&s.dispatchEvent(new s.Event("resize"))},printStyle:()=>we(o),toHtml:()=>xe(o)}),{$el:o,unmount(){var s;(s=t==null?void 0:t.registerExportAdapter)===null||s===void 0||s.call(t,null),p(),delete o.dataset.docxWorker,delete o.dataset.docxDarkMode,o.innerHTML=""}}}export{Pe as default};
