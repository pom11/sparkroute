// SPDX-FileCopyrightText: 2026 Scitrera LLC
// SPDX-FileCopyrightText: 2026 Fox Engine Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { useState } from "react";
import { afterEach, beforeEach, expect, it } from "vitest";
import { VirtualModelEditor } from "./VirtualModelEditor";
import type { ConfigurationDocument } from "./types";

beforeEach(() => {
  Object.defineProperty(HTMLDialogElement.prototype, "showModal", { configurable: true, value: function(this: HTMLDialogElement) { this.setAttribute("open", ""); } });
  Object.defineProperty(HTMLDialogElement.prototype, "close", { configurable: true, value: function(this: HTMLDialogElement) { this.removeAttribute("open"); } });
});
afterEach(() => { cleanup(); Reflect.deleteProperty(HTMLDialogElement.prototype, "showModal"); Reflect.deleteProperty(HTMLDialogElement.prototype, "close"); });
const base = { name: "coding", aliases: ["code"], pools: [{ priority: 0, targets: [{ deployment: "one-job", weight: 1 }] }] };
const low = { ...base, name: "coding:low", aliases: ["quick-code"], request_overrides: { chat_completions: { reasoning_effort: "low" }, responses: { reasoning: { effort: "low" } } } };
function Editor({ generated = false, profiles = false, disabled = false, baseOverrides = false } = {}) {
  const [document, setDocument] = useState<ConfigurationDocument>({ deployments: [{ name: "one-job" }], virtual_models: [...(generated ? [] : [baseOverrides ? { ...base, request_overrides: { chat_completions: { temperature: 0.3 } } } : base]), ...(profiles ? [low] : [])] });
  return <><VirtualModelEditor document={document} disabled={disabled} onChange={setDocument} readOnlyDocument={generated ? { deployments: [], virtual_models: [base] } : undefined} /><output aria-label="Draft">{JSON.stringify(document)}</output></>;
}
const draft = () => JSON.parse(screen.getByLabelText("Draft").textContent!);
const click = (name: string) => fireEvent.click(screen.getByRole("button", { name }));
const parameters = (json: string) => fireEvent.change(screen.getByLabelText("Request parameters (JSON)"), { target: { value: json } });

