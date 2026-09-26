// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Scitrera LLC
// Copyright 2026 Fox Engine Ltd.
package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sparksq/sparkroute/pkg/config"
)

func TestDeploymentProfileFailoverUsesIsolatedParameters(t *testing.T) {
	received := []map[string]json.RawMessage{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]json.RawMessage
		_ = json.NewDecoder(r.Body).Decode(&body)
		received = append(received, body)
		w.Header().Set("Content-Type", "application/json")
		if len(received) == 1 {
			w.WriteHeader(502)
			_, _ = w.Write([]byte(`{"error":{"message":"retry"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"ok","choices":[]}`))
	}))
	defer upstream.Close()
	doc := proxyDocument(upstream.URL + "/v1")
	first := doc.Deployments[0]
	second := first
	second.Name = "other"
	second.Model = "second-runtime"
	first.RequestProfiles = map[string]config.RequestProfileImplementation{"low": {IngressOverrides: config.RequestParameters{"chat_completions": {"temperature": json.RawMessage(`0.2`), "chat_template_kwargs": json.RawMessage(`{"first_only":true}`)}}}}
	second.RequestProfiles = map[string]config.RequestProfileImplementation{"low": {IngressOverrides: config.RequestParameters{"chat_completions": {"temperature": json.RawMessage(`0.4`), "chat_template_kwargs": json.RawMessage(`{"enable_thinking":false}`)}}}}
	doc.Deployments = []config.Deployment{first, second}
	parent := doc.VirtualModels[0]
	parent.Name = "coding"
	parent.Aliases = []string{"code"}
	parent.Limits.MaxAttempts = 2
	parent.Pools = []config.RoutingPool{{Priority: 0, Targets: []config.WeightedTarget{{Deployment: first.Name, Weight: 1}}}, {Priority: 1, Targets: []config.WeightedTarget{{Deployment: second.Name, Weight: 1}}}}
	profile := config.VirtualModel{Name: "coding:low", Profile: &config.RequestProfileBinding{Parent: "coding", Selector: "low"}}
	doc.VirtualModels = []config.VirtualModel{parent, profile}
	handler, err := NewDataHandler(doc, DataOptions{})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"code:low","messages":[{"role":"user","content":"hello"}],"temperature":1,"chat_template_kwargs":{"caller":true}}`))
	req.Header.Set("Content-Type", "application/json")
	out := httptest.NewRecorder()
	handler.ServeHTTP(out, req)
	if out.Code != 200 || len(received) != 2 {
		t.Fatalf("%d %s calls=%d", out.Code, out.Body, len(received))
	}
	if string(received[0]["temperature"]) != "0.2" || string(received[1]["temperature"]) != "0.4" || strings.Contains(string(received[1]["chat_template_kwargs"]), "first_only") || !strings.Contains(string(received[1]["chat_template_kwargs"]), "caller") {
		t.Fatalf("incorrect per-target bodies: %s", received)
	}
	if string(received[1]["model"]) != `"second-runtime"` {
		t.Fatal(received[1])
	}
}
func TestDeploymentProfileRejectsMissingOperationBeforeUpstream(t *testing.T) {
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++ }))
	defer upstream.Close()
	doc := proxyDocument(upstream.URL + "/v1")
	parent := doc.VirtualModels[0]
	doc.Deployments[0].RequestProfiles = map[string]config.RequestProfileImplementation{"low": {IngressOverrides: config.RequestParameters{"responses": {"temperature": json.RawMessage(`0.2`)}}}}
	doc.VirtualModels = append(doc.VirtualModels, config.VirtualModel{Name: "coding:low", Profile: &config.RequestProfileBinding{Parent: parent.Name, Selector: "low"}})
	handler, err := NewDataHandler(doc, DataOptions{})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"coding:low","messages":[]}`))
	req.Header.Set("Content-Type", "application/json")
	out := httptest.NewRecorder()
	handler.ServeHTTP(out, req)
	if out.Code != 400 || calls != 0 || !strings.Contains(out.Body.String(), "request_profile_unsupported") {
		t.Fatalf("%d %s calls=%d", out.Code, out.Body, calls)
	}
}
func TestDeploymentProfileResponsesTranslationAndUpstreamParameters(t *testing.T) {
	captured := map[string]json.RawMessage{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path %s", r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&captured)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"ok","model":"test","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}]}`))
	}))
	defer upstream.Close()
	doc := proxyDocument(upstream.URL + "/v1")
	parent := doc.VirtualModels[0]
	doc.Deployments[0].RequestProfiles = map[string]config.RequestProfileImplementation{"low": {SupportedOperations: []string{"responses"}, UpstreamOverrides: config.RequestParameters{"chat_completions": {"chat_template_kwargs": json.RawMessage(`{"enable_thinking":false}`)}}}}
	doc.VirtualModels = append(doc.VirtualModels, config.VirtualModel{Name: "coding:low", Profile: &config.RequestProfileBinding{Parent: parent.Name, Selector: "low"}})
	handler, err := NewDataHandler(doc, DataOptions{})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{"model":"coding:low","input":"hello","store":false}`))
	req.Header.Set("Content-Type", "application/json")
	out := httptest.NewRecorder()
	handler.ServeHTTP(out, req)
	if out.Code != 200 || string(captured["chat_template_kwargs"]) != `{"enable_thinking":false}` {
		t.Fatalf("%d %s request=%s", out.Code, out.Body, captured)
	}
}

