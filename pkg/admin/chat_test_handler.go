// SPDX-FileCopyrightText: 2026 Scitrera LLC
// SPDX-FileCopyrightText: 2026 Fox Engine Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

package admin

import (
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"sort"
	"strings"

	"github.com/sparksq/sparkroute/pkg/config"
)

type chatTestModel struct {
	ID         string   `json:"id"`
	Operations []string `json:"operations"`
}

func (h *handler) chatTestModels() []chatTestModel {
	document, err := h.document.ResolveRequestProfiles()
	if err != nil {
		return nil
	}
	names := map[string][]string{}
	for _, model := range document.VirtualModels {
		if model.Visibility.Effective() == config.ModelVisibilityInternal {
			continue
		}
		operations := []string{}
		for _, operation := range []string{"chat_completions", "responses"} {
			supported := model.Profile == nil
			for _, pool := range model.Pools {
				for _, target := range pool.Targets {
					for _, deployment := range document.Deployments {
						if deployment.Name == target.Deployment {
							_, _, ok := model.EffectiveProfileParameters(deployment, operation, "")
							supported = supported || ok
						}
					}
				}
			}
			if supported {
				operations = append(operations, operation)
			}
		}
		for _, name := range append([]string{model.Name}, model.Aliases...) {
			names[name] = operations
		}
	}
	if document.ModelRouting != nil {
		for name := range document.ModelRouting.VirtualModels {
			names[name] = []string{"chat_completions", "responses"}
		}
	}
	result := make([]chatTestModel, 0, len(names))
	for name, operations := range names {
		result = append(result, chatTestModel{ID: name, Operations: operations})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func (h *handler) serveChatTestModels(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, RoleConfigWrite) || !allowRead(w, r) {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"models": h.chatTestModels()})
}

// Chat tests are operator-initiated inference through the current data plane.
// The browser cannot supply URLs, credentials, state IDs, tools, or launch flags.
func (h *handler) serveChatTest(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, RoleConfigWrite) {
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeError(w, 405, "method_not_allowed", "use POST")
		return
	}
	mediaType, _, mediaErr := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mediaErr != nil || mediaType != "application/json" {
		writeError(w, 415, "invalid_content_type", "use application/json")
		return
	}
	var input struct {
		Model     string `json:"model"`
		Operation string `json:"operation"`
		Messages  []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
		MaxTokens int `json:"max_tokens"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeError(w, 400, "invalid_request", "invalid chat test request")
		return
	}
	if decoder.Decode(new(any)) != io.EOF {
		writeError(w, 400, "invalid_request", "expected one JSON object")
		return
	}
	if input.Operation == "" {
		input.Operation = "chat_completions"
	}
	if input.Operation != "chat_completions" && input.Operation != "responses" {
		writeError(w, 400, "invalid_operation", "choose Chat Completions or Responses")
		return
	}
	found := false
	for _, model := range h.chatTestModels() {
		if model.ID == input.Model {
			found = true
			break
		}
	}
	if !found {
		writeError(w, 400, "unknown_model", "choose a configured virtual model, profile, or alias")
		return
	}
	if len(input.Messages) == 0 || len(input.Messages) > 128 {
		writeError(w, 400, "invalid_messages", "send between 1 and 128 messages")
		return
	}
	for _, message := range input.Messages {
		if (message.Role != "user" && message.Role != "assistant" && message.Role != "system") || strings.TrimSpace(message.Content) == "" {
			writeError(w, 400, "invalid_messages", "messages require a user, assistant, or system role and text")
			return
		}
	}
	if input.MaxTokens == 0 {
		input.MaxTokens = 1024
	}
	if input.MaxTokens < 1 || input.MaxTokens > 8192 {
		writeError(w, 400, "invalid_limit", "output limit must be between 1 and 8192 tokens")
		return
	}
	body := map[string]any{"model": input.Model, "stream": true, "store": false}
	path := "/v1/chat/completions"
	if input.Operation == "responses" {
		path = "/v1/responses"
		body["input"] = input.Messages
		body["max_output_tokens"] = input.MaxTokens
	} else {
		body["messages"] = input.Messages
		body["max_tokens"] = input.MaxTokens
		body["stream_options"] = map[string]bool{"include_usage": true}
	}
	raw, err := json.Marshal(body)
	if err != nil {
		writeError(w, 400, "invalid_request", "cannot encode chat request")
		return
	}
	forward := r.Clone(r.Context())
	forward.URL.Path = path
	forward.URL.RawPath = ""
	forward.URL.RawQuery = ""
	forward.RequestURI = path
	// Do not forward admin credentials or unrelated browser headers to inference.
	forward.Header = http.Header{"Content-Type": []string{"application/json"}, "Accept": []string{"text/event-stream"}}
	forward.Body = io.NopCloser(bytes.NewReader(raw))
	forward.ContentLength = int64(len(raw))
	w.Header().Set("Cache-Control", "no-store")
	h.options.ChatTest.ServeHTTP(w, forward)
}
