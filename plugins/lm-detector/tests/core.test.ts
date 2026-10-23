import {expect, test} from "bun:test";
import {generateChallenges} from "../shared/challenge-browser.js";
import {parseNumbers, countNumbers, orderedBlockFeature} from "../shared/fingerprint-core.js";
import {decodeDetector} from "../shared/detector-codec.ts";
import {supportsSharedDetector} from "../shared/shared-detector.ts";

test("three challenges have independent valid lengths", () => {
  const challenges=generateChallenges(3);
  expect(challenges).toHaveLength(3);
  expect(new Set(challenges.map(x=>x.expected_count)).size).toBe(3);
  for(const c of challenges) {
    expect(c.expected_count).toBeGreaterThanOrEqual(292);
    expect(c.expected_count).toBeLessThanOrEqual(332);
    expect(c.prompt).toContain("355");
  }
});

test("number parser and features preserve the upstream rules", () => {
  expect(parseNumbers("1, 2, 356, 355")).toEqual([1,2,355]);
  expect(parseNumbers("1 2 words 3,4,5")).toEqual([3,4,5]);
  const values=[1,20,355,20];
  expect(countNumbers(values).reduce((a,b)=>a+b,0)).toBe(4);
  expect(orderedBlockFeature(values)).toHaveLength(74);
});

test("binary decoder reads the column-interleaved float encoding", () => {
  const header=new TextEncoder().encode(JSON.stringify({schema:"test",weights:{$f32:0,n:2}}));
  const offset=Math.ceil((4+header.length)/4)*4;
  const bytes=new Uint8Array(offset+8), view=new DataView(bytes.buffer);
  view.setUint32(0,header.length,true);bytes.set(header,4);
  const floats=new Uint8Array(8),fv=new DataView(floats.buffer);
  fv.setFloat32(0,1.5,true);fv.setFloat32(4,-2.25,true);
  for(let i=0;i<2;i++)for(let b=0;b<4;b++)bytes[offset+b*2+i]=floats[i*4+b];
  const result=decodeDetector(bytes);
  expect(result.weights).toEqual([1.5,-2.25]);
});

test("detector and bank must describe the exact same references", () => {
  const bank={built_at:"fixture",models:[{id:"a",response_count:3}]};
  const artifact={schema:"shared-detector-v2",bank_built_at:"fixture",model_ids:["a"],response_counts:[3]};
  expect(supportsSharedDetector(bank,artifact)).toBe(true);
  expect(supportsSharedDetector({...bank,built_at:"other"},artifact)).toBe(false);
});
