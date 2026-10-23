import {test,expect} from "bun:test";
import {Fingerprint} from "../index.js";

const hooks=await Fingerprint();
const tools=hooks.tool;
const originalFetch=globalThis.fetch;
const fixtureModel="alpha/model";
const validResponse=Array(300).fill(1).join(", ");

function fakeGateway(onPost,modelIds=[fixtureModel]) {
  globalThis.fetch=async (url,init={})=>{
    if(String(url).endsWith("/models"))
      return Response.json({data:modelIds.map(id=>({id}))});
    if(String(url).endsWith("/responses") || String(url).endsWith("/chat/completions"))
      return onPost(url,init);
    throw Error("Unexpected Gateway route: "+url);
  };
}
async function settled(jobId) {
  let job;
  for(let i=0;i<150;i++){
    job=await tools.status.execute({jobId});
    if(job.state!=="running")return job;
    await Bun.sleep(20);
  }
  throw Error("Backend job did not settle within the test deadline");
}

test("native backend samples via Gateway and scores without a CLI",async()=>{
  const seen=[];
  fakeGateway(async (url,init)=>{
    const body=JSON.parse(init.body);
    seen.push({url:String(url),model:body.model,input:body.input});
    expect(init.headers.Authorization).toBe("Bearer magpie");
    return Response.json({status:"completed",output:[{type:"message",content:[{type:"output_text",text:validResponse}]}]});
  });
  try {
    const options=await tools.models.execute({});
    expect(options.models).toEqual([fixtureModel]);
    const started=await tools.start.execute({model:fixtureModel,format:"responses"});
    expect(started.jobId).toBeString();
    const completed=await settled(started.jobId);
    expect(completed.state).toBe("done");
    expect(completed.completed).toBe(3);
    expect(completed.samples.every(s=>["complete","truncated"].includes(s.state))).toBe(true);
    expect(completed.analysis.decision).toBe("not_model");
    expect(seen).toHaveLength(3);
    expect(seen.every(x=>x.model===fixtureModel && Array.isArray(x.input))).toBe(true);
    expect(JSON.stringify(completed)).not.toContain("magpie");
  } finally {globalThis.fetch=originalFetch;}
});

test("unlisted model is rejected before any paid sample",async()=>{
  let posts=0;
  fakeGateway(async()=>{posts++;return Response.json({});});
  try {
    await expect(tools.start.execute({model:"alpha/not-listed",format:"chat"}))
      .rejects.toThrow("not currently exposed");
    expect(posts).toBe(0);
  } finally {globalThis.fetch=originalFetch;}
});

test("cancellation aborts outstanding sample requests",async()=>{
  let aborted=0;
  fakeGateway(async(url,init)=>new Promise((resolve,reject)=>{
    if(init.signal.aborted){aborted++;reject(Error("cancelled"));return;}
    init.signal.addEventListener("abort",()=>{aborted++;reject(Error("cancelled"));},{once:true});
  }));
  try {
    const job=await tools.start.execute({model:fixtureModel,format:"chat"});
    const canceled=await tools.cancel.execute({jobId:job.jobId});
    expect(canceled.state).toBe("cancelled");
    const done=await settled(job.jobId);
    expect(done.state).toBe("cancelled");
    expect(aborted).toBe(3);
  } finally {globalThis.fetch=originalFetch;}
});

test("routing group IDs are hidden and rejected before sampling",async()=>{
  let posts=0;
  fakeGateway(async()=>{posts++;return Response.json({});},[fixtureModel,"group/mixed"]);
  try {
    expect((await tools.models.execute({})).models).toEqual([fixtureModel]);
    await expect(tools.start.execute({model:"group/mixed"}))
      .rejects.toThrow("Routing groups cannot be fingerprinted");
    expect(posts).toBe(0);
  } finally {globalThis.fetch=originalFetch;}
});

test("completed non-streaming Chat responses are accepted",async()=>{
  fakeGateway(async()=>Response.json({
    choices:[{finish_reason:"stop",message:{content:validResponse}}]
  }));
  try {
    const job=await tools.start.execute({model:fixtureModel,format:"chat"});
    const done=await settled(job.jobId);
    expect(done.state).toBe("done");
    expect(done.completed).toBe(3);
    expect(done.samples.every(s=>["complete","truncated"].includes(s.state))).toBe(true);
  } finally {globalThis.fetch=originalFetch;}
});

test("non-terminal Chat and Responses results are never scored",async()=>{
  for (const format of ["chat","responses"]) {
    fakeGateway(async()=>format==="chat"
      ? Response.json({choices:[{finish_reason:"length",message:{content:validResponse}}]})
      : Response.json({status:"incomplete",output:[{type:"message",content:[{type:"output_text",text:validResponse}]}]}));
    try {
      const job=await tools.start.execute({model:fixtureModel,format});
      const done=await settled(job.jobId);
      expect(done.state).toBe("failed");
      expect(done.samples.every(s=>s.state==="failed")).toBe(true);
    } finally {globalThis.fetch=originalFetch;}
  }
});

test("excess model integers are trimmed to each challenge length",async()=>{
  fakeGateway(async()=>Response.json({status:"completed",output:[{
    type:"message",content:[{type:"output_text",text:Array(400).fill(1).join(", ")}]
  }]}));
  try {
    const job=await tools.start.execute({model:fixtureModel});
    const done=await settled(job.jobId);
    expect(done.state).toBe("done");
    expect(done.samples.every(s=>s.state==="truncated" && s.count===s.expectedCount)).toBe(true);
  } finally {globalThis.fetch=originalFetch;}
});

