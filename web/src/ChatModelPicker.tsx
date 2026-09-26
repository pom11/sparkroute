// SPDX-FileCopyrightText: 2026 Scitrera LLC
// SPDX-FileCopyrightText: 2026 Fox Engine Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

import { useEffect, useId, useRef, useState } from "react";
import type { ChatTestModel } from "./api";

export function ChatModelPicker({ models, value, disabled, loading, onChange }: {
  models: ChatTestModel[]; value: string; disabled: boolean; loading: boolean; onChange: (value: string) => void;
}) {
  const id = useId();
  const input = useRef<HTMLInputElement>(null);
  const list = useRef<HTMLUListElement>(null);
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [active, setActive] = useState(0);
  const matches = models.filter(model => model.id.toLowerCase().includes(query.toLowerCase()));
  const activeIndex = Math.min(active, matches.length - 1);

  useEffect(() => {
    if (open) list.current?.children[activeIndex]?.scrollIntoView?.({ block: "nearest" });
  }, [open, activeIndex, query]);

  function show() { setQuery(""); setActive(0); setOpen(true); }
  function choose(id: string) { onChange(id); setOpen(false); setQuery(""); }

  return <div className="chat-model-picker" onBlur={event => {
    if (!event.currentTarget.contains(event.relatedTarget)) setOpen(false);
  }}>
    <label htmlFor={id}>Model or profile</label>
    <div className="chat-model-input">
      <input id={id} ref={input} role="combobox" aria-autocomplete="list" aria-expanded={open}
        aria-controls={`${id}-options`} aria-activedescendant={open && activeIndex >= 0 ? `${id}-option-${activeIndex}` : undefined}
        autoComplete="off" spellCheck={false} disabled={disabled} value={open ? query : value}
        placeholder={loading ? "Loading models…" : !models.length ? "No models configured" : value || "Find a model or profile…"}
        onFocus={show} onClick={() => { if (!open) show(); }}
        onChange={event => { setQuery(event.target.value); setActive(0); setOpen(true); }}
        onKeyDown={event => {
          if (event.key === "ArrowDown" || event.key === "ArrowUp") {
            event.preventDefault();
            if (!open) { show(); return; }
            setActive(index => Math.max(0, Math.min(matches.length - 1, index + (event.key === "ArrowDown" ? 1 : -1))));
          } else if (event.key === "Enter" && open) {
            event.preventDefault();
            if (matches[activeIndex]) choose(matches[activeIndex]!.id);
          } else if (event.key === "Escape") {
            event.preventDefault(); setOpen(false);
          } else if (event.key === "Tab") setOpen(false);
        }} />
      <button type="button" className="chat-model-toggle" aria-label="Show models and profiles" disabled={disabled}
        aria-expanded={open} aria-controls={`${id}-options`} onMouseDown={event => event.preventDefault()}
        onClick={() => { if (open) setOpen(false); else { input.current?.focus(); show(); } }}>
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" aria-hidden="true"><path d="m6 9 6 6 6-6" /></svg>
      </button>
    </div>
    {open && <div className="chat-model-menu">
      <ul id={`${id}-options`} ref={list} role="listbox" aria-label="Models and profiles">
        {matches.map((model, index) => <li id={`${id}-option-${index}`} key={model.id} role="option"
          aria-selected={model.id === value} className={index === activeIndex ? "active" : ""}
          onMouseDown={event => event.preventDefault()} onMouseMove={() => setActive(index)} onClick={() => choose(model.id)}>
          {model.id}{model.id === value && <span aria-hidden="true">✓</span>}
        </li>)}
      </ul>
      {!matches.length && <p role="status">No matching models or profiles.</p>}
    </div>}
  </div>;
}
