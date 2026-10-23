// Native Magpie Page backend. No CLI process, no fpd subprocess.
// The reference bank/data are read from the lmfpd npm package; the original
// detector algorithm is vendored from Ikaleio/lm-detector shared/.
import {createRequire} from "node:module";
import {dirname, join} from "node:path";
import {readFile} from "node:fs/promises";
import {randomUUID} from "node:crypto";
import {generateChallenges} from "./shared/challenge-browser.js";
import {parseNumbers} from "./shared/fingerprint-core.js";
import {decodeDetector} from "./shared/detector-codec.ts";
import {analyzeSharedOutputs, supportsSharedDetector} from "./shared/shared-detector.ts";

const require = createRequire(import.meta.url);
const jobs = new Map();
const MAX_RUNNING = 2, MAX_HISTORY = 40, MAX_AGE = 24 * 60 * 60 * 1000;
const MAX_REPEAT = 20, DEFAULT_TIMEOUT = 120;
const DEFAULT_GATEWAY = "http://127.0.0.1:3425/v1";
const MAX_RESPONSE_BYTES = 1024 * 1024;
// Match upstream sample-distribution.ts: UI warning only, never affects scoring.
const ANOMALOUS_MINIMUM = 200;
const stringArg = {type:"string",minLength:1};
const schema = (properties,required) => ({type:"object",properties,required,additionalProperties:false});
let starting = 0, referencePromise;

function gateway() {
  const url = new URL((process.env.MAGPIE_FPD_GATEWAY_URL || DEFAULT_GATEWAY).trim());
  if (!["http:","https:"].includes(url.protocol) || url.username || url.password || url.search || url.hash)
    throw Error("Gateway URL must be an HTTP(S) URL without credentials or query.");
  const local = ["127.0.0.1","localhost","[::1]"].includes(url.hostname);
  if (!local && url.protocol !== "https:") throw Error("Remote Gateway must use HTTPS.");
  const key = process.env.MAGPIE_FPD_GATEWAY_KEY || (local ? "magpie" : "");
  if (!key) throw Error("Set MAGPIE_FPD_GATEWAY_KEY for a remote Gateway.");
  const base = url.href.replace(/\/+$/,"").replace(/\/v1$/,"") + "/v1";
  return {base,key};
}

async function boundedJson(response) {
  if (Number(response.headers.get("content-length")) > MAX_RESPONSE_BYTES)
    throw Error("Gateway response is too large.");
  if (!response.body) throw Error("Gateway returned an empty body.");
  const reader = response.body.getReader();
  const chunks = []; let size = 0;
  try {
    while (true) {
      const {done,value} = await reader.read();
      if (done) break;
      size += value.byteLength;
      if (size > MAX_RESPONSE_BYTES) throw Error("Gateway response is too large.");
      chunks.push(value);
    }
  } finally {
    if (size > MAX_RESPONSE_BYTES) await reader.cancel().catch(()=>{});
    reader.releaseLock();
  }
  const bytes = new Uint8Array(size);
  let offset = 0;
  for (const chunk of chunks) { bytes.set(chunk,offset); offset += chunk.length; }
  return JSON.parse(new TextDecoder().decode(bytes));
}
async function models() {
  const {base,key} = gateway();
  const response = await fetch(base + "/models", {
    headers:{Authorization:"Bearer " + key}, redirect:"error", signal:AbortSignal.timeout(10000)
  });
  if (!response.ok) throw Error("Cannot list Gateway models (HTTP " + response.status + ").");
  const payload = await response.json();
  if (!Array.isArray(payload?.data)) throw Error("Unexpected Gateway model list.");
  const valid = s => typeof s === "string" && s.length <= 256 &&
    /^[^\s\x00-\x1f]+\/[^\s\x00-\x1f]+$/.test(s);
  return [...new Set(payload.data.map(row => row?.id).filter(id => valid(id) && !id.startsWith("group/")))].slice(0,2000).sort();
}

