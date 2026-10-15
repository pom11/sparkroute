// SPDX-FileCopyrightText: 2026 Scitrera LLC
// SPDX-FileCopyrightText: 2026 Fox Engine Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

import {cleanup, fireEvent, render, screen, waitFor} from "@testing-library/react";
import {useState} from "react";
import {afterEach, expect, it} from "vitest";
import {VirtualModelEditor} from "./VirtualModelEditor";
import {deploymentInventory, routingLabel} from "./deploymentInventory";
import type {ConfigurationDocument, GatewayStatus} from "./types";

afterEach(() => {cleanup(); window.history.replaceState({}, "", "/admin/");});

it("opens the conflicting route and lets the user select an unrouted generated deployment", async () => {
  window.history.replaceState({}, "", "/admin/configuration/models?model=saved-route");
  const model = (name: string) => ({name, pools: [{targets: [{deployment: "old", weight: 1}]}]});
  function Editor() {
    const [document, setDocument] = useState<ConfigurationDocument>({deployments: [{name: "old"}], virtual_models: [model("first"), model("saved-route")]});
    return <><VirtualModelEditor document={document} disabled={false} onChange={setDocument} readOnlyDocument={{deployments: [{name: "new", title: "DeepSeek on sparks25"}], virtual_models: []}} /><output aria-label="Saved draft">{JSON.stringify(document)}</output></>;
  }
  render(<Editor />);
  await waitFor(() => expect(screen.getByLabelText("Canonical name")).toHaveValue("saved-route"));
  expect(screen.getByRole("option", {name: "DeepSeek on sparks25"})).toBeEnabled();
  fireEvent.change(screen.getByRole("combobox", {name: "Deployment"}), {target: {value: "new"}});
  const draft = JSON.parse(screen.getByLabelText("Saved draft").textContent!);
  expect(draft.virtual_models[1].pools[0].targets[0].deployment).toBe("new");
  expect(draft.virtual_models[0].pools[0].targets[0].deployment).toBe("old");
});

it("shows a retained discovered target as unavailable when it has no ready endpoint", () => {
  const status = {targets: [{deployment: "new", endpoint_source: "discovered", routing_state: "routed", model_names: ["saved-route"], admission_available: true}]} as GatewayStatus;
  const [entry] = deploymentInventory(status, undefined, []);
  expect(entry).toBeDefined();
  expect(routingLabel(entry!)).toBe("Unavailable");
});
