// SPDX-FileCopyrightText: 2026 Scitrera LLC
// SPDX-FileCopyrightText: 2026 Fox Engine Ltd.
// SPDX-License-Identifier: AGPL-3.0-only
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { ChatTestWorkspace } from "./ChatTestWorkspace";
import { streamChatTest } from "./api";

afterEach(() => { cleanup(); vi.unstubAllGlobals(); });
const models = [{id:"coding:quick",operations:["chat_completions","responses"]},{id:"coding:think",operations:["chat_completions","responses"]}];
function stream(text: string) {
 const encoded = new TextEncoder().encode(text);
 return new Response(new ReadableStream({start(controller) {controller.enqueue(encoded.slice(0,17));controller.enqueue(encoded.slice(17));controller.close();}}),{headers:{"Content-Type":"text/event-stream"}});
}
it("keeps conversation, reasoning and draft across settings changes and sends history to the new target", async () => {
 let turn=0;
 const fetch = vi.fn(async (path:string, _options?:RequestInit) => path.endsWith("/models") ? Response.json({models}) : stream(`data: {"choices":[{"delta":{"reasoning_content":"Consider this ${++turn}"}}]}\r\n\r\ndata: {"choices":[{"delta":{"content":"Answer ${turn}"}}]}\n\ndata: {"choices":[],"usage":{"completion_tokens":23}}\n\ndata: [DONE]\n\n`));
 vi.stubGlobal("fetch",fetch);
 const {unmount}=render(<ChatTestWorkspace token="admin-token"/>);
 await waitFor(()=>expect(screen.getByRole("combobox",{name:"Model or profile"})).toHaveValue("coding:quick"));
 fireEvent.change(screen.getByLabelText("Message"),{target:{value:"What is 17 * 19?"}});
 fireEvent.click(screen.getByRole("button",{name:"Send"}));
 await screen.findByText("Answer 1");
 await waitFor(()=>expect(screen.getByRole("status")).toHaveTextContent("23 output tokens"));
 fireEvent.change(screen.getByLabelText("Message"),{target:{value:"Explain that answer"}});
 fireEvent.focus(screen.getByRole("combobox",{name:"Model or profile"}));
 fireEvent.click(screen.getByRole("option",{name:"coding:think"}));
 fireEvent.change(screen.getByLabelText("Chat request API"),{target:{value:"responses"}});
 fireEvent.change(screen.getByLabelText("Output token limit"),{target:{value:"512"}});
 fireEvent.click(screen.getByRole("button",{name:"Refresh models"}));
 await waitFor(()=>expect(screen.getByRole("button",{name:"Send"})).toBeEnabled());
 expect(screen.getByText("Answer 1")).toBeInTheDocument();
 expect(screen.getByText("Consider this 1")).toBeInTheDocument();
 expect(screen.getByLabelText("Message")).toHaveValue("Explain that answer");
 fireEvent.click(screen.getByRole("button",{name:"Send"}));
 await screen.findByText("Answer 2");
 await waitFor(()=>expect(screen.getByRole("button",{name:"Clear conversation"})).toBeEnabled());
 const calls=fetch.mock.calls.filter(([path])=>path==="/v1/chat-test");
 expect(JSON.parse(String(calls[1]![1]!.body))).toMatchObject({model:"coding:think",operation:"responses",max_tokens:512,messages:[{role:"user",content:"What is 17 * 19?"},{role:"assistant",content:"Answer 1"},{role:"user",content:"Explain that answer"}]});
 expect(screen.getByText("Consider this 1")).toBeInTheDocument();
 expect(screen.getByText("coding:quick",{selector:"header"})).toBeInTheDocument();
 expect(screen.getByText("coding:think",{selector:"header"})).toBeInTheDocument();
 fireEvent.click(screen.getByRole("button",{name:"Clear conversation"}));
 expect(screen.queryByText("Answer 1")).toBeNull();
 expect(screen.getByText("Send a message to begin.")).toBeInTheDocument();
 fireEvent.change(screen.getByLabelText("Message"),{target:{value:"A new conversation"}});
 fireEvent.click(screen.getByRole("button",{name:"Send"}));
 await screen.findByText("Answer 3");
 unmount();render(<ChatTestWorkspace token="admin-token"/>);
 expect(screen.queryByText("A new conversation")).toBeNull();
});