test("oversized Gateway JSON responses fail without unbounded parsing",async()=>{
  fakeGateway(async()=>new Response("x".repeat(1024*1024+1),{headers:{"Content-Type":"application/json"}}));
  try {
    const job=await tools.start.execute({model:fixtureModel});
    const done=await settled(job.jobId);
    expect(done.state).toBe("failed");
    expect(done.error).toContain("Gateway response is too large");
  } finally {globalThis.fetch=originalFetch;}
});

function eventData(data) { return "data: "+JSON.stringify(data)+"\r\n\r\n"; }
function chunkedSSE(frames) {
  const data=new TextEncoder().encode(frames.join(""));
  let offset=0;
  return new Response(new ReadableStream({
    pull(controller) {
      if(offset>=data.length) { controller.close();return; }
      const step=1+(offset%47);
      controller.enqueue(data.slice(offset,offset+step));offset+=step;
    }
  }),{headers:{"content-type":"text/event-stream; charset=utf-8"}});
}

test("streaming Chat accumulates split SSE deltas and verifies terminal finish",async()=>{
  const parts=Array(280).fill(1).join(", "),seen=[];
  fakeGateway(async(url,init)=>{
    seen.push(JSON.parse(init.body));
    expect(init.headers.Accept).toBe("text/event-stream");
    return chunkedSSE([
      ": keepalive\r\n\r\n",
      eventData({choices:[{index:0,delta:{content:parts.slice(0,410)}}]}),
      eventData({choices:[{index:0,delta:{content:parts.slice(410)}}]}),
      eventData({choices:[{index:0,delta:{},finish_reason:"stop"}]}),
      "data: [DONE]\r\n\r\n"
    ]);
  });
  try {
    const job=await tools.start.execute({model:fixtureModel,format:"chat",stream:true});
    expect(job.stream).toBe(true);
    const done=await settled(job.jobId);
    expect(done.state).toBe("done");
    expect(done.samples.every(s=>s.state==="complete" && s.count===280)).toBe(true);
    expect(seen).toHaveLength(3);
    expect(seen.every(body=>body.stream===true)).toBe(true);
  } finally {globalThis.fetch=originalFetch;}
});

test("streaming Responses handles deltas and the response.completed event",async()=>{
  const text=Array(280).fill(1).join(", ");
  fakeGateway(async()=>chunkedSSE([
    eventData({type:"response.output_text.delta",delta:text}),
    eventData({type:"response.completed",response:{status:"completed",output:[]}})
  ]));
  try {
    const job=await tools.start.execute({model:fixtureModel,format:"responses",stream:true});
    const done=await settled(job.jobId);
    expect(done.state).toBe("done");
    expect(done.samples.every(s=>s.state==="complete" && s.count===280)).toBe(true);
  } finally {globalThis.fetch=originalFetch;}
});

test("streaming samples stop at the upstream relaxed-mode integer cap",async()=>{
  let cancellations=0;
  fakeGateway(async()=>{
    const payload=new TextEncoder().encode(eventData({choices:[{index:0,delta:{
      content:Array(400).fill(1).join(", ")+", "
    }}]}));
    return new Response(new ReadableStream({
      start(controller){controller.enqueue(payload);},
      cancel(){cancellations++;}
    }),{headers:{"content-type":"text/event-stream"}});
  });
  try {
    const job=await tools.start.execute({model:fixtureModel,format:"chat",stream:true});
    const done=await settled(job.jobId);
    expect(done.state).toBe("done");
    expect(done.samples.every(s=>s.state==="truncated" && s.count===s.expectedCount)).toBe(true);
    expect(cancellations).toBe(3);
  } finally {globalThis.fetch=originalFetch;}
});

test("streaming terminal failures cannot enter scoring",async()=>{
  for (const format of ["chat","responses"]) {
    fakeGateway(async()=>chunkedSSE(format==="chat"
      ? [eventData({choices:[{index:0,delta:{content:Array(280).fill(1).join(", ")}}]}),
        "data: [DONE]\n\n"]
      : [eventData({type:"response.output_text.delta",delta:Array(280).fill(1).join(", ")}),
        eventData({type:"response.incomplete",response:{status:"incomplete"}})]));
    try {
      const job=await tools.start.execute({model:fixtureModel,format,stream:true});
      const done=await settled(job.jobId);
      expect(done.state).toBe("failed");
      expect(done.samples.every(s=>s.state==="failed")).toBe(true);
    } finally {globalThis.fetch=originalFetch;}
  }
});

test("cancel aborts active SSE readers without scoring partial output",async()=>{
  let aborted=0;
  fakeGateway(async(url,init)=>{
    return new Response(new ReadableStream({
      start(controller){
        controller.enqueue(new TextEncoder().encode(eventData({choices:[{index:0,
          delta:{content:"1, 2, 3, "}}]})));
        init.signal.addEventListener("abort",()=>{aborted++;controller.error(Error("aborted"));},{once:true});
      }
    }),{headers:{"content-type":"text/event-stream"}});
  });
  try {
    const job=await tools.start.execute({model:fixtureModel,format:"chat",stream:true});
    for(let n=0;n<100;n++){
      const state=await tools.status.execute({jobId:job.jobId});
      if(state.samples.length===3 && state.samples.every(s=>s.state==="streaming"))break;
      await Bun.sleep(5);
    }
    const cancelled=await tools.cancel.execute({jobId:job.jobId});
    expect(cancelled.state).toBe("cancelled");
    await Bun.sleep(20);
    expect(aborted).toBe(3);
    const result=await tools.status.execute({jobId:job.jobId});
    expect(result.analysis).toBeNull();
  } finally {globalThis.fetch=originalFetch;}
});
