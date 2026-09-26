// SPDX-FileCopyrightText: 2026 Scitrera LLC
// SPDX-FileCopyrightText: 2026 Fox Engine Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

import { useEffect, useId, useRef, useState } from "react";
import { ProfileEditorDialog } from "./ProfileEditorDialog";
import { LinkedProfileEditor } from "./LinkedProfileEditor";
import { ProfileAPIConfigurations } from "./ProfileAPIConfigurations";
import { apiLabel, profileAPIWarnings, profileConfigurations, type APIConfiguration } from "./profileAPIConfigurations";
import type { JSONObject } from "./extensions";

type Profile = { model: JSONObject; readOnly: boolean };
const operations = [
  ["chat_completions", "OpenAI chat"], ["responses", "OpenAI responses"],
  ["responses_compact", "OpenAI responses compact"], ["messages", "Anthropic messages"],
  ["generate_content", "Gemini generate content"], ["stream_generate_content", "Gemini streaming"],
  ["converse", "Bedrock converse"], ["converse_stream", "Bedrock streaming"],
] as const;
const objectValue = (value: unknown): JSONObject => value && typeof value === "object" && !Array.isArray(value) ? value as JSONObject : {};
const defaults = (operation: string) => operation.startsWith("responses") ? { reasoning: { effort: "low" } }
  : operation === "chat_completions" ? { reasoning_effort: "low" } : {};

