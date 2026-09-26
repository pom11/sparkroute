// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Scitrera LLC
// Copyright 2026 Fox Engine Ltd.
import { expect, it } from "vitest";
import { visibleSparkrunDocument } from "./sparkrunExclusions";

it("keeps legacy aggregate exclusions effective after per-job profile splitting", () => {
  const source = {type: "discovered", controller: "sparkrun"};
  const generated = {
    deployments: [
      {name: "aggregate", provider: "sparkrun", model: "coding", endpoint_source: source},
      {name: "job", provider: "sparkrun", model: "coding", endpoint_source: source, discovery_job_ids: ["job-a"]},
      {name: "other", provider: "sparkrun", model: "other", endpoint_source: source},
    ],
    virtual_models: [
      {name: "coding", pools: [{targets: [{deployment: "job", weight: 1}]}]},
      {name: "coding:low", profile: {parent: "coding", selector: "low"}},
    ],
  };
  const result = visibleSparkrunDocument({sparkrun_overrides:{excluded_deployments:["aggregate"]}}, generated)!;
  expect(result.deployments).toEqual([generated.deployments[2]]);
  expect(result.virtual_models).toEqual([]);
  expect(generated.deployments).toHaveLength(3);
});