async function references() {
  if (!referencePromise) referencePromise = (async () => {
    let pkg;
    try { pkg = require.resolve("lmfpd/package.json"); }
    catch { throw Error("Missing lmfpd reference files. Run npm install in plugins/lm-detector."); }
    const data = join(dirname(pkg), "data");
    const [bankFile,detectorFile] = await Promise.all([
      readFile(join(data,"unified_bank.json"),"utf8"),
      readFile(join(data,"shared_detector.bin"))
    ]);
    const bank = JSON.parse(bankFile);
    const detector = decodeDetector(detectorFile);
    if (!supportsSharedDetector(bank,detector))
      throw Error("lmfpd reference bank and vendored detector are incompatible. Align versions.");
    return {bank,detector};
  })().catch(e => { referencePromise = null; throw e; });
  return referencePromise;
}

function publicJob(j) {
  return {jobId:j.id,model:j.model,format:j.format,state:j.state,
    stream:j.stream,effort:j.effort ?? null,timeout:j.timeout,count:j.count,
    parallel:j.parallel,repeat:j.repeat,strict:j.strict,round:j.round,
    startedAt:j.startedAt,finishedAt:j.finishedAt || null,
    completed:j.completed,samples:j.samples.map(s=>({...s})),
    rounds:j.rounds.map(r=>({...r,samples:r.samples.map(s=>({...s}))})),
    error:j.error || null,analysis:j.analysis || null};
}
function findJob(id) {
  if (typeof id !== "string" || id.length > 100) throw Error("Invalid job ID.");
  const j = jobs.get(id);
  if (!j) throw Error("Job not found; the plugin host may have restarted.");
  return j;
}
function prune() {
  const now = Date.now();
  for (const [id,j] of jobs) if (j.state !== "running" &&
      now - Date.parse(j.finishedAt || j.startedAt) > MAX_AGE) jobs.delete(id);
  for (const [id,j] of jobs) {
    if (jobs.size <= MAX_HISTORY) break;
    if (j.state !== "running") jobs.delete(id);
  }
}
function pickText(payload,format) {
  if (format === "chat") {
    const choice = payload?.choices?.[0], m = choice?.message;
    if (m?.refusal || ["refusal","content_filter"].includes(choice?.finish_reason))
      throw Error("Model refused the fingerprint challenge.");
    if (!["stop","end_turn","completed"].includes(choice?.finish_reason))
      throw Error("Chat completion did not finish normally.");
    const c = m?.content;
    return typeof c === "string" ? c : Array.isArray(c) ?
      c.map(x=>typeof x?.text==="string"?x.text:"").join("") : "";
  }
  if (payload?.status !== "completed")
    throw Error("Responses request did not complete.");
  return (Array.isArray(payload?.output)?payload.output:[])
    .filter(x=>x?.type==="message")
    .flatMap(x=>Array.isArray(x?.content)?x.content:[])
    .filter(x=>x?.type==="output_text")
    .map(x=>typeof x.text==="string"?x.text:"").join("");
}
// Consume OpenAI-compatible SSE without buffering the complete stream.
async function streamText(response,format,expected,out,strict,onFirstChunk) {
  if (!response.body) throw Error("Gateway returned an empty SSE body.");
  const reader=response.body.getReader(),decoder=new TextDecoder();
  let buffer="",text="",bytes=0,finished=false,finish="",capped=false;
  function append(part) {
    if (typeof part!=="string" || !part || capped) return;
    text+=part;
    if (text.length>262144) throw Error("Unexpectedly large model response.");
    // A trailing digit may still be part of the next streamed token.
    const numbers=parseNumbers(text.replace(/\d+$/,""));
    out.count=Math.min(numbers.length,expected);
    if (!strict && numbers.length>=expected) {
      text=numbers.slice(0,expected).join(", ");
      out.count=expected;
      capped=true;
    }
  }
  function event(frame) {
    const data=frame.split(/\r\n|\r|\n/).filter(line=>line.startsWith("data:"))
      .map(line=>line.slice(5).replace(/^ /,"")).join("\n");
    if (!data) return;
    if (data.trim()==="[DONE]") { if(format==="chat")finished=true; return; }
    const packet=JSON.parse(data);
    if (packet?.error || packet?.type==="error")
      throw Error(String(packet?.error?.message||packet?.message||"Upstream SSE error."));
    if (format==="chat") {
      const choices=Array.isArray(packet?.choices)?packet.choices:[];
      const choice=choices.find(x=>x?.index===0)||choices[0];
      if (choice?.delta?.refusal || ["refusal","content_filter"].includes(choice?.finish_reason))
        throw Error("Model refused the fingerprint challenge.");
      if (choice?.finish_reason) finish=choice.finish_reason;
      append(choice?.delta?.content);
    } else {
      if (packet?.type==="response.refusal.delta")
        throw Error("Model refused the fingerprint challenge.");
      if (packet?.type==="response.failed" || packet?.type==="response.incomplete")
        throw Error("Responses request did not complete.");
      if (packet?.type==="response.output_text.delta") append(packet.delta);
      if (packet?.type==="response.output_text.done" && !text) append(packet.text);
      if (packet?.type==="response.output_item.done" && !text)
        append(pickText({status:"completed",output:[packet.item]},"responses"));
      if (packet?.type==="response.completed") {
        const response=packet.response;
        if (response?.status!=="completed") throw Error("Responses request did not complete.");
        if (!capped) {
          const full=pickText(response,"responses");
          if (full) {
            text="";
            append(full);
          }
        }
        finish="completed";
        finished=true;
      }
    }
  }
  try {
    while (!finished && !capped) {
      const {done,value}=await reader.read();
      if (done) break;
      if (value.byteLength) onFirstChunk();
      bytes+=value.byteLength;
      if (bytes>MAX_RESPONSE_BYTES) throw Error("Gateway SSE response is too large.");
      buffer+=decoder.decode(value,{stream:true});
      if (buffer.length>262144) throw Error("Gateway SSE event is too large.");
      let match;
      while ((match=/\r\n\r\n|\n\n|\r\r/.exec(buffer))) {
        const frame=buffer.slice(0,match.index);
        buffer=buffer.slice(match.index+match[0].length);
        event(frame);
        if (finished||capped) break;
      }
    }
    if (!finished && !capped) {
      buffer+=decoder.decode();
      if (buffer.trim()) event(buffer);
    }
    if (!capped && !(finished && (format==="responses"
      ? finish==="completed" : ["stop","end_turn","completed"].includes(finish))))
      throw Error("Stream ended without a successful completion.");
    return {text,capped};
  } finally {
    await reader.cancel().catch(()=>{});
    reader.releaseLock();
  }
}
async function sample(job,index,challenge) {
  const out = job.samples[index];
  out.state = "waiting";
  let key = "", deadlineTimer, deadline;
  try {
    const config = gateway();
    const {base} = config;
    key = config.key;
    const body = job.format === "chat"
      ? {model:job.model,messages:[{role:"user",content:challenge.prompt}],stream:job.stream}
      : {model:job.model,input:[{role:"user",content:challenge.prompt}],stream:job.stream,store:false};
    if (job.effort) {
      if (job.format === "chat") body.reasoning_effort = job.effort;
      else body.reasoning = {effort:job.effort};
    }
    const path = job.format === "chat" ? "/chat/completions" : "/responses";
    deadline = new AbortController();
    deadlineTimer = setTimeout(()=>deadline.abort(Error("Request timed out.")),job.timeout*1000);
    const signal = AbortSignal.any([job.controller.signal,deadline.signal]);
    const response = await fetch(base + path, {
      method:"POST",headers:{"Content-Type":"application/json",
        Accept:job.stream?"text/event-stream":"application/json",Authorization:"Bearer " + key},
      body:JSON.stringify(body),signal,redirect:"error"
    });
    if (!response.ok) {
      let reason = "";
      try { const x = await boundedJson(response);reason=String(x?.error?.message||"").slice(0,220); } catch {}
      throw Error("HTTP " + response.status + (reason ? ": " + reason : ""));
    }
    const isSSE=response.headers.get("content-type")?.toLowerCase().includes("text/event-stream");
    let text,capped=false;
    if (isSSE) {
      out.state="streaming";
      ({text,capped}=await streamText(response,job.format,challenge.expected_count,out,
        job.strict,()=>{clearTimeout(deadlineTimer);deadlineTimer=null;}));
    } else {
      const payload=await boundedJson(response);
      if (payload?.error) throw Error("Gateway rejected the challenge.");
      text=pickText(payload,job.format);
      if (text.length>262144) throw Error("Unexpectedly large model response.");
    }
    const numbers = parseNumbers(text);
    out.count = Math.min(numbers.length,challenge.expected_count);
    out.minimumValidNumber = numbers.length
      ? numbers.reduce((minimum,value)=>Math.min(minimum,value),Infinity) : null;
    out.anomalousDistribution = out.minimumValidNumber !== null &&
      out.minimumValidNumber >= ANOMALOUS_MINIMUM;
    if (numbers.length < Math.max(80,Math.ceil(challenge.expected_count*.55)))
      throw Error("Too few valid integers (" + numbers.length + "/" + challenge.expected_count + ").");
    // Match upstream relaxed-mode scoring: ignore integers beyond the requested length.
    out.state = !job.strict && (capped || numbers.length > challenge.expected_count) ? "truncated" : "complete";
    const accepted = !job.strict && numbers.length >= challenge.expected_count
      ? numbers.slice(0,challenge.expected_count).join(", ") : text;
    return {text:accepted,expected_count:challenge.expected_count};
  } catch(e) {
    out.state = job.controller.signal.aborted ? "cancelled" : "failed";
    const message = deadline?.signal.aborted && !job.controller.signal.aborted
      ? "Request timed out ("+job.timeout+"s)." : String(e?.message || e);
    out.error = (job.controller.signal.aborted ? "Cancelled" :
      key ? message.replaceAll(key,"[REDACTED]") : message).slice(0,250);
    return {text:"",expected_count:challenge.expected_count};
  } finally { clearTimeout(deadlineTimer); job.completed++; }
}
async function execute(j,bank,detector) {
  try {
    for (let round=1;round<=j.repeat && !j.controller.signal.aborted;round++) {
      j.round=round;
      const challenges=generateChallenges(j.count);
      j.samples=challenges.map((c,i)=>({
        index:i+1,state:"queued",count:0,expectedCount:c.expected_count,
        minimumValidNumber:null,anomalousDistribution:false,error:null
      }));
      const outputs=new Array(j.count);
      let next=0;
      const worker=async()=>{
        while(next<challenges.length && !j.controller.signal.aborted) {
          const index=next++;
          outputs[index]=await sample(j,index,challenges[index]);
        }
      };
      await Promise.all(Array.from({length:j.parallel},worker));
      if(j.controller.signal.aborted)return;
      let analysis=null,error=null;
      if(!outputs.some(x=>x?.text)) {
        error="All challenges failed: "+(j.samples.find(s=>s.error)?.error||"no valid outputs");
      } else if(j.strict && j.samples.some(s=>s.state!=="complete")) {
        error="Strict mode requires three complete, valid responses.";
      } else {
        const result=analyzeSharedOutputs(outputs,bank,detector,{allowPartial:!j.strict});
        analysis={...result,results:(result.results||[]).slice(0,20)};
      }
      const report={round,samples:j.samples.map(s=>({...s})),analysis,error};
      j.rounds.push(report);
      j.analysis=analysis;
    }
    if(!j.controller.signal.aborted) {
      j.state=j.rounds.some(r=>r.analysis)?"done":"failed";
      if(j.state==="failed")
        j.error=j.rounds.at(-1)?.error||"No rounds could be scored.";
    }
  } catch(e) {
    if(!j.controller.signal.aborted) {
      j.state="failed";
      j.error=String(e?.message||e).slice(0,300);
    }
  } finally {
    j.finishedAt=new Date().toISOString();
    j.controller=null;
    prune();
  }
}
async function start({model,format="responses",stream=false,effort,
  timeout=DEFAULT_TIMEOUT,count=3,parallel=3,repeat=1,strict=false}) {
  if (typeof model !== "string" || model.length > 256 ||
      !/^[^\s\x00-\x1f]+\/[^\s\x00-\x1f]+$/.test(model))
    throw Error("Choose a valid provider/model.");
  if (model.startsWith("group/")) throw Error("Routing groups cannot be fingerprinted reliably.");
  if (!["responses","chat"].includes(format)) throw Error("Unsupported API protocol.");
  if (typeof stream!=="boolean") throw Error("Invalid streaming option.");
  if (effort!==undefined && (typeof effort!=="string" || !(effort.length >= 1 && effort.length <= 64 && effort.trim() === effort && [...effort].every(ch => ch.codePointAt(0) >= 32 && ch.codePointAt(0) !== 127))))
    throw Error("Invalid reasoning effort.");
  if (typeof timeout!=="number" || !Number.isFinite(timeout) || timeout<0.1 || timeout>600)
    throw Error("Timeout must be between 0.1 and 600 seconds.");
  if (!Number.isInteger(count) || count<1 || count>3)
    throw Error("Sample count must be 1–3.");
  if (!Number.isInteger(parallel) || parallel<1 || parallel>3)
    throw Error("Parallel requests must be 1–3.");
  parallel=Math.min(parallel,count);
  if (!Number.isInteger(repeat) || repeat<1 || repeat>MAX_REPEAT)
    throw Error("Repeat must be between 1 and "+MAX_REPEAT+".");
  if(typeof strict!=="boolean")throw Error("Invalid strict option.");
  if(strict && count!==3)throw Error("Strict mode requires count=3.");
  if ([...jobs.values()].filter(x=>x.state==="running").length + starting >= MAX_RUNNING)
    throw Error("Two detections are already running.");
  starting++;
  try {
    if (!(await models()).includes(model)) throw Error("Model is not currently exposed by Gateway.");
    const {bank,detector} = await references();
    const j = {id:randomUUID(),model,format,stream,effort,timeout,count,parallel,repeat,strict,
      state:"running",startedAt:new Date().toISOString(),finishedAt:null,
      completed:0,round:0,rounds:[],samples:[],analysis:null,error:null,controller:new AbortController()};
    jobs.set(j.id,j);
    prune();
    void execute(j,bank,detector);
    return publicJob(j);
  } finally { starting--; }
}
function cancel(id) {
  const j = findJob(id);
  if (j.state === "running") {
    j.state = "cancelled";
    j.finishedAt = new Date().toISOString();
    j.controller?.abort();
  }
  return publicJob(j);
}

export const Fingerprint = async () => ({tool:{
  models:{description:"List Magpie Gateway model IDs for fingerprinting.",
    args:schema({},[]),async execute(){return {models:await models()};}},
  start:{description:"Start native LM Detector analysis with configurable samples and rounds.",
    args:schema({model:stringArg,format:{type:"string",enum:["responses","chat"]},
      stream:{type:"boolean"},effort:{type:"string",minLength:1,maxLength:64},
      timeout:{type:"number",minimum:0.1,maximum:600},
      count:{type:"integer",minimum:1,maximum:3},
      parallel:{type:"integer",minimum:1,maximum:3},
      repeat:{type:"integer",minimum:1,maximum:MAX_REPEAT},
      strict:{type:"boolean"}},["model"]),
    async execute(args){return await start(args);}},
  status:{description:"Check fingerprint job status and relative candidate rankings.",
    args:schema({jobId:stringArg},["jobId"]),
    async execute({jobId}){return publicJob(findJob(jobId));}},
  cancel:{description:"Cancel the current fingerprint job.",
    args:schema({jobId:stringArg},["jobId"]),
    async execute({jobId}){return cancel(jobId);}}
}});
