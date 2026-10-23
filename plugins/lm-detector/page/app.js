(() => {
  const $ = id => document.getElementById(id);
  let models = [], providerNames = new Map(), chosenProvider = "", jobId = null, active = false;
  let timer = null, clock = null, started = 0, resultJob = null;
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
  function syncCount() {
    const count=Number($("count").value),old=Number($("parallel").value);
    $("parallel").replaceChildren(...Array.from({length:count},(_,i)=>opt(String(i+1),String(i+1))));
    $("parallel").value=String(Math.min(count,old||count));
    if(count!==3)$("strict").checked=false;
    $("strict").disabled=!!jobId||count!==3;
  }
  function setModels() {
    const p=$("provider").value, old=$("model").value;
    const filtered=models.filter(m=>m.startsWith(p+"/"));
    $("model").replaceChildren(...filtered.map(m=>opt(m.slice(p.length+1),m)));
    if(filtered.includes(old))$("model").value=old;
    for(const id of ["provider","model","format","stream","reload","effort",
      "timeout","count","parallel","repeat"])$(id).disabled=!!jobId;
    $("strict").disabled=!!jobId||Number($("count").value)!==3;
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
  function renderRound(index) {
    const r=resultJob?.rounds?.[index],a=r?.analysis;
    if(!r)return;
    $("winner").textContent=a?.prediction_name||a?.prediction||"本轮未评分";
    $("winnerNote").textContent=a?.evidence?.reason||r.error||a?.decision||"";
    $("sampleCount").textContent="第 "+r.round+"/"+resultJob.repeat+" 轮 · 有效样本 "+
      (a?.used_outputs||0)+"/"+resultJob.count;
    const anomalous=(r.samples||[]).filter(s=>s.anomalousDistribution);
    $("distributionWarning").hidden=!anomalous.length;
    $("distributionWarning").textContent=anomalous.length
      ? "⚠ 样本 "+anomalous.map(s=>s.index).join("、")+
        " 的最小有效数字不低于 200，数字分布异常，本轮检测结果不可靠。请重新检测以生成新的随机提示词。"
      : "";
    $("results").replaceChildren(...(a?.results||[]).slice(0,15).map(item=>{
      const tr=document.createElement("tr"),name=document.createElement("td"),
        p=document.createElement("td"),s=document.createElement("td");
      name.textContent=item.display_name||item.model||"—";p.className="num";s.className="num";
      if(typeof item.probability==="number"&&Number.isFinite(item.probability)){
        const bar=document.createElement("span"),fill=document.createElement("span");
        bar.className="bar";fill.style.width=Math.min(100,Math.max(0,item.probability*100))+"%";
        bar.append(fill);p.append(bar,document.createTextNode((item.probability*100).toFixed(1)+"%"));
      }else p.textContent="—";
      s.textContent=typeof item.score==="number"&&Number.isFinite(item.score)?item.score.toFixed(3):"—";
      tr.append(name,p,s);return tr;
    }));
    $("diagnostics").textContent=JSON.stringify({
      round:r.round,request:{effort:resultJob.effort,timeout:resultJob.timeout,
        count:resultJob.count,parallel:resultJob.parallel,repeat:resultJob.repeat,
        strict:resultJob.strict},decision:a?.decision,probability_status:a?.probability_status,
      evidence:a?.evidence,error:r.error,samples:r.samples
    },null,2);
  }
  function render(job) {
    const rounds=job.rounds||[];
    $("resultSection").hidden=!rounds.length;
    if(!rounds.length)return;
    resultJob=job;
    $("roundResult").replaceChildren(...rounds.map((r,i)=>opt(
      "第 "+r.round+" 轮 · "+(r.analysis?.prediction_name||r.analysis?.prediction||r.error||"未评分"),String(i))));
    const selected=rounds.length-1;
    $("roundResult").value=String(selected);
    $("roundNotice").textContent=rounds.filter(r=>!r.analysis || r.analysis.decision==="unscorable").length
      ? "部分轮次未能评分，请选择对应轮次查看错误和样本状态。"
      : "各轮独立评分，不会合并闭集概率。";
    renderRound(selected);
  }
  function finish(job){
    stop();jobId=null;$("cancel").hidden=true;$("elapsed").textContent="";setModels();
    render(job);
    if(job.state==="done") {
      say(job.rounds.some(r=>!r.analysis || r.analysis.decision==="unscorable")
        ?"检测结束，部分轮次未评分":"检测完成","ok");
    }else if(job.state==="cancelled")say("检测已取消");
    else say("检测失败："+(job.error||"未知错误"),"error");
  }
  async function poll() {
    if(!jobId)return;const id=jobId;
    try {
      const j=await tool("status",{jobId:id});if(jobId!==id)return;
      if(j.state!=="running"){finish(j);return;}
      const reading=j.samples.filter(s=>s.state==="streaming");
      say("第 "+j.round+"/"+j.repeat+" 轮，已完成 "+j.completed+"/"+(j.count*j.repeat)+
        " 次挑战"+(reading.length?"，流式接收 "+reading.map(s=>s.count).join("/")+" 个数字":"")+"…","busy");
      timer=setTimeout(poll,1600);
    }catch(e){stop();jobId=null;$("cancel").hidden=true;setModels();say("读取任务失败："+e.message,"error");}
  }
  async function run(){
    if(jobId||!$("model").value)return;
    $("run").disabled=true;$("resultSection").hidden=true;say("正在建立检测任务…","busy");
    try {
      const args={model:$("model").value,format:$("format").value,stream:$("stream").checked,
        timeout:Number($("timeout").value),count:Number($("count").value),
        parallel:Number($("parallel").value),repeat:Number($("repeat").value),
        strict:$("strict").checked};
      const effort=$("effort").value.trim();
      if(effort)args.effort=effort;
      const j=await tool("start",args);
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
    $("count").addEventListener("change",syncCount);
    $("roundResult").addEventListener("change",()=>renderRound(Number($("roundResult").value)));
    syncCount();
    $("run").addEventListener("click",run);
    $("cancel").addEventListener("click",cancel);
    $("reload").addEventListener("click",load);
    await load();
  }).catch(e=>say(String(e?.message||e),"error"));
})();