it("filters model names by contains and supports keyboard selection and cancel", async () => {
 vi.stubGlobal("fetch", vi.fn(async()=>Response.json({models})));
 render(<ChatTestWorkspace token=""/>);
 const picker=screen.getByRole("combobox",{name:"Model or profile"});
 await waitFor(()=>expect(picker).toHaveValue("coding:quick"));
 fireEvent.focus(picker);
 expect(screen.getAllByRole("option").filter(option=>option.closest('[role="listbox"]'))).toHaveLength(2);
 fireEvent.change(picker,{target:{value:"ING:TH"}});
 expect(screen.getByRole("option",{name:"coding:think"})).toBeInTheDocument();
 expect(screen.queryByRole("option",{name:"coding:quick"})).toBeNull();
 fireEvent.keyDown(picker,{key:"Enter"});
 expect(picker).toHaveValue("coding:think");
 expect(picker).toHaveAttribute("aria-expanded","false");
 fireEvent.keyDown(picker,{key:"ArrowDown"});
 fireEvent.change(picker,{target:{value:"missing"}});
 expect(screen.getByText("No matching models or profiles.")).toBeInTheDocument();
 fireEvent.keyDown(picker,{key:"Enter"});
 expect(picker).toHaveValue("missing");
 fireEvent.keyDown(picker,{key:"Escape"});
 expect(picker).toHaveValue("coding:think");
 fireEvent.click(screen.getByRole("button",{name:"Show models and profiles"}));
 fireEvent.keyDown(picker,{key:"ArrowDown"});
 expect(picker.getAttribute("aria-activedescendant")).toBe(screen.getByRole("option",{name:"coding:think"}).id);
 fireEvent.click(screen.getByRole("option",{name:"coding:quick"}));
 expect(picker).toHaveValue("coding:quick");
});
it("parses Responses events and reports truncated streams", async () => {
 vi.stubGlobal("fetch",vi.fn(async()=>stream('event: response.output_text.delta\ndata: {"type":"response.output_text.delta","delta":"Hello"}\n\nevent: response.completed\ndata: {"type":"response.completed","response":{"usage":{"output_tokens":1}}}\n\n')));
 const chunks:unknown[]=[];
 await streamChatTest("",{model:"coding",operation:"responses",messages:[],max_tokens:10},chunk=>chunks.push(chunk),new AbortController().signal);
 expect(chunks).toContainEqual({text:"Hello",reasoning:undefined,usage:undefined});
 vi.stubGlobal("fetch",vi.fn(async()=>stream('data: {"choices":[{"delta":{"content":"partial"}}]}\n\n')));
 await expect(streamChatTest("",{model:"coding",operation:"chat_completions",messages:[],max_tokens:10},()=>{},new AbortController().signal)).rejects.toThrow("before completion");
});
it("cancels an in-flight generation with Stop", async () => {
 let inferenceSignal:AbortSignal|undefined;
 vi.stubGlobal("fetch",vi.fn(async(path:string,options?:RequestInit)=>{
  if(path.endsWith("/models")) return Response.json({models});
  inferenceSignal=options?.signal as AbortSignal;
  return await new Promise<Response>((_resolve,reject)=>inferenceSignal!.addEventListener("abort",()=>reject(new DOMException("Stopped","AbortError"))));
 }));
 render(<ChatTestWorkspace token=""/>);
 await waitFor(()=>expect(screen.getByRole("combobox",{name:"Model or profile"})).toHaveValue("coding:quick"));
 fireEvent.change(screen.getByLabelText("Message"),{target:{value:"hello"}});fireEvent.click(screen.getByRole("button",{name:"Send"}));
 fireEvent.click(await screen.findByRole("button",{name:"Stop"}));
 await waitFor(()=>expect(inferenceSignal?.aborted).toBe(true));
 expect(await screen.findByRole("alert")).toHaveTextContent("Generation stopped");
});