it("adds a named profile into the table after general fields and before routing pools", () => {
  render(<Editor />);
  const profiles = screen.getByRole("region", { name: "Request profiles" });
  expect(screen.getByLabelText(/^Aliases/).compareDocumentPosition(profiles) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  expect(profiles.compareDocumentPosition(screen.getByRole("heading", { name: "Routing pools" })) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  click("Add profile");
  fireEvent.change(screen.getByLabelText("Profile selector"), { target: { value: "xhigh" } });
  parameters('{"reasoning_effort":"xhigh"}'); click("Add profile to draft");
  expect(draft().virtual_models[1]).toEqual({name: "coding:xhigh", profile: {parent: "coding", selector: "xhigh", manual: true, patch: {ingress_overrides: {chat_completions: {reasoning_effort: "xhigh"}}}}});
  expect(screen.getByLabelText("Canonical name")).toHaveValue("coding");
  expect(within(screen.getByRole("complementary", { name: "Virtual models" })).queryByText("coding:xhigh")).not.toBeInTheDocument();
  const row = screen.getByRole("button", { name: "Edit profile coding:xhigh" }).closest("tr")!;
  expect(within(row).getByRole("rowheader")).toHaveTextContent("xhigh");
  expect(row.querySelector(".profile-summary")?.textContent).toContain("Incoming: Chat Completions");
  expect(screen.queryByLabelText("Request parameters (JSON)")).not.toBeInTheDocument();
});

it("opens a modal outside the table, preserves other API parameters and aliases, and supports cancel and delete", () => {
  render(<Editor profiles />);
  click("Edit profile coding:low");
  const row = screen.getByRole("button", { name: "Edit profile coding:low" }).closest("tr")!;
  const dialog = screen.getByRole("dialog", {name: "Edit profile coding:low"});
  expect(dialog).toContainElement(screen.getByLabelText("Request parameters (JSON)"));
  expect(row.closest("table")).not.toContainElement(dialog);
  parameters('{"reasoning_effort":"high"}'); click("Cancel");
  expect(draft().virtual_models[1]).toEqual(low);
  click("Edit profile coding:low");
  fireEvent.change(screen.getByLabelText("Request API"), { target: { value: "responses" } });
  parameters('{"reasoning":{"effort":"medium"}}'); click("Apply to draft");
  expect(draft().virtual_models[1]).toEqual({ ...low, request_overrides: { ...low.request_overrides, responses: { reasoning: { effort: "medium" } } } });
  click("Edit profile coding:low"); click("Delete profile coding:low");
  expect(draft().virtual_models).toEqual([base]);
  expect(screen.queryByRole("button", { name: "Delete profile coding:low" })).not.toBeInTheDocument();
});

it("rejects a conflicting selector and invalid JSON without losing the editor draft", () => {
  render(<Editor profiles />); click("Add profile"); click("Add profile to draft");
  expect(screen.getByRole("alert")).toHaveTextContent("unique profile selector");
  fireEvent.change(screen.getByLabelText("Profile selector"), { target: { value: "high" } });
  parameters("[]"); click("Add profile to draft");
  expect(screen.getByRole("alert")).toHaveTextContent("JSON object");
  expect(screen.getByLabelText("Profile selector")).toHaveValue("high");
  expect(draft().virtual_models).toEqual([base, low]);
});

it("can create an operator profile from a generated model without making the generated fields editable", () => {
  render(<Editor generated />);
  expect(screen.getByLabelText("Canonical name")).toBeDisabled();
  click("Add profile");
  fireEvent.change(screen.getByLabelText("Request API"), { target: { value: "responses" } });
  parameters('{"reasoning":{"effort":"high"}}'); click("Add profile to draft");
  expect(draft().virtual_models).toEqual([{name: "coding:low", profile: {parent: "coding", selector: "low", manual: true, patch: {ingress_overrides: {responses: {reasoning: {effort: "high"}}}}}}]);
});

it("disables every profile mutation for readers", () => {
  render(<Editor profiles disabled />);
  for (const name of ["Add profile", "Edit profile coding:low"]) expect(screen.getByRole("button", { name })).toBeDisabled();
});

it("keeps a base model when deleting its default parameter override", () => {
  render(<Editor baseOverrides />);
  expect(screen.getByRole("rowheader", { name: "(default)" })).toBeInTheDocument();
  click("Edit profile coding"); click("Delete parameters for coding");
  expect(draft().virtual_models).toEqual([base]);
});

it("preserves external routing edits while the profile modal is open", () => {
  render(<Editor baseOverrides />);
  click("Edit profile coding");
  fireEvent.change(screen.getByLabelText("Relative weight"), { target: { value: "42" } });
  parameters('{"reasoning_effort":"medium"}'); click("Apply to draft");
  expect(draft().virtual_models[0].pools[0].targets[0].weight).toBe(42);
});

it("shows only the generated parent when operator variants precede it in the document", () => {
  render(<Editor generated profiles />);
  const list = screen.getByRole("complementary", { name: "Virtual models" });
  expect(within(list).queryByText("coding:low")).not.toBeInTheDocument();
  expect(within(list).getByText("coding")).toBeInTheDocument();
  expect(screen.getByLabelText("Canonical name")).toHaveValue("coding");
  expect(screen.getByLabelText("Canonical name")).toBeDisabled();
  click("Edit profile coding:low");
  parameters('{"reasoning_effort":"medium"}'); click("Apply to draft");
  expect(draft().virtual_models[0].request_overrides.chat_completions).toEqual({ reasoning_effort: "medium" });
});

it("keeps standalone profiles accessible when their parent is absent", () => {
  render(<VirtualModelEditor document={{ deployments: [{ name: "one-job" }], virtual_models: [low] }} disabled={false} onChange={() => {}} />);
  expect(within(screen.getByRole("complementary", { name: "Virtual models" })).getByText("coding:low")).toBeInTheDocument();
  expect(screen.getByLabelText("Canonical name")).toHaveValue("coding:low");
});

it("edits a deployment-specific recipe override and resets it without copying inherited values", () => {
 function RecipeEditor() {
  const [document,setDocument]=useState<ConfigurationDocument>({deployments:[{name:"one-job",request_profiles:{low:{chat_completions:{temperature:0.2,top_p:0.9}}}}],virtual_models:[base,{name:"coding:low",profile:{parent:"coding",selector:"low"}}]});
  return <><VirtualModelEditor document={document} disabled={false} onChange={setDocument}/><output aria-label="Draft">{JSON.stringify(document)}</output></>;
 }
 render(<RecipeEditor/>);click("Edit profile coding:low");
 expect(screen.getByLabelText("Effective parameters one-job")).toHaveTextContent('"temperature": 0.2');
 fireEvent.change(screen.getByLabelText("Override scope"),{target:{value:"one-job"}});
 parameters('{"temperature":0.5}');click("Apply to draft");
 const binding=draft().virtual_models[1].profile;
 expect(binding.deployment_overrides["one-job"].ingress_overrides.chat_completions).toEqual({temperature:0.5});
 expect(draft().virtual_models[1].pools).toBeUndefined();
 click("Edit profile coding:low");fireEvent.change(screen.getByLabelText("Override scope"),{target:{value:"one-job"}});
 click("Reset to inherited");click("Apply to draft");
 expect(draft().virtual_models[1].profile.deployment_overrides["one-job"].ingress_overrides.chat_completions).toBeUndefined();
 expect(draft().deployments[0].request_profiles.low.chat_completions).toEqual({temperature:0.2,top_p:0.9});
});

it("adds an available recipe profile without pinning its parameters", () => {
 function RecipeEditor(){const [document,setDocument]=useState<ConfigurationDocument>({deployments:[{name:"one-job",request_profiles:{"low.v2":{chat_completions:{temperature:0.2}}}}],virtual_models:[base]});return <><VirtualModelEditor document={document} disabled={false} onChange={setDocument}/><output aria-label="Draft">{JSON.stringify(document)}</output></>;}
 render(<RecipeEditor/>);click("Use recipe profile low.v2");
 expect(draft().virtual_models[1]).toEqual({name:"coding:low.v2",profile:{parent:"coding",selector:"low.v2"}});
});


it("keeps explicitly linked profiles attached when the parent is renamed", () => {
 function LinkedEditor() {
  const [document,setDocument]=useState<ConfigurationDocument>({deployments:[{name:"one-job"}],virtual_models:[base,{name:"custom-public-name",profile:{parent:"coding",selector:"low",manual:true}}]});
  return <><VirtualModelEditor document={document} disabled={false} onChange={setDocument}/><output aria-label="Draft">{JSON.stringify(document)}</output></>;
 }
 render(<LinkedEditor/>);
 fireEvent.change(screen.getByLabelText("Canonical name"),{target:{value:"assistant"}});
 expect(draft().virtual_models[1]).toEqual({name:"custom-public-name",profile:{parent:"assistant",selector:"low",manual:true}});
});


function MultiAPIEditor() {
 const [document,setDocument]=useState<ConfigurationDocument>({providers:[{name:"local",type:"openai"}],deployments:[{name:"one-job",provider:"local",request_profiles:{quick:{supported_operations:["chat_completions","responses"],ingress_overrides:{chat_completions:{temperature:0.2},responses:{temperature:0.2}},upstream_overrides:{chat_completions:{chat_template_kwargs:{enable_thinking:false}}}}}}],virtual_models:[base,{name:"coding:quick",profile:{parent:"coding",selector:"quick"}},{name:"coding:other",profile:{parent:"coding",selector:"other",manual:true,patch:{ingress_overrides:{responses:{temperature:0.8}}}}}]});
 return <><VirtualModelEditor document={document} disabled={false} onChange={setDocument}/><output aria-label="Draft">{JSON.stringify(document)}</output></>;
}

it("exposes multiple incoming and runtime configurations and preserves every edit when adding APIs", () => {
 render(<MultiAPIEditor/>);click("Edit profile coding:quick");
 const configurations=screen.getByRole("region",{name:"API configurations"});
 expect(within(configurations).getAllByText("Recipe")).toHaveLength(3);
 parameters('{"temperature":0.3}');
 click("Edit Incoming API: Responses");parameters('{"max_output_tokens":512}');
 click("Edit Runtime API: Chat Completions");parameters('{"chat_template_kwargs":{"enable_thinking":true}}');
 click("Add API configuration");
 fireEvent.change(screen.getByLabelText("New configuration API"),{target:{value:"messages"}});
 fireEvent.change(screen.getByLabelText("New configuration stage"),{target:{value:"ingress"}});
 click("Add configuration");parameters('{"max_tokens":768}');
 expect(screen.getByRole("status",{name:"API availability"})).toHaveTextContent("recipe does not support incoming Anthropic Messages");
 click("Edit Incoming API: Chat Completions");expect(screen.getByLabelText("Request parameters (JSON)")).toHaveValue('{\n  "temperature": 0.3\n}');
 click("Apply to draft");
 const patch=draft().virtual_models[1].profile.patch;
 expect(patch.ingress_overrides).toEqual({chat_completions:{temperature:0.3},responses:{max_output_tokens:512},messages:{max_tokens:768}});
 expect(patch.upstream_overrides).toEqual({chat_completions:{chat_template_kwargs:{enable_thinking:true}}});
 click("Edit profile coding:quick");click("Edit Incoming API: Anthropic Messages");expect(screen.getByLabelText("Request parameters (JSON)")).toHaveValue('{\n  "max_tokens": 768\n}');
 fireEvent.change(screen.getByLabelText("Profile implementation"),{target:{value:"manual"}});
 expect(screen.getByRole("status",{name:"API availability"})).toHaveTextContent("no declared native API or supported translation");
 expect(screen.getByRole("status",{name:"API availability"})).not.toHaveTextContent("recipe does not support");
 click("Edit Incoming API: Responses");expect(screen.queryByRole("status",{name:"API availability"})).toBeNull();
});

it("keeps API and scope edits when resetting only the selected entry", () => {
 render(<MultiAPIEditor/>);click("Edit profile coding:quick");parameters('{"temperature":0.3}');
 click("Edit Incoming API: Responses");parameters('{"temperature":0.4}');
 fireEvent.change(screen.getByLabelText("Override scope"),{target:{value:"one-job"}});parameters('{"top_p":0.9}');
 click("Edit Runtime API: Chat Completions");parameters('{"chat_template_kwargs":{"enable_thinking":true}}');
 click("Edit Incoming API: Responses");click("Reset to inherited");click("Apply to draft");
 const binding=draft().virtual_models[1].profile;
 expect(binding.patch.ingress_overrides).toEqual({chat_completions:{temperature:0.3},responses:{temperature:0.4}});
 expect(binding.deployment_overrides["one-job"].ingress_overrides.responses).toBeUndefined();
 expect(binding.deployment_overrides["one-job"].upstream_overrides.chat_completions.chat_template_kwargs.enable_thinking).toBe(true);
});

it("keeps invalid JSON in place when adding a new API and initializes a different profile independently", () => {
 render(<MultiAPIEditor/>);click("Edit profile coding:quick");parameters('{"temperature":');
 click("Add API configuration");fireEvent.change(screen.getByLabelText("New configuration API"),{target:{value:"messages"}});click("Add configuration");
 expect(screen.getByRole("alert")).toBeInTheDocument();expect(screen.getByLabelText("Request API")).toHaveValue("chat_completions");
 expect(screen.getByLabelText("Request parameters (JSON)")).toHaveValue('{"temperature":');
 click("Cancel adding API");click("Cancel");click("Edit profile coding:other");
 expect(screen.getByLabelText("Request API")).toHaveValue("responses");
 expect(JSON.parse((screen.getByLabelText("Request parameters (JSON)") as HTMLTextAreaElement).value)).toEqual({temperature:0.8});
});


it("cancels incomplete edits with Escape, restores focus and unlocks page scrolling", () => {
 render(<MultiAPIEditor/>);
 const original=draft();
 const trigger=screen.getByRole("button",{name:"Edit profile coding:quick"});
 trigger.focus();fireEvent.click(trigger);
 expect(screen.getByRole("heading",{name:"Edit profile"})).toHaveFocus();
 expect(document.body.style.overflow).toBe("hidden");
 parameters('{"temperature":');
 fireEvent(screen.getByRole("dialog"), new Event("cancel",{cancelable:true}));
 expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
 expect(draft()).toEqual(original);
 expect(trigger).toHaveFocus();
 expect(document.body.style.overflow).toBe("");
 fireEvent.click(trigger);
 expect(screen.getByLabelText("Request parameters (JSON)")).toHaveValue("{}");
 click("Cancel");expect(trigger).toHaveFocus();
});

it("deletes a linked profile from the modal without applying its unsaved parameters", () => {
 render(<MultiAPIEditor/>);const original=draft();
 const trigger=screen.getByRole("button",{name:"Edit profile coding:quick"});
 trigger.focus();fireEvent.click(trigger);parameters('{"temperature":0.99}');
 click("Delete profile coding:quick");
 expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
 expect(draft()).toEqual({...original,virtual_models:original.virtual_models.filter((model: {name:string})=>model.name!=="coding:quick")});
 expect(screen.getByRole("button",{name:"Add profile"})).toHaveFocus();
 click("Add profile");
 expect(screen.getByRole("dialog",{name:"Add request profile"})).toBeInTheDocument();
 expect(screen.queryByRole("button",{name:/Delete profile/})).not.toBeInTheDocument();
 click("Cancel");
});

it("previews migration in the modal and leaves the legacy profile intact on cancel", () => {
 render(<Editor profiles/>);click("Review migration");
 const dialog=screen.getByRole("dialog",{name:"Edit profile coding:low"});
 expect(dialog).toHaveTextContent("Migration preview");
 click("Cancel");expect(draft().virtual_models[1]).toEqual(low);
 click("Review migration");click("Apply to draft");
 expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
 expect(draft().virtual_models[1].profile.patch.ingress_overrides).toEqual(low.request_overrides);
 expect(draft().virtual_models[1].migration_preview).toBeUndefined();
});
