// SPDX-FileCopyrightText: 2026 Scitrera LLC
// SPDX-FileCopyrightText: 2026 Fox Engine Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

import { useEffect, useRef, useState, type FormEvent } from "react";
import { ChatModelPicker } from "./ChatModelPicker";
import { fetchChatTestModels, streamChatTest, type ChatTestModel } from "./api";

type Message = { role: "user" | "assistant"; content: string; reasoning?: string; model?: string };

export function ChatTestWorkspace({ token }: { token: string }) {
  const [models, setModels] = useState<ChatTestModel[]>([]);
  const [model, setModel] = useState("");
  const [operation, setOperation] = useState("chat_completions");
  const [prompt, setPrompt] = useState("");
  const [system, setSystem] = useState("");
  const [limit, setLimit] = useState(1024);
  const [messages, setMessages] = useState<Message[]>([]);
  const [busy, setBusy] = useState(false);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [stats, setStats] = useState("");
  const [refresh, setRefresh] = useState(0);
  const controller = useRef<AbortController | null>(null);
  const transcript = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const abort = new AbortController();
    setLoading(true);
    void fetchChatTestModels(token, abort.signal).then(result => {
      setModels(result.models);
      setModel(current => result.models.some(item => item.id === current) ? current : result.models[0]?.id ?? "");
      setError("");
    }).catch((cause: unknown) => { if (!abort.signal.aborted) setError(String(cause)); })
      .finally(() => { if (!abort.signal.aborted) setLoading(false); });
    return () => abort.abort();
  }, [token, refresh]);
  useEffect(() => () => controller.current?.abort(), []);
  useEffect(() => { transcript.current?.scrollTo?.({ top: transcript.current.scrollHeight }); }, [messages]);
  const operations = models.find(item => item.id === model)?.operations ?? [];
  useEffect(() => {
    if (operations.length && !operations.includes(operation)) setOperation(operations[0]!);
  }, [model, models, operation]);

  function clear() { setPrompt(""); setMessages([]); setStats(""); setError(""); }
  async function send(event: FormEvent) {
    event.preventDefault();
    if (busy || !prompt.trim() || !model) return;
    const history = [...messages, { role: "user" as const, content: prompt.trim() }];
    const requestMessages = history.filter(message => message.content).map(({ role, content }) => ({ role, content }));
    const abort = new AbortController();
    controller.current = abort;
    const started = performance.now();
    let firstToken: number | undefined;
    let content = "", reasoning = "", usage: Record<string, number> = {};
    setMessages([...history, { role: "assistant", content: "", model }]);
    setPrompt(""); setStats(""); setError(""); setBusy(true);
    try {
      await streamChatTest(token, {
        model, operation, max_tokens: limit,
        messages: [...(system.trim() ? [{ role: "system", content: system.trim() }] : []), ...requestMessages],
      }, chunk => {
        if ((chunk.text || chunk.reasoning) && firstToken === undefined) firstToken = performance.now() - started;
        content += chunk.text ?? ""; reasoning += chunk.reasoning ?? "";
        if (chunk.usage) usage = chunk.usage;
        setMessages([...history, { role: "assistant", content, reasoning, model }]);
      }, abort.signal);
      if (!content && !reasoning) setError("The model returned no text. Try a larger output limit or another API.");
    } catch (cause) {
      setError(abort.signal.aborted ? "Generation stopped. Partial output is retained until you clear the conversation." : cause instanceof Error ? cause.message : String(cause));
    } finally {
      const output = usage.output_tokens ?? usage.completion_tokens;
      setStats([`${((performance.now() - started) / 1000).toFixed(1)}s total`, firstToken === undefined ? "" : `${(firstToken / 1000).toFixed(1)}s to first token`, output === undefined ? "" : `${output} output tokens`].filter(Boolean).join(" · "));
      setBusy(false); controller.current = null;
    }
  }

  return <section className="chat-test" aria-label="Chat test">
    <div className="chat-test-heading"><div><h2>Try a model or profile</h2>
      <p>Conversation stays in this page and clears when you leave or reload. Test payloads are excluded from SparkRoute saved traces; normal usage metrics and upstream provider policies still apply.</p>
    </div><button className="secondary-button" type="button" disabled={busy} onClick={() => setRefresh(value => value + 1)}>Refresh models</button></div>
    <div className="chat-test-settings">
      <ChatModelPicker models={models} value={model} disabled={busy || loading} loading={loading} onChange={setModel} />
      <label>Request API<select aria-label="Chat request API" value={operation} disabled={busy} onChange={event => setOperation(event.target.value)}>
        <option value="chat_completions">Chat Completions</option><option value="responses">Responses</option>
      </select></label>
      <label>Output token limit<input aria-label="Output token limit" type="number" min={1} max={8192} value={limit} disabled={busy} onChange={event => setLimit(Number(event.target.value))}/></label>
    </div>
    {model && !operations.includes(operation) && <p className="notice">This profile does not declare support for the selected API. Choose a supported API or edit its recipe.</p>}
    <details className="chat-test-system"><summary>System message (optional)</summary><textarea aria-label="System message" rows={3} value={system} disabled={busy || messages.length > 0} onChange={event => setSystem(event.target.value)} placeholder="Instructions for this conversation"/><small>Clear the conversation to change its system message.</small></details>
    <div className="chat-test-transcript" ref={transcript} role="log" aria-label="Conversation" aria-live="polite">
      {!messages.length && <div className="chat-test-empty"><h3>No messages yet</h3><p>Send a message to begin.</p></div>}
      {messages.map((message, index) => <article className={`chat-test-message ${message.role}`} key={index}>
        <header>{message.role === "user" ? "You" : message.model ?? "Assistant"}</header>
        {message.reasoning && <details open={busy && index === messages.length - 1}><summary>Reasoning</summary><pre>{message.reasoning}</pre></details>}
        <pre>{message.content || (busy && index === messages.length - 1 ? "Waiting for the model…" : "No answer text returned.")}</pre>
      </article>)}
    </div>
    <form className="chat-test-composer" onSubmit={event => void send(event)}>
      {error && <p className="notice error" role="alert">{error}</p>}
      <label htmlFor="chat-prompt">Message</label>
      <textarea id="chat-prompt" rows={3} value={prompt} disabled={busy} placeholder="Ask a question to test this model…" onChange={event => setPrompt(event.target.value)} onKeyDown={event => { if (event.key === "Enter" && (event.ctrlKey || event.metaKey)) { event.preventDefault(); event.currentTarget.form?.requestSubmit(); } }}/>
      <div className="chat-test-actions"><span role="status">{busy ? "Generating…" : stats || "Ctrl/⌘ + Enter to send"}</span>
        <button className="secondary-button" type="button" disabled={busy || !messages.length} onClick={clear}>Clear conversation</button>
        {busy ? <button type="button" onClick={() => controller.current?.abort()}>Stop</button> : <button type="submit" disabled={!model || !prompt.trim() || loading || !operations.includes(operation)}>Send</button>}
      </div>
    </form>
  </section>;
}