func TestDeploymentProfileStreamingSkipsUnsupportedCandidates(t *testing.T) {
	received := make(chan map[string]json.RawMessage, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		received <- body
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"id\":\"chunk\",\"model\":\"second-runtime\",\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\ndata: [DONE]\n\n"))
	}))
	defer upstream.Close()
	for _, reason := range []string{"missing profile", "missing API", "unsupported augmented capability"} {
		t.Run(reason, func(t *testing.T) {
			doc := proxyDocument(upstream.URL + "/v1")
			first := doc.Deployments[0]
			second := first
			second.Name = "other"
			second.Model = "second-runtime"
			implementation := config.RequestProfileImplementation{IngressOverrides: config.RequestParameters{"chat_completions": {"temperature": json.RawMessage(`0.4`)}}}
			second.RequestProfiles = map[string]config.RequestProfileImplementation{"low": implementation}
			switch reason {
			case "missing API":
				first.RequestProfiles = map[string]config.RequestProfileImplementation{"low": {SupportedOperations: []string{"responses"}}}
			case "unsupported augmented capability":
				first.RequestProfiles = map[string]config.RequestProfileImplementation{"low": {IngressOverrides: config.RequestParameters{"chat_completions": {"logprobs": json.RawMessage(`true`)}}}}
				first.CapabilityPolicy.Unsupported = []config.Capability{config.CapabilityLogprobs}
			}
			doc.Deployments = []config.Deployment{first, second}
			parent := doc.VirtualModels[0]
			parent.Pools = []config.RoutingPool{{Priority: 0, Targets: []config.WeightedTarget{{Deployment: first.Name, Weight: 1}}}, {Priority: 1, Targets: []config.WeightedTarget{{Deployment: second.Name, Weight: 1}}}}
			doc.VirtualModels = []config.VirtualModel{parent, {Name: "coding:low", Profile: &config.RequestProfileBinding{Parent: parent.Name, Selector: "low"}}}
			handler, err := NewDataHandler(doc, DataOptions{})
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"coding:low","messages":[],"stream":true}`))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != 200 || !strings.Contains(response.Body.String(), "data: [DONE]") {
				t.Fatalf("%d %s", response.Code, response.Body)
			}
			body := <-received
			if string(body["model"]) != `"second-runtime"` || string(body["temperature"]) != "0.4" || string(body["stream"]) != "true" || body["logprobs"] != nil {
				t.Fatalf("wrong profile candidate: %s", body)
			}
		})
	}
}