export function RequestProfileEditor({ model, profiles, names, disabled, deployments = [], providers = [], sources = {}, onAdd, onChange, onDelete }: {
  model: JSONObject; profiles: Profile[]; names: string[]; disabled: boolean; deployments?: JSONObject[]; providers?: JSONObject[]; sources?: JSONObject;
  onAdd: (profile: JSONObject) => void;
  onChange: (name: string, profile: JSONObject) => void;
  onDelete: (name: string) => void;
}) {
  const [editor, setEditor] = useState<{ originalName: string | null; profile: JSONObject } | null>(null);
  const [selector, setSelector] = useState("low");
  const [operation, setOperation] = useState("chat_completions");
  const [parameters, setParameters] = useState("");
  const [overrides, setOverrides] = useState<JSONObject>({});
  const [error, setError] = useState("");
  const id = useId();
  const addButton = useRef<HTMLButtonElement>(null);
  const close = () => { setEditor(null); setError(""); };
  const targetNames = new Set((Array.isArray(model.pools) ? model.pools : []).flatMap(pool => {
    const targets = objectValue(pool).targets; return Array.isArray(targets) ? targets.map(target => String(objectValue(target).deployment)) : [];
  }));
  const targets = deployments.filter(deployment => targetNames.has(String(deployment.name))).map(deployment => {
    const source = objectValue(sources[String(deployment.name)]), baseline = objectValue(deployment.profile_source);
    return Boolean(baseline.recipe) && source.recipe === baseline.recipe && source.recipe_revision === baseline.recipe_revision && source.baseline_revision === baseline.revision
      ? {...deployment, request_profiles: source.profiles ?? {}, profile_source: source} : deployment;
  });
  const available = [...new Set(targets.flatMap(deployment => Object.keys(objectValue(deployment.request_profiles))))].sort();

  useEffect(() => { setEditor(null); setError(""); }, [model.name]);
  const suffix = (name: string) => name.startsWith(`${model.name}:`) ? name.slice(String(model.name).length + 1) : name;

  function open(profile?: JSONObject) {
    const current = objectValue(profile?.request_overrides);
    const api = Object.keys(current)[0] ?? "chat_completions";
    setEditor({ originalName: profile ? String(profile.name) : null, profile: profile ?? model });
    setSelector(profile ? suffix(String(profile.name)) : "low");
    setOperation(api);
    setOverrides(current);
    setParameters(JSON.stringify(current[api] ?? defaults(api), null, 2));
    setError("");
  }
  function parsedParameters(): JSONObject {
    const parsed: unknown = JSON.parse(parameters);
    if (!parsed || Array.isArray(parsed) || typeof parsed !== "object") throw new Error("Parameters must be a JSON object.");
    return parsed as JSONObject;
  }
  function changeOperation(api: string): boolean {
    try {
      const parsed = parsedParameters();
      const changed = operation in overrides || JSON.stringify(parsed) !== JSON.stringify(defaults(operation));
      const next = changed ? { ...overrides, [operation]: parsed } : overrides;
      setOverrides(next);
      setOperation(api);
      setParameters(JSON.stringify(next[api] ?? defaults(api), null, 2));
      setError("");
      return true;
    } catch (e) { setError(e instanceof Error ? e.message : "Invalid profile parameters."); return false; }
  }
  function apply() {
    if (!editor || disabled) return;
    try {
      const values = parsedParameters();
      // A base model with overrides is editable without changing its public name.
      const isBase = editor.originalName === String(model.name);
      const name = isBase ? String(model.name) : `${model.name}:${selector.trim()}`;
      if (!isBase && (!/^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$/.test(selector.trim()) || name !== editor.originalName && names.includes(name))) {
        throw new Error("Choose a unique profile selector using 1–64 letters, numbers, dots, underscores, or hyphens.");
      }
      const current = editor.originalName === null ? model : profiles.find(({ model: profile }) => profile.name === editor.originalName)?.model ?? editor.profile;
      const result = editor.originalName === null
        ? {name, profile: {parent: model.name, selector: selector.trim(), manual: true, patch: {ingress_overrides:{...overrides, [operation]: values}}}}
        : { ...current, name, request_overrides: { ...overrides, [operation]: values } };
      if (editor.originalName === null) onAdd(result); else onChange(editor.originalName, result);
      setEditor(null); setError("");
    } catch (e) { setError(e instanceof Error ? e.message : "Invalid profile parameters."); }
  }
  const simpleEntries: APIConfiguration[] = [...new Set([...Object.keys(overrides),operation])].map(api=>({operation:api,stage:"ingress",origins:api in overrides ? ["Operator overrides"] : []}));
  const simpleWarnings=profileAPIWarnings({manual:true},targets,providers,operation,"ingress");
  const editForm = editor && editor.profile.profile ? <LinkedProfileEditor key={String(editor.originalName)}
    profile={editor.profile} parent={model} deployments={targets} providers={providers} disabled={disabled} formId={`${id}-editor`}
    onApply={profile => {onChange(String(editor.originalName), profile); close();}}
    /> : editor && <form className="profile-editor" id={`${id}-editor`} onSubmit={event => {event.preventDefault(); apply();}}>
    <fieldset disabled={disabled}>
      <ProfileAPIConfigurations entries={simpleEntries} operation={operation} stage="ingress" disabled={disabled} incomingOnly onSelect={api=>changeOperation(api)} warnings={api=>profileAPIWarnings({manual:true},targets,providers,api,"ingress")} />
      {simpleWarnings.length>0 && <div role="status" aria-label="API availability" className="profile-api-notice"><strong>API availability</strong><ul>{simpleWarnings.map(warning=><li key={warning}>{warning}</li>)}</ul></div>}
      <div className="model-field-grid">
        {editor.originalName !== String(model.name) && <label>Selector
          <input aria-label="Profile selector" value={selector} onChange={(e) => setSelector(e.target.value)} placeholder="xhigh" />
          <small>Public name: {String(model.name)}:{selector}</small>
        </label>}
        <label>Request API<select aria-label="Request API" value={operation} onChange={(e) => changeOperation(e.target.value)}>
          {!operations.some(([value]) => value === operation) && <option value={operation}>{operation}</option>}
          {operations.map(([value, label]) => <option key={value} value={value}>{label}</option>)}
        </select></label>
        <label className="wide">Request parameters (JSON)
          <textarea aria-label="Request parameters (JSON)" rows={5} spellCheck={false} value={parameters} onChange={(e) => setParameters(e.target.value)} />
        </label>
      </div>
      <p className="section-help">Values override caller parameters for the selected API before protocol translation. Choose values supported by the underlying model.</p>
      {error && <p className="notice error" role="alert">{error}</p>}
    </fieldset>
  </form>;

  return <section className="model-section request-profiles" aria-labelledby={`${id}-title`}>
    <div className="model-section-heading">
      <h4 id={`${id}-title`}>Request profiles</h4>
      <button ref={addButton} className="secondary-button" type="button" disabled={disabled} onClick={() => open()}>Add profile</button>
    </div>
    <p className="section-help">Profiles inherit this model's routing. Recipe parameters follow their source; your overrides stay in place.</p>
    {available.length > 0 && <div className="recipe-actions" aria-label="Available recipe profiles">{available.map(selector => {
      const name = `${model.name}:${selector}`;
      return <button key={selector} type="button" className="secondary-button" disabled={disabled || names.includes(name)} onClick={() => onAdd({name, profile: {parent:model.name, selector}})}>Use recipe profile {selector}</button>;
    })}</div>}
    <div className="profile-table-wrap"><table className="profile-table" aria-label="Request profiles">
      <colgroup><col className="profile-selector-column" /><col /><col className="profile-options-column" /></colgroup>
      <thead><tr><th scope="col">Selector</th><th scope="col">API configurations</th><th scope="col">Options</th></tr></thead>
      <tbody>
        {profiles.map(({ model: profile, readOnly }) => {
          const name = String(profile.name);
          const configured = profileConfigurations(objectValue(profile.profile),model,profile,targets);
          const summary = configured.map(entry=>`${entry.stage==="ingress" ? "Incoming" : "Runtime"}: ${apiLabel(entry.operation)}`).join(" · ") || "No API parameters configured";
          const parameterJSON = JSON.stringify(profile.profile ? objectValue(profile.profile).patch ?? {} : profile.request_overrides);
          return <tr key={name} className={readOnly ? "generated-entry" : undefined}>
              <th scope="row"><span className="profile-selector" title={name}>{name === String(model.name) ? "(default)" : suffix(name)}</span></th>
              <td><span className="profile-summary" title={parameterJSON}>{summary}</span></td>
              <td><div className="profile-row-actions">
                <button className="icon-button" type="button" title={`Edit profile ${name}`} aria-label={`Edit profile ${name}`} aria-haspopup="dialog" disabled={disabled || readOnly} onClick={() => open(profile)}>
                  <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><path d="m15 5 4 4M4 20l4-1L20 7a2.8 2.8 0 0 0-4-4L4 15z" /></svg>
                </button>
                {!profile.profile && name !== String(model.name) && <button type="button" className="secondary-button" disabled={disabled || readOnly} onClick={() => {
                  const linked = {name, aliases:profile.aliases ?? [],
                    profile:{parent:model.name, selector:suffix(name), manual:!available.includes(suffix(name)),patch:{ingress_overrides:profile.request_overrides}}};
                  setEditor({originalName:name, profile:{...linked, migration_preview:{old_pools:profile.pools, inherited_pools:model.pools}}});
                }}>Review migration</button>}
              </div></td>
            </tr>;
        })}
        {!profiles.length && <tr><td colSpan={3} className="profile-empty">No request profiles configured.</td></tr>}
      </tbody>
    </table></div>
    {editor && <ProfileEditorDialog name={editor.originalName} formId={`${id}-editor`} disabled={disabled} fallbackFocus={addButton}
      defaultProfile={editor.originalName === String(model.name)} onCancel={close}
      onDelete={editor.originalName === null ? undefined : () => {if (!disabled) {onDelete(editor.originalName!); close();}}}>
      {editForm}
    </ProfileEditorDialog>}
  </section>;
}
