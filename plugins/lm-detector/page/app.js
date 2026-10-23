(() => {
  const $ = id => document.getElementById(id);
  let models = [], providerNames = new Map(), chosenProvider = "", jobId = null, active = false;
  let timer = null, clock = null, started = 0;
  function say(message, kind="") {
    $("message").textContent = message; $("dot").className = kind;
  }
  function opt(text,value) {
    const el = document.createElement("option");el.textContent=text;el.value=value;return el;
  }
  function providers() {return [...new Set(models.map(m=>m.split("/")[0]))].sort();}
  function setProviders() {
    const old=$("provider").value, ids=providers();
    const missing=!!chosenProvider&&!ids.includes(chosenProvider);
    $("provider").replaceChildren(...ids.map(id=>opt(providerNames.get(id)||id,id)),
      ...(missing?[opt(providerNames.get(chosenProvider)||chosenProvider,chosenProvider)]:[]));
    $("provider").value=missing?chosenProvider:ids.includes(chosenProvider)?chosenProvider:ids.includes(old)?old:(ids[0]||"");
    setModels();
    if(missing)say("所选 Provider 当前没有可检测的 Gateway 模型","error");
    return !missing;
  }
  function setModels() {
    const p=$("provider").value, old=$("model").value;
    const filtered=models.filter(m=>m.startsWith(p+"/"));
    $("model").replaceChildren(...filtered.map(m=>opt(m.slice(p.length+1),m)));
    if(filtered.includes(old))$("model").value=old;
    for(const id of ["provider","model","format","stream","reload"])$(id).disabled=!!jobId;
    $("run").disabled=!!jobId||!filtered.length;
  }
  async function tool(name,args={}) {
    if(typeof magpie.tool?.run!=="function")throw Error("当前 Magpie 不支持 Page 后台调用");
    const out=await magpie.tool.run(name,args);
    if(typeof out?.result!=="string")throw Error("插件响应格式异常");
    return JSON.parse(out.result);
  }
  async function load() {
    say("正在读取 Gateway 模型…","busy");$("run").disabled=true;
    try {
      const [m,p]=await Promise.all([tool("models"),magpie.read("providers:list").catch(()=>[])]);
      models=Array.isArray(m?.models)?m.models:[];
      const list=Array.isArray(p)?p:(Array.isArray(p?.providers)?p.providers:[]);
      providerNames=new Map(list.filter(x=>x?.id).map(x=>[x.id,x.name||x.id]));
      if(setProviders())
        say(models.length?"已加载 "+models.length+" 个模型":"Gateway 暂无可用模型",models.length?"ok":"error");
    }catch(e){say("读取失败："+e.message,"error");}
  }
  function stop(){clearTimeout(timer);clearInterval(clock);timer=null;clock=null;}
  function render(job) {
    const a=job.analysis;$("resultSection").hidden=!a;if(!a)return;
    $("winner").textContent=a.prediction_name||a.prediction||"无法判断";
    $("winnerNote").textContent=a.evidence?.reason||a.decision||"";
    $("sampleCount").textContent="有效样本 "+(a.used_outputs||0)+"/3";
    $("results").replaceChildren(...(a.results||[]).slice(0,15).map(r=>{
      const tr=document.createElement("tr"),name=document.createElement("td"),p=document.createElement("td"),s=document.createElement("td");
      name.textContent=r.display_name||r.model||"—";p.className="num";s.className="num";
      if(typeof r.probability==="number"&&Number.isFinite(r.probability)){
        const bar=document.createElement("span"),fill=document.createElement("span");
        bar.className="bar";fill.style.width=Math.min(100,Math.max(0,r.probability*100))+"%";
        bar.append(fill);p.append(bar,document.createTextNode((r.probability*100).toFixed(1)+"%"));
      }else p.textContent="—";
      s.textContent=typeof r.score==="number"&&Number.isFinite(r.score)?r.score.toFixed(3):"—";
      tr.append(name,p,s);return tr;
    }));
    $("diagnostics").textContent=JSON.stringify({decision:a.decision,probability_status:a.probability_status,
      evidence:a.evidence,samples:job.samples},null,2);
  }
  function finish(job){
    stop();jobId=null;$("cancel").hidden=true;$("elapsed").textContent="";setModels();
    if(job.state==="done"){say("检测完成","ok");render(job);}
    else if(job.state==="cancelled")say("检测已取消");
    else say("检测失败："+(job.error||"未知错误"),"error");
  }
  async function poll() {
    if(!jobId)return;const id=jobId;
    try {
      const j=await tool("status",{jobId:id});if(jobId!==id)return;
      if(j.state!=="running"){finish(j);return;}
      const reading=j.samples.filter(s=>s.state==="streaming");
      say("已完成 "+j.completed+"/3 次挑战"+(reading.length?"，流式接收 "+reading.map(s=>s.count).join("/")+" 个数字":"")+"…","busy");
      timer=setTimeout(poll,1600);
    }catch(e){stop();jobId=null;$("cancel").hidden=true;setModels();say("读取任务失败："+e.message,"error");}
  }
  async function run(){
    if(jobId||!$("model").value)return;
    $("run").disabled=true;$("resultSection").hidden=true;say("正在建立检测任务…","busy");
    try {
      const j=await tool("start",{model:$("model").value,format:$("format").value,stream:$("stream").checked});
      jobId=j.jobId;started=Date.now();$("cancel").hidden=false;setModels();
      clock=setInterval(()=>$("elapsed").textContent=Math.floor((Date.now()-started)/1000)+"s",1000);
      await poll();
    }catch(e){say("无法开始："+e.message,"error");setModels();}
  }
  async function cancel(){
    const id=jobId;if(!id)return;$("cancel").disabled=true;
    try{const j=await tool("cancel",{jobId:id});if(jobId===id)finish(j);}
    catch(e){say("取消失败："+e.message,"error");}
    finally{$("cancel").disabled=false;}
  }
  magpie.onAction(({id,provider})=>{
    if(id!=="detect"||!provider?.id)return;
    chosenProvider=provider.id;
    if(active)setProviders();
  });
  magpie.ready.then(async()=>{
    active=true;
    $("provider").addEventListener("change",()=>{chosenProvider="";setModels();});
    $("run").addEventListener("click",run);
    $("cancel").addEventListener("click",cancel);
    $("reload").addEventListener("click",load);
    await load();
  }).catch(e=>say(String(e?.message||e),"error"));
})();
