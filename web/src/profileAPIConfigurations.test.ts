// SPDX-FileCopyrightText: 2026 Scitrera LLC
// SPDX-FileCopyrightText: 2026 Fox Engine Ltd.
// SPDX-License-Identifier: AGPL-3.0-only
import { expect, it } from "vitest";
import { profileAPIWarnings, recipeIncomingAPIs } from "./profileAPIConfigurations";

const providers=[{name:"local",type:"openai"}];
const binding={selector:"quick"};
const deployment={name:"runtime",provider:"local",request_profiles:{quick:{supported_operations:["chat_completions","responses"],upstream_overrides:{chat_completions:{temperature:0.2}}}}};
it("distinguishes recipe support from translated and native API availability",()=>{
 expect(profileAPIWarnings(binding,[deployment],providers,"responses","ingress")).toEqual([]);
 expect(profileAPIWarnings(binding,[deployment],providers,"chat_completions","upstream")).toEqual([]);
 expect(profileAPIWarnings(binding,[deployment],providers,"responses","upstream")[0]).toContain("none of this profile's supported incoming APIs can reach runtime Responses");
 expect(profileAPIWarnings(binding,[deployment],providers,"messages","ingress")[0]).toContain("recipe does not support");
 const manual={...binding,manual:true};
 expect(profileAPIWarnings(manual,[deployment],providers,"messages","ingress")[0]).toContain("no declared native API");
 expect(profileAPIWarnings(manual,[{...deployment,native_protocols:["anthropic"]}],providers,"messages","ingress")).toEqual([]);
 expect(profileAPIWarnings(manual,[{...deployment,native_protocols:["anthropic"]}],providers,"chat_completions","ingress")).toEqual([]);
 expect(profileAPIWarnings(manual,[deployment],[{name:"local",type:"openai_responses"}],"chat_completions","ingress")[0]).toContain("no declared native API");
});
it("reports only affected deployments and respects exclusions and unknown providers",()=>{
 const native={...deployment,name:"native",capabilities:["responses"]};
 expect(profileAPIWarnings(binding,[deployment,native],providers,"responses","upstream")).toHaveLength(1);
 expect(profileAPIWarnings({...binding,excluded_deployments:["runtime"]},[deployment,native],providers,"responses","upstream")).toEqual([]);
 expect(profileAPIWarnings(binding,[deployment],[],"responses","upstream")).toEqual([]);
});
it("infers recipe support from ingress when supported_operations is empty or absent",()=>{
 expect(recipeIncomingAPIs({supported_operations:[],ingress_overrides:{responses:{temperature:0.2}}})).toEqual(["responses"]);
 expect(recipeIncomingAPIs({chat_completions:{temperature:0.2}})).toEqual(["chat_completions"]);
 expect(recipeIncomingAPIs({supported_operations:["messages"],ingress_overrides:{responses:{temperature:0.2}}})).toEqual(["messages"]);
});
