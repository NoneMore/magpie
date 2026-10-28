// Native LM Detector page: no iframe, Bun host or credential access.
magpie.plugin("lm-detector", (ctx) => {
  let root, models=[], provider="", model="", format="responses", running="", starting=false;
  let pollTimer=0, loaded=false, pinnedProvider=false, selectedRound=0, lastJob=null;
  const ui=ctx.ui;
  const find=(name)=>root.querySelector('[data-fpd="'+name+'"]');
  const say=(message)=>{find("message").textContent=message;};
  const call=(route,input={})=>api("lm-detector/"+route,input);
  const groups=()=>[...new Set(models.map(m=>m.split("/")[0]))].sort();
  const setMenu=(name,options,current,choose)=>{
    const place=find(name);
    place.replaceChildren(ui.menu(options.map(x=>({v:x.v,name:x.name})),current,name,choose));
  };
  function picks() {
    const busy=starting||!!running;
    const providers=groups();
    // A provider row action must never silently redirect a paid run to another provider.
    if(!pinnedProvider && !providers.includes(provider))provider=providers[0]||"";
    const choices=providers.map(v=>({v,name:v}));
    if(pinnedProvider && provider && !providers.includes(provider))choices.unshift({v:provider,name:provider+" (no direct models)"});
    setMenu("provider",choices,provider,v=>{
      provider=v;pinnedProvider=false;model="";picks();
    });
    const list=models.filter(v=>v.startsWith(provider+"/"));
    if(!list.includes(model))model=list[0]||"";
    setMenu("model",list.map(v=>({v,name:v.slice(provider.length+1)})),model,v=>{model=v;});
    setMenu("format",[{v:"responses",name:"Responses"},{v:"chat",name:"Chat Completions"}],format,v=>{format=v;});
    find("run").disabled=!model||busy;
    for(const el of root.querySelectorAll("input"))el.disabled=busy;
    const strict=find("strict");
    if(Number(find("count").value)!==3){strict.checked=false;strict.disabled=true;}
    for(const key of ["provider","model","format","reload"])find(key).querySelectorAll("button").forEach(b=>b.disabled=busy);
  }
  async function load() {
    if(starting||running)return;
    say(t("Reading Gateway models…"));
    try{
      const answer=await call("models");
      models=answer.models||[];
      picks();
      loaded=true;
      say(pinnedProvider && !models.some(m=>m.startsWith(provider+"/"))
        ? provider+": "+t("No fingerprintable Gateway models.")
        : models.length?t("Choose one direct model and run a fingerprint check."):t("No fingerprintable Gateway models."));
    }catch(e){say(e.message);}
  }
  function options() {
    const count=Number(find("count").value);
    return {model,format,stream:find("stream").checked,effort:find("effort").value.trim(),
      count,parallel:Math.min(count,Number(find("parallel").value)),
      timeout:Number(find("timeout").value),repeat:Number(find("repeat").value),strict:find("strict").checked};
  }
  function render(job) {
    lastJob=job;
    find("progress").textContent=t("Round")+" "+job.round+"/"+job.repeat+
      " · "+job.completed+"/"+(job.count*job.repeat)+" "+t("requests finished");
    const samples=job.samples||[];
    find("samples").replaceChildren(...samples.map(s=>{
      const row=document.createElement("div");row.className="fpd-sample";
      const label=document.createElement("span");
      label.textContent="#"+s.index+" · "+s.state+" · "+s.count+"/"+s.expectedCount;
      const bar=document.createElement("progress");
      bar.max=s.expectedCount;bar.value=s.count;
      if(s.state==="waiting")bar.removeAttribute("value");
      row.append(label,bar);
      if(s.firstByteMs!=null || s.durationMs!=null){
        const metrics=document.createElement("small");
        const parts=[];
        if(s.firstByteMs!=null)parts.push((job.stream?"First byte ":"Response ready ")+(s.firstByteMs/1000).toFixed(2)+"s");
        if(s.durationMs!=null)parts.push((s.durationMs/1000).toFixed(1)+"s");
        // Use the entire observed sample lifetime for an honest average.
        // First SSE bytes can be heartbeat/metadata, not first content.
        if(s.startedAt!=null && s.count>0){
          const elapsed=s.durationMs??Math.max(0,Date.now()-s.startedAt);
          if(elapsed>0)parts.push((s.count*1000/elapsed).toFixed(1)+" avg numbers/s");
        }
        metrics.textContent=parts.join(" · ");
        row.append(metrics);
      }
      if(s.error){const e=document.createElement("small");e.textContent=s.error;row.append(e);}
      return row;
    }));
    const rounds=job.rounds||[];
    const picker=find("round");
    const options=[{id:0,name:"Latest"}].concat(rounds.map(r=>({id:r.round,name:t("Round")+" "+r.round})));
    picker.replaceChildren(...options.map(o=>{
      const el=document.createElement("option");el.value=o.id;el.textContent=o.name;return el;
    }));
    if(!options.some(o=>o.id===selectedRound))selectedRound=0;
    picker.value=String(selectedRound);
    picker.hidden=rounds.length<2;
    const latest=selectedRound?rounds.find(r=>r.round===selectedRound):rounds[rounds.length-1];
    if(latest){
      const a=latest.analysis;
      find("winner").textContent=a?.prediction_name||a?.prediction||t("Not scored");
      find("reason").textContent=a?.evidence?.reason||latest.error||"";
      find("results").replaceChildren(...(a?.results||[]).slice(0,15).map(item=>{
        const line=document.createElement("div");line.className="fpd-result";
        const title=document.createElement("span");
        title.textContent=item.display_name||item.model||"—";
        const score=document.createElement("span");
        score.textContent=typeof item.probability==="number"?(100*item.probability).toFixed(1)+"%":item.score.toFixed(3);
        line.append(title,score);
        return line;
      }));
      const warning=latest.samples.filter(s=>s.anomalousDistribution).map(s=>"#"+s.index);
      find("warning").textContent=warning.length?t("Abnormal number distribution:")+" "+warning.join(", "):"";
    }else{
      // A new paid job must never display the previous model's verdict.
      for(const key of ["winner","reason","warning"])find(key).textContent="";
      find("results").replaceChildren();
    }
    find("cancel").hidden=job.state!=="running";
    if(job.state!=="running"){
      running="";picks();
      say(job.state==="done"?t("Fingerprint check completed."):job.state==="cancelled"?t("Fingerprint check cancelled."):job.error||job.state);
    }
  }
  async function poll() {
    if(!running)return;
    const id=running;
    try{
      const job=await call("status",{jobId:id});
      if(running!==id)return;
      render(job);
      if(running)pollTimer=setTimeout(poll,300);
    }catch(e){running="";say(e.message);picks();}
  }
  async function run() {
    if(starting||running||!model)return;
    // Set this before the first await: Start is paid and the server allows two jobs.
    starting=true;picks();
    try{
      const job=await call("start",options());
      running=job.jobId;starting=false;selectedRound=0;lastJob=null;picks();render(job);
      await poll();
    }catch(e){starting=false;say(e.message);picks();}
  }
  async function cancel(){
    if(!running)return;
    try{const job=await call("cancel",{jobId:running});render(job);}
    catch(e){say(e.message);}
  }
  function reveal(p) {
    if(p?.id){provider=p.id;pinnedProvider=true;model="";if(root&&loaded)picks();}
  }
  ctx.slots.add("page",{
    key:"lm-detector",title:"Model fingerprint",
    html:`
    <div class="row-head"><span class="label" data-t>Model fingerprint</span></div>
    <section class="list fpd-shell">
      <div class="fpd-field"><span data-t>Provider</span><div data-fpd="provider"></div></div>
      <div class="fpd-field"><span data-t>Model</span><div data-fpd="model"></div></div>
      <div class="fpd-field"><span data-t>API format</span><div data-fpd="format"></div></div>
      <div class="fpd-options">
        <label><span data-t>Stream</span><input data-fpd="stream" type="checkbox" checked></label>
        <label><span data-t>Strict</span><input data-fpd="strict" type="checkbox"></label>
        <label><span data-t>Reasoning effort</span><input data-fpd="effort" maxlength="64" type="text" placeholder="high"></label>
        <label><span data-t>Timeout (seconds)</span><input data-fpd="timeout" type="number" min="0.1" max="600" step="0.1" value="120"></label>
        <label><span data-t>Challenges per round</span><input data-fpd="count" type="number" min="1" max="3" value="3"></label>
        <label><span data-t>Parallel requests</span><input data-fpd="parallel" type="number" min="1" max="3" value="3"></label>
        <label><span data-t>Rounds</span><input data-fpd="repeat" type="number" min="1" max="20" value="1"></label>
      </div>
      <div class="fpd-actions">
        <button type="button" class="text" data-fpd="reload" data-t>Reload models</button>
        <button type="button" class="text" data-fpd="run" data-t>Run fingerprint</button>
        <button type="button" class="text" data-fpd="cancel" data-t hidden>Cancel detection</button>
      </div>
      <p data-fpd="message" role="status"></p>
      <p data-fpd="progress"></p>
      <div data-fpd="samples"></div>
      <select data-fpd="round" aria-label="Detection round" hidden></select>
      <strong data-fpd="winner"></strong>
      <p data-fpd="reason"></p>
      <p data-fpd="warning" role="alert"></p>
      <div data-fpd="results"></div>
      <p class="usage-note" data-t>Scores are closed-set comparisons, not proof of model identity. Every run uses paid model tokens.</p>
    </section>`,
    mount(main) {
      root=main;
      find("reload").onclick=load;
      find("run").onclick=run;
      find("cancel").onclick=cancel;
      find("round").onchange=()=>{
        selectedRound=Number(find("round").value);
        if(lastJob)render(lastJob);
      };
      find("count").onchange=()=>picks();
      picks();
    },
    open(){void load();},
    reveal,
  });
});
