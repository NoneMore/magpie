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
const DEFAULT_GATEWAY = "http://127.0.0.1:3425/v1";
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
  return [...new Set(payload.data.map(row => row?.id).filter(valid))].slice(0,2000).sort();
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
    startedAt:j.startedAt,finishedAt:j.finishedAt || null,
    completed:j.completed,samples:j.samples.map(s=>({...s})),
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
    const m = payload?.choices?.[0]?.message;
    if (m?.refusal) throw Error("Model refused the fingerprint challenge.");
    const c = m?.content;
    return typeof c === "string" ? c : Array.isArray(c) ?
      c.map(x=>typeof x?.text==="string"?x.text:"").join("") : "";
  }
  if (payload?.status === "failed" || payload?.status === "incomplete")
    throw Error("Responses request did not complete.");
  return (Array.isArray(payload?.output)?payload.output:[])
    .filter(x=>x?.type==="message")
    .flatMap(x=>Array.isArray(x?.content)?x.content:[])
    .filter(x=>x?.type==="output_text")
    .map(x=>typeof x.text==="string"?x.text:"").join("");
}
async function sample(job,index,challenge) {
  const out = job.samples[index];
  out.state = "waiting";
  try {
    const {base,key} = gateway();
    const body = job.format === "chat"
      ? {model:job.model,messages:[{role:"user",content:challenge.prompt}],stream:false}
      : {model:job.model,input:[{role:"user",content:challenge.prompt}],stream:false,store:false};
    const path = job.format === "chat" ? "/chat/completions" : "/responses";
    const signal = AbortSignal.any([job.controller.signal,AbortSignal.timeout(250000)]);
    const response = await fetch(base + path, {
      method:"POST",headers:{"Content-Type":"application/json",Authorization:"Bearer " + key},
      body:JSON.stringify(body),signal,redirect:"error"
    });
    if (!response.ok) {
      let reason = "";
      try { const x = await response.json();reason=String(x?.error?.message||"").slice(0,220); } catch {}
      throw Error("HTTP " + response.status + (reason ? ": " + reason : ""));
    }
    const payload = await response.json();
    if (payload?.error) throw Error("Gateway rejected the challenge.");
    const text = pickText(payload,job.format);
    if (text.length > 262144) throw Error("Unexpectedly large model response.");
    const numbers = parseNumbers(text);
    out.count = numbers.length;
    if (numbers.length < Math.max(80,Math.ceil(challenge.expected_count*.55)))
      throw Error("Too few valid integers (" + numbers.length + "/" + challenge.expected_count + ").");
    out.state = "complete";
    return {text,expected_count:challenge.expected_count};
  } catch(e) {
    out.state = job.controller.signal.aborted ? "cancelled" : "failed";
    const {key} = gateway();
    out.error = (job.controller.signal.aborted ? "Cancelled" :
      String(e?.message || e).replaceAll(key,"[REDACTED]")).slice(0,250);
    return {text:"",expected_count:challenge.expected_count};
  } finally { job.completed++; }
}
async function execute(j,bank,detector) {
  try {
    const challenges = generateChallenges(3);
    j.samples = challenges.map((c,i)=>({
      index:i+1,state:"queued",count:0,expectedCount:c.expected_count,error:null
    }));
    const outputs = await Promise.all(challenges.map((c,i)=>sample(j,i,c)));
    if (j.controller.signal.aborted) return;
    if (!outputs.some(x=>x.text)) {
      j.state = "failed";
      j.error = "All challenges failed: " + (j.samples.find(s=>s.error)?.error || "no valid outputs");
      return;
    }
    const analysis = analyzeSharedOutputs(outputs,bank,detector,{allowPartial:true});
    j.analysis = {...analysis,results:(analysis.results || []).slice(0,20)};
    j.state = "done";
  } catch(e) {
    if (!j.controller.signal.aborted) {
      j.state = "failed";
      j.error = String(e?.message||e).slice(0,300);
    }
  } finally {
    j.finishedAt = new Date().toISOString();
    j.controller = null;
    prune();
  }
}
async function start(model,format="responses") {
  if (typeof model !== "string" || model.length > 256 ||
      !/^[^\s\x00-\x1f]+\/[^\s\x00-\x1f]+$/.test(model))
    throw Error("Choose a valid provider/model.");
  if (!["responses","chat"].includes(format)) throw Error("Unsupported API protocol.");
  if ([...jobs.values()].filter(x=>x.state==="running").length + starting >= MAX_RUNNING)
    throw Error("Two detections are already running.");
  starting++;
  try {
    if (!(await models()).includes(model)) throw Error("Model is not currently exposed by Gateway.");
    const {bank,detector} = await references();
    const j = {id:randomUUID(),model,format,state:"running",startedAt:new Date().toISOString(),
      finishedAt:null,completed:0,samples:[],analysis:null,error:null,controller:new AbortController()};
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
  start:{description:"Start native LM Detector analysis (three model challenges).",
    args:schema({model:stringArg,format:{type:"string",enum:["responses","chat"]}},["model"]),
    async execute({model,format}){return await start(model,format);}},
  status:{description:"Check fingerprint job status and relative candidate rankings.",
    args:schema({jobId:stringArg},["jobId"]),
    async execute({jobId}){return publicJob(findJob(jobId));}},
  cancel:{description:"Cancel the current fingerprint job.",
    args:schema({jobId:stringArg},["jobId"]),
    async execute({jobId}){return cancel(jobId);}}
}});
