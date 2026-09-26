// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Scitrera LLC
// Copyright 2026 Fox Engine Ltd.
package gateway

import (
	"encoding/json"
	"fmt"
	"slices"

	"github.com/sparksq/sparkroute/pkg/config"
	"github.com/sparksq/sparkroute/pkg/routing"
)

type preparedProfileRequest struct {
	body                []byte
	route               routing.ProtocolRoute
	embeddingDimensions int64
	embeddingInputCount int
}

// prepareProfileRequest never changes the shared request or another candidate's
// body. Both capability detection and translation see this deployment's values.
func (h *chatCompletionsHandler) prepareProfileRequest(raw []byte, pathModel string, streaming bool, model config.VirtualModel, provider config.Provider, deployment config.Deployment) (preparedProfileRequest, error) {
	var result preparedProfileRequest
	in, _, supported := model.EffectiveProfileParameters(deployment, h.operation.String(), "")
	if !supported {
		return result, fmt.Errorf("deployment does not implement this profile for %s", h.operation)
	}
	body, err := applyRequestOverrides(raw, in)
	if err != nil {
		return result, err
	}
	if int64(len(body)) > h.maxRequestBytes {
		return result, fmt.Errorf("profile request exceeds body limit")
	}
	envelope, _, _, err := h.operation.decodeRequest(body, pathModel)
	if err != nil {
		return result, err
	}
	required, err := h.operation.detectCapabilities(envelope, streaming)
	if err != nil {
		return result, err
	}
	if provider.Type == "openai_responses" && h.operation == openAIOperationChatCompletions {
		return result, fmt.Errorf("chat to Responses translation is unavailable")
	}
	if provider.Type == "openai_subscription" && (h.operation != openAIOperationResponses || rawJSONBool(envelope["store"]) || rawJSONBool(envelope["background"]) || rawNonNull(envelope["previous_response_id"]) || rawNonNull(envelope["conversation"])) {
		return result, fmt.Errorf("profile request is incompatible with subscription target")
	}
	if h.operation == openAIOperationResponses {
		required = slices.DeleteFunc(required, func(c config.Capability) bool {
			return c == config.CapabilityResponses || provider.Type == "openai_subscription" && c == config.CapabilityStoredCompletion
		})
	}
	ingress := config.Protocol(h.operation.protocol())
	protocols := deployment.EffectiveNativeProtocols(provider)
	if slices.Contains(protocols, ingress) {
		protocols = append([]config.Protocol{ingress}, slices.DeleteFunc(slices.Clone(protocols), func(p config.Protocol) bool { return p == ingress })...)
	}
	var last error
	for _, protocol := range protocols {
		native := protocol == ingress
		upstreamOp := h.operation
		var upstream []byte
		var prepareErr error
		switch {
		case h.operation == openAIOperationResponses && protocol == config.ProtocolOpenAI && !deployment.DeclaresCapability(config.CapabilityResponses):
			native = false
			upstreamOp = openAIOperationChatCompletions
			upstream, prepareErr = translateResponsesRequestToChat(body)
			if prepareErr == nil {
				upstream, prepareErr = rewriteTranslatedOpenAIModel(upstream, deployment.Model)
			}
		case native:
			upstream, prepareErr = h.operation.rewriteRequest(envelope, deployment.Model)
		case h.operation == openAIOperationChatCompletions && protocol == config.ProtocolAnthropic:
			upstreamOp = anthropicOperationMessages
			upstream, prepareErr = translateChatCompletionsRequestToAnthropic(envelope)
			if prepareErr == nil {
				upstream, prepareErr = rewriteTranslatedAnthropicModel(upstream, deployment.Model)
			}
		case h.operation == openAIOperationChatCompletions && protocol == config.ProtocolGemini:
			upstreamOp = geminiOperationGenerateContent
			if streaming {
				upstreamOp = geminiOperationStreamContent
			}
			upstream, prepareErr = translateChatCompletionsRequestToGemini(envelope)
		case h.operation == openAIOperationChatCompletions && protocol == config.ProtocolBedrock:
			upstreamOp = bedrockOperationConverse
			if streaming {
				upstreamOp = bedrockOperationConverseStream
			}
			upstream, prepareErr = translateChatCompletionsRequestToBedrock(envelope)
		case h.operation == openAIOperationEmbeddings && protocol == config.ProtocolGemini:
			upstreamOp = geminiOperationEmbedContent
			upstream, result.embeddingDimensions, result.embeddingInputCount, prepareErr = translateOpenAIEmbeddingsRequestToGemini(envelope)
			if result.embeddingInputCount > 1 {
				upstreamOp = geminiOperationBatchEmbed
				if prepareErr == nil {
					upstream, prepareErr = rewriteGeminiBatchEmbeddingModel(upstream, deployment.Model)
				}
			}
		default:
			continue
		}
		if prepareErr != nil {
			last = prepareErr
			continue
		}
		_, out, _ := model.EffectiveProfileParameters(deployment, h.operation.String(), upstreamOp.String())
		// Same-operation overlapping controls have unambiguous meaning; an upstream
		// override must not silently contradict an ingress override.
		if parameter := profileParameterConflict(in, out, h.operation, upstreamOp); parameter != "" {
			return result, fmt.Errorf("conflicting ingress/upstream parameter %s", parameter)
		}

		upstream, prepareErr = applyRequestOverrides(upstream, out)
		if prepareErr != nil {
			last = prepareErr
			continue
		}
		if int64(len(upstream)) > h.maxRequestBytes {
			return result, fmt.Errorf("profile request exceeds body limit")
		}
		upstreamEnvelope, _, _, prepareErr := upstreamOp.decodeRequest(upstream, deployment.Model)
		if prepareErr != nil {
			last = prepareErr
			continue
		}
		upstreamRequired, prepareErr := upstreamOp.detectCapabilities(upstreamEnvelope, streaming)
		if prepareErr != nil {
			last = prepareErr
			continue
		}
		candidateRequired := slices.Clone(required)
		for _, capability := range upstreamRequired {
			if !slices.Contains(candidateRequired, capability) {
				candidateRequired = append(candidateRequired, capability)
			}
		}
		if provider.Type == "openai_subscription" {
			candidateRequired = slices.DeleteFunc(candidateRequired, func(c config.Capability) bool { return c == config.CapabilityStoredCompletion })
		}
		result.body = upstream
		result.route = routing.ProtocolRoute{Protocol: protocol, Native: native, RequiredCapabilities: candidateRequired}
		return result, nil
	}
	if last == nil {
		last = fmt.Errorf("no supported translation for profile request")
	}
	return result, last
}
func jsonEquivalent(a, b json.RawMessage) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	aa, _ := json.Marshal(x)
	bb, _ := json.Marshal(y)
	return string(aa) == string(bb)
}

func profileParameterConflict(in, out map[string]json.RawMessage, from, to openAIOperation) string {
	comparable := config.MergeProfileParameters(nil, in)
	if from == openAIOperationResponses && to == openAIOperationChatCompletions {
		var reasoning map[string]json.RawMessage
		if json.Unmarshal(in["reasoning"], &reasoning) == nil {
			if effort, ok := reasoning["effort"]; ok {
				comparable["reasoning_effort"] = effort
			}
		}
		if value, ok := in["max_output_tokens"]; ok {
			comparable["max_completion_tokens"] = value
			comparable["max_tokens"] = value
		}
	}
	for key, value := range out {
		previous, ok := comparable[key]
		if !ok {
			continue
		}
		var a, b map[string]json.RawMessage
		if json.Unmarshal(previous, &a) == nil && a != nil && json.Unmarshal(value, &b) == nil && b != nil {
			if nested := profileParameterConflict(a, b, from, to); nested != "" {
				return key + "/" + nested
			}
			continue
		}
		if !jsonEquivalent(previous, value) {
			return key
		}
	}
	return ""
}
