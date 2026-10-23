import {test,expect} from "bun:test";
import {Fingerprint} from "../index.js";

const hooks=await Fingerprint();
const tools=hooks.tool;
const originalFetch=globalThis.fetch;
const fixtureModel="alpha/model";
const validResponse=Array(300).fill(1).join(", ");

function fakeGateway(onPost) {
  globalThis.fetch=async (url,init={})=>{
    if(String(url).endsWith("/models"))
      return Response.json({data:[{id:fixtureModel}]});
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
    expect(completed.samples.every(s=>s.state==="complete")).toBe(true);
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
