// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Scitrera LLC
// Copyright 2026 Fox Engine Ltd.
import {useState} from "react";
import type {JSONObject} from "./extensions";
import { ProfileAPIConfigurations } from "./ProfileAPIConfigurations";
import { apiLabel, configurationKey, profileAPIs, profileAPIWarnings, profileConfigurations, recipeIncomingAPIs, type APIConfiguration, type ProfileStage } from "./profileAPIConfigurations";
const obj = (v: unknown): JSONObject => v && typeof v === "object" && !Array.isArray(v) ? v as JSONObject : {};
const apis = Object.keys(profileAPIs);
function merge(a: JSONObject, b: JSONObject): JSONObject {
 const result = structuredClone(a);
 for (const [key,value] of Object.entries(b)) result[key] = value && typeof value === "object" && !Array.isArray(value) && result[key] && typeof result[key] === "object" && !Array.isArray(result[key]) ? merge(obj(result[key]),obj(value)) : value;
 return result;
}
function suppress(value: JSONObject, paths: string[]): JSONObject {
 const result = structuredClone(value);
 function remove(target: JSONObject, keys: string[]) {
  const [key, ...rest] = keys;
  if (key === undefined) return;
  if (!rest.length) { delete target[key]; return; }
  if (!target[key] || typeof target[key] !== "object" || Array.isArray(target[key])) return;
  const child = obj(target[key]);
  remove(child, rest);
  if (!Object.keys(child).length) delete target[key];
 }
 for (const path of paths) remove(result, path.slice(1).split("/").map(k => k.replaceAll("~1","/").replaceAll("~0","~")));
 return result;
}
export function LinkedProfileEditor({profile, parent, deployments, providers = [], disabled, formId, onApply}: {
 profile: JSONObject; parent: JSONObject; deployments: JSONObject[]; providers?: JSONObject[]; disabled: boolean;
 formId: string; onApply: (profile: JSONObject) => void;
}) {
 const [binding,setBinding] = useState(obj(profile.profile));
 const [target,setTarget] = useState("");
 const initial = profileConfigurations(obj(profile.profile), parent, profile, deployments)[0] ?? {operation:"chat_completions", stage:"ingress" as const};
 const [operation,setOperation] = useState(initial.operation);
 const [stage,setStage] = useState<ProfileStage>(initial.stage);
 const [draftEntries,setDraftEntries] = useState<APIConfiguration[]>([]);
 const [error,setError] = useState("");
 const selectedPatch = (b: JSONObject, t: string) => t ? obj(obj(b.deployment_overrides)[t]) : obj(b.patch);
 const [parameters,setParameters] = useState(JSON.stringify(obj(obj(binding.patch)[`${initial.stage}_overrides`])[initial.operation] ?? {},null,2));
 const [removed,setRemoved] = useState((obj(obj(binding.patch)[`remove_${initial.stage}`])[initial.operation] as string[] ?? []).join("\n"));
 function saveFields(reset = false): JSONObject {
  const parsed: unknown = reset ? {} : JSON.parse(parameters);
  if (!parsed || Array.isArray(parsed) || typeof parsed !== "object") throw Error("Overrides must be a JSON object.");
  const paths = reset ? [] : removed.split("\n").map(s=>s.trim()).filter(Boolean);
  if (paths.some(path=>!path.startsWith("/") || path==="/")) throw Error("Suppressed fields must be JSON pointers such as /temperature.");
  const patch = {...selectedPatch(binding,target)}, key = `${stage}_overrides`, removeKey = `remove_${stage}`;
  const values = {...obj(patch[key])}, removals = {...obj(patch[removeKey])};
  if (reset || !Object.keys(parsed).length) delete values[operation]; else values[operation] = parsed;
  if (!paths.length) delete removals[operation]; else removals[operation] = paths;
  patch[key]=values; patch[removeKey]=removals;
  return target ? {...binding, deployment_overrides:{...obj(binding.deployment_overrides),[target]:patch}} : {...binding,patch};
 }
 function switchFields(t: string, api: string, s: ProfileStage): boolean {
  try {
   const next=saveFields();setBinding(next);setTarget(t);setOperation(api);setStage(s);
   const patch=selectedPatch(next,t);
   setParameters(JSON.stringify(obj(patch[`${s}_overrides`])[api] ?? {},null,2));
   setRemoved((obj(patch[`remove_${s}`])[api] as string[] ?? []).join("\n"));setError("");
   setDraftEntries(entries => entries.some(entry => entry.operation === api && entry.stage === s) ? entries : [...entries,{operation:api,stage:s,origins:[]}]);
   return true;
  } catch(e){setError(String(e));return false;}
 }
 function apply(){if(disabled)return;try{const next: JSONObject={...profile,profile:saveFields()};delete next.migration_preview;onApply(next);}catch(e){setError(String(e));}}
 let preview=binding;
 try {preview=saveFields();} catch { /* Keep the last valid configuration list while JSON is incomplete. */ }
 const entries=profileConfigurations(preview,parent,profile,deployments);
 for (const entry of draftEntries) if (!entries.some(value=>configurationKey(value)===configurationKey(entry))) entries.push(entry);
 const scopedDeployments=target ? deployments.filter(deployment=>deployment.name===target) : deployments;
 const warnings=profileAPIWarnings(preview,scopedDeployments,providers,operation,stage);
 return <form id={formId} className="profile-editor linked-profile-editor" onSubmit={event=>{event.preventDefault();apply();}}>
  <p>Routing and policies follow {String(parent.name)}. Only fields entered below override the recipe. Empty objects inherit; JSON null remains a value.</p>
  {Boolean(profile.migration_preview) && <div role="status"><strong>Migration preview</strong><p>Existing parameters remain pinned as operator overrides. Routing will follow the parent.</p><pre>{JSON.stringify(profile.migration_preview,null,2)}</pre></div>}
  <fieldset disabled={disabled} className="linked-profile-fields">
   <label>Implementation<select aria-label="Profile implementation" value={binding.manual ? "manual" : "recipe"} onChange={e=>setBinding({...binding,manual:e.target.value==="manual"})}><option value="recipe">Follow deployment recipes</option><option value="manual">Operator parameters only</option></select></label>
   <label>Recipe selector<input aria-label="Recipe selector" value={String(binding.selector)} onChange={e=>setBinding({...binding,selector:e.target.value})}/></label>
   <ProfileAPIConfigurations entries={entries} operation={operation} stage={stage} disabled={disabled}
    onSelect={(api,s)=>switchFields(target,api,s)} warnings={(api,s)=>profileAPIWarnings(preview,deployments,providers,api,s)} />
   <label>Override scope<select aria-label="Override scope" value={target} onChange={e=>switchFields(e.target.value,operation,stage)}><option value="">All deployments</option>{deployments.map(d=><option key={String(d.name)} value={String(d.name)}>{String(d.title ?? d.name)}</option>)}</select></label>
   <label>Parameter stage<select aria-label="Parameter stage" value={stage} onChange={e=>switchFields(target,operation,e.target.value as ProfileStage)}><option value="ingress">Incoming API, before translation</option><option value="upstream">Runtime API, after translation</option></select></label>
   <label>Request API<select aria-label="Request API" value={operation} onChange={e=>switchFields(target,e.target.value,stage)}>{apis.map(api=><option key={api} value={api}>{apiLabel(api)}</option>)}</select></label>
   <p className="profile-api-explanation wide">{stage==="ingress" ? `These parameters apply when the client calls ${apiLabel(operation)}, before translation.` : `These parameters apply when the request sent to the runtime uses ${apiLabel(operation)}, after translation.`} Other API configurations stay in this profile.</p>
   {warnings.length>0 && <div role="status" aria-label="API availability" className="profile-api-notice wide"><strong>This configuration will not apply on every deployment in this scope.</strong><ul>{warnings.map(warning=><li key={warning}>{warning}</li>)}</ul><p>You can keep these parameters for a future configuration. They do not enable an API.</p></div>}
   <label className="wide">Operator overrides (JSON)<textarea aria-label="Request parameters (JSON)" rows={6} value={parameters} onChange={e=>setParameters(e.target.value)}/></label>
   <label className="wide">Suppress inherited parameters<textarea aria-label="Suppress inherited parameters" placeholder="/temperature" value={removed} onChange={e=>setRemoved(e.target.value)}/><small>One JSON pointer per line. Suppression removes a recipe override and lets caller values through.</small></label>
   <button type="button" className="secondary-button" onClick={()=>{try{setBinding(saveFields(true));setParameters("{}");setRemoved("");setDraftEntries(entries=>entries.filter(entry=>entry.operation!==operation || entry.stage!==stage));setError("");}catch(e){setError(String(e));}}}>Reset to inherited</button>
   <small className="wide">Reset removes only the selected scope, stage and API override. Recipe and other API configurations remain.</small>
   <details open className="wide"><summary>Deployment parameters and source status</summary>{deployments.map(d=>{
    const impl=obj(obj(d.request_profiles)[String(binding.selector)]), source=obj(d.profile_source);
    const base=obj(obj(impl[`${stage}_overrides`] ?? (stage==="ingress" ? impl : {}))[operation]);
    let effective=merge(merge(binding.manual ? {} : base,stage==="ingress" ? obj(obj(parent.request_overrides)[operation]) : {}),stage==="ingress" ? obj(obj(profile.request_overrides)[operation]) : {});
    for(const patch of [obj(preview.patch),obj(obj(preview.deployment_overrides)[String(d.name)])]) effective=suppress(merge(effective,obj(obj(patch[`${stage}_overrides`])[operation])),obj(patch[`remove_${stage}`])[operation] as string[] ?? []);
    const excluded = Array.isArray(binding.excluded_deployments) ? binding.excluded_deployments as string[] : [];
    return <div className="profile-source-status" key={String(d.name)}><strong>{String(d.title ?? d.name)}</strong>
    <label className="checkbox-field"><input type="checkbox" checked={!excluded.includes(String(d.name))} onChange={event => setBinding({...binding, excluded_deployments:event.target.checked ? excluded.filter(name=>name!==d.name) : [...excluded,String(d.name)]})}/>Use deployment {String(d.title ?? d.name)}</label>
    <p>{binding.manual ? "Operator implementation" : Object.keys(impl).length ? "Recipe implementation" : "Profile unavailable on this deployment"}</p>
    {!binding.manual && Object.keys(impl).length>0 && <p>Supported incoming APIs: {recipeIncomingAPIs(impl).map(apiLabel).join(", ")}</p>}
    {Boolean(source.recipe) && <p>{String(source.kind)==="snapshot" ? "Uploaded recipe snapshot" : "Automatically follows recipe"}: {String(source.recipe)} · revision <span title={String(source.revision ?? "pending")}>{String(source.revision ?? "pending").slice(0,12)}</span></p>}
    {Boolean(source.error) && <p role="status">{String(source.error)}</p>}<pre aria-label={`Effective parameters ${String(d.name)}`}>{JSON.stringify(effective,null,2)}</pre></div>;
   })}</details>
   {error && <p role="alert">{error}</p>}
  </fieldset>
 </form>;
}
