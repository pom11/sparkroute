// SPDX-FileCopyrightText: 2026 Scitrera LLC
// SPDX-FileCopyrightText: 2026 Fox Engine Ltd.
// SPDX-License-Identifier: AGPL-3.0-only
import { useState } from "react";
import { apiLabel, configurationKey, profileAPIs, stageLabel, type APIConfiguration, type ProfileStage } from "./profileAPIConfigurations";

export function ProfileAPIConfigurations({ entries, operation, stage, disabled, incomingOnly = false, onSelect, warnings = () => [] }: {
  entries: APIConfiguration[]; operation: string; stage: ProfileStage; disabled: boolean; incomingOnly?: boolean;
  onSelect: (operation: string, stage: ProfileStage) => boolean; warnings?: (operation: string, stage: ProfileStage) => string[];
}) {
  const [adding, setAdding] = useState(false);
  const [newAPI, setNewAPI] = useState("responses");
  const [newStage, setNewStage] = useState<ProfileStage>("ingress");
  const existing = entries.some(entry => entry.operation === newAPI && entry.stage === newStage);
  return <section className="profile-api-configurations wide" aria-label="API configurations">
    <div className="profile-api-heading"><h6>API configurations</h6><button type="button" className="secondary-button" disabled={disabled} onClick={() => {
      const nextStage=incomingOnly ? "ingress" : stage;
      setNewStage(nextStage);
      setNewAPI(Object.keys(profileAPIs).find(api=>!entries.some(entry=>entry.operation===api && entry.stage===nextStage)) ?? operation);
      setAdding(true);
    }}>Add API configuration</button></div>
    <p>Each entry has its own parameters. Switching entries keeps your edits; Apply saves them all to the draft.</p>
    {entries.length ? <ul className="profile-api-list">{entries.map(entry => {
      const problems = warnings(entry.operation, entry.stage);
      return <li key={configurationKey(entry)}><button type="button" disabled={disabled}
        aria-label={`Edit ${stageLabel(entry.stage)}: ${apiLabel(entry.operation)}`}
        aria-pressed={entry.operation === operation && entry.stage === stage} onClick={() => onSelect(entry.operation, entry.stage)}>
        <span className="profile-api-entry-heading"><strong>{apiLabel(entry.operation)}</strong><span>{stageLabel(entry.stage)}</span></span>
        <code>{entry.operation}</code><small>{entry.origins.join(" · ") || "New · enter parameters to save"}</small>
        {problems.length > 0 && <small className="profile-api-warning">Unavailable on {problems.length} deployment{problems.length === 1 ? "" : "s"}</small>}
      </button></li>;
    })}</ul> : <p>No API parameters configured yet.</p>}
    {adding && <div className="profile-api-add">
      <label>API<select aria-label="New configuration API" value={newAPI} onChange={event => setNewAPI(event.target.value)}>{Object.entries(profileAPIs).map(([key,label]) => <option key={key} value={key}>{label}</option>)}</select></label>
      {!incomingOnly && <label>Stage<select aria-label="New configuration stage" value={newStage} onChange={event => setNewStage(event.target.value as ProfileStage)}><option value="ingress">Incoming API, before translation</option><option value="upstream">Runtime API, after translation</option></select></label>}
      <div className="recipe-actions"><button type="button" className="secondary-button" disabled={disabled} onClick={() => {if (onSelect(newAPI, newStage)) setAdding(false);}}>{existing ? "Open configuration" : "Add configuration"}</button><button type="button" className="text-button" onClick={() => setAdding(false)}>Cancel adding API</button></div>
    </div>}
  </section>;
}
