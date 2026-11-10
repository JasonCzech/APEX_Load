function e(e){let t=typeof e;return e!==void 0&&t!==`function`&&t!==`symbol`}function t(t){let n=new Map,r=``,i=1,a=e=>{r+=e;for(let t=0;t<e.length;t+=1)e.charCodeAt(t)===10&&(i+=1)},o=(t,r,s)=>{let c=i;if(t===null)a(`null`);else if(Array.isArray(t)){if(t.length===0)a(`[]`);else{a(`[
`);let e=`${r}  `;t.forEach((n,r)=>{a(e),o(n,e,`${s}[${r}]`),a(r<t.length-1?`,
`:`
`)}),a(`${r}]`)}}else if(typeof t==`object`){let n=Object.keys(t).filter(n=>e(t[n]));if(n.length===0)a(`{}`);else{a(`{
`);let e=`${r}  `;n.forEach((r,i)=>{a(`${e}${JSON.stringify(r)}: `);let c=s?`${s}.${r}`:r;o(t[r],e,c),a(i<n.length-1?`,
`:`
`)}),a(`${r}}`)}}else a(JSON.stringify(t));s&&n.set(s,{startLine:c,endLine:i})};return o(t,``,``),{text:r,ranges:n}}function n(e){return e.trim()}function r(e,t){if(!t)return null;let r=e.get(n(t));if(r)return r;let i=n(t).replace(/\.(\d+)(?=\.|$)/g,`[$1]`);return e.get(i)??null}export{r as n,t};