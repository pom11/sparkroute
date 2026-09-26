// SPDX-FileCopyrightText: 2026 Scitrera LLC
// SPDX-FileCopyrightText: 2026 Fox Engine Ltd.
// SPDX-License-Identifier: AGPL-3.0-only
import type { JSONObject } from "./extensions";

export const profileAPIs: Record<string, string> = {
  chat_completions: "Chat Completions", responses: "Responses", messages: "Anthropic Messages",
  responses_compact: "Responses Compact", messages_count_tokens: "Anthropic token counting",
  generate_content: "Gemini generate content", stream_generate_content: "Gemini streaming", count_tokens: "Gemini token counting",
  converse: "Bedrock Converse", converse_stream: "Bedrock streaming", embeddings: "OpenAI embeddings",
  embed_content: "Gemini embeddings", batch_embed_contents: "Gemini batch embeddings",
};
export type ProfileStage = "ingress" | "upstream";
export type APIConfiguration = { operation: string; stage: ProfileStage; origins: string[] };
export const profileObject = (value: unknown): JSONObject => value && typeof value === "object" && !Array.isArray(value) ? value as JSONObject : {};
const strings = (value: unknown) => Array.isArray(value) ? value.filter((item): item is string => typeof item === "string") : [];
export const configurationKey = (entry: Pick<APIConfiguration, "stage" | "operation">) => `${entry.stage}:${entry.operation}`;
export const stageLabel = (stage: ProfileStage) => stage === "ingress" ? "Incoming API" : "Runtime API";
export const apiLabel = (operation: string) => profileAPIs[operation] ?? operation;
export function recipeIncomingAPIs(implementation: JSONObject): string[] {
  const declared = strings(implementation.supported_operations);
  // An empty list follows the backend's inference from ingress keys.
  return declared.length ? declared : Object.keys(ingressParameters(implementation));
}
function ingressParameters(implementation: JSONObject): JSONObject {
  return profileObject(implementation.ingress_overrides ?? ("upstream_overrides" in implementation || "supported_operations" in implementation ? {} : implementation));
}

export function profileConfigurations(binding: JSONObject, parent: JSONObject, profile: JSONObject, deployments: JSONObject[]): APIConfiguration[] {
  const entries = new Map<string, APIConfiguration>();
  const add = (stage: ProfileStage, operation: string, origin: string) => {
    const key = configurationKey({stage, operation}), entry = entries.get(key) ?? {stage, operation, origins: []};
    if (!entry.origins.includes(origin)) entry.origins.push(origin);
    entries.set(key, entry);
  };
  const parameters = (stage: ProfileStage, values: unknown, origin: string) => Object.keys(profileObject(values)).forEach(api => add(stage, api, origin));
  if (!binding.manual) for (const deployment of deployments) {
    const impl = profileObject(profileObject(deployment.request_profiles)[String(binding.selector)]);
    parameters("ingress", ingressParameters(impl), "Recipe");
    parameters("upstream", impl.upstream_overrides, "Recipe");
    recipeIncomingAPIs(impl).forEach(api => add("ingress", api, "Recipe"));
  }
  parameters("ingress", parent.request_overrides, "Parent overrides");
  parameters("ingress", profile.request_overrides, "Profile overrides");
  for (const [patch, origin] of [[binding.patch, "All-deployment overrides"], ...Object.values(profileObject(binding.deployment_overrides)).map(patch => [patch, "Deployment overrides"])] as [unknown, string][]) {
    const value = profileObject(patch);
    for (const stage of ["ingress", "upstream"] as const) {
      parameters(stage, value[`${stage}_overrides`], origin);
      parameters(stage, value[`remove_${stage}`], "Suppressed fields");
    }
  }
  return [...entries.values()].sort((a,b) => a.stage.localeCompare(b.stage) || Object.keys(profileAPIs).indexOf(a.operation) - Object.keys(profileAPIs).indexOf(b.operation));
}

const protocolForAPI = (api: string) => {
  if (["chat_completions", "responses", "responses_compact", "embeddings"].includes(api)) return "openai";
  if (["messages", "messages_count_tokens"].includes(api)) return "anthropic";
  if (["converse", "converse_stream"].includes(api)) return "bedrock";
  if (["generate_content", "stream_generate_content", "count_tokens", "embed_content", "batch_embed_contents"].includes(api)) return "gemini";
  return "";
};
const providerProtocol: Record<string,string> = {sparkrun:"openai",openai:"openai",openai_compatible:"openai",openai_responses:"openai",openai_subscription:"openai",anthropic:"anthropic",gemini:"gemini",bedrock:"bedrock"};
// Advisory protocol checks only; payload capabilities and actual health remain server-owned.
function upstreamAPIs(deployment: JSONObject, provider: JSONObject, incoming: string): string[] | undefined {
  const type = String(provider.type ?? ""), declared = strings(deployment.native_protocols);
  const protocols = declared.length ? declared : providerProtocol[type] ? [providerProtocol[type]!] : [];
  if (!protocols.length || !protocolForAPI(incoming)) return undefined;
  if (type === "openai_responses" && incoming === "chat_completions" || type === "openai_subscription" && incoming !== "responses") return [];
  return protocols.flatMap(protocol => {
    if (protocol === protocolForAPI(incoming)) {
      if (incoming === "responses" && !strings(deployment.capabilities).includes("responses") && !["openai_responses", "openai_subscription"].includes(type)) return ["chat_completions"];
      return [incoming];
    }
    if (incoming === "chat_completions") return ({anthropic:["messages"],gemini:["generate_content","stream_generate_content"],bedrock:["converse","converse_stream"]} as Record<string,string[]>)[protocol] ?? [];
    if (incoming === "embeddings" && protocol === "gemini") return ["embed_content","batch_embed_contents"];
    return [];
  });
}

export function profileAPIWarnings(binding: JSONObject, deployments: JSONObject[], providers: JSONObject[], operation: string, stage: ProfileStage): string[] {
  const result: string[] = [];
  for (const deployment of deployments) {
    if (strings(binding.excluded_deployments).includes(String(deployment.name))) continue;
    const title = String(deployment.title ?? deployment.name), impl = profileObject(profileObject(deployment.request_profiles)[String(binding.selector)]);
    const incoming = binding.manual ? Object.keys(profileAPIs) : recipeIncomingAPIs(impl);
    if (!binding.manual && !Object.keys(impl).length) { result.push(`${title}: this deployment has no recipe implementation for this profile.`); continue; }
    if (stage === "ingress" && !incoming.includes(operation)) {
      result.push(`${title}: the recipe does not support incoming ${apiLabel(operation)}. Add it to the recipe's supported_operations (or ingress_overrides when that list is omitted). Operator overrides do not enable it.`);
      continue;
    }
    const provider = providers.find(value => value.name === deployment.provider) ?? {};
    if (stage === "ingress") {
      const available = upstreamAPIs(deployment, provider, operation);
      if (available?.length === 0) result.push(`${title}: no declared native API or supported translation accepts incoming ${apiLabel(operation)}. Review the deployment's native APIs.`);
    } else {
      const paths = incoming.map(api => upstreamAPIs(deployment, provider, api));
      if (paths.every(path => path !== undefined) && !paths.some(path => path?.includes(operation))) result.push(`${title}: none of this profile's supported incoming APIs can reach runtime ${apiLabel(operation)} with the current deployment declarations.`);
    }
  }
  return result;
}
