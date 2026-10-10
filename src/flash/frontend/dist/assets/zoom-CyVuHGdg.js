const r=()=>{const t=new Set;return{emit(){t.forEach(e=>e())},subscribe(e){return t.add(e),()=>{t.delete(e)}}}};export{r as c};
