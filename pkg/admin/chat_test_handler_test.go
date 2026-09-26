// SPDX-FileCopyrightText: 2026 Scitrera LLC
// SPDX-FileCopyrightText: 2026 Fox Engine Ltd.
// SPDX-License-Identifier: AGPL-3.0-only
package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sparksq/sparkroute/pkg/config"
	"github.com/sparksq/sparkroute/pkg/identity"
)

func TestChatTestUsesProfileAliasesAndNormalInferenceWithNoStoredState(t *testing.T) {
	document := config.Document{VirtualModels: []config.VirtualModel{{Name: "coding", Aliases: []string{"code"}}, {Name: "coding:quick", Profile: &config.RequestProfileBinding{Parent: "coding", Selector: "quick", Manual: true}}, {Name: "internal", Visibility: config.ModelVisibilityInternal}}}
	calls := 0
	handler := NewHandler(document, "test", Options{AllowInsecureAdmin: true, ChatTest: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "" || r.URL.RawQuery != "" {
			t.Error("forwarded admin credentials/query")
		}
		caller, _ := identity.FromContext(r.Context())
		if caller.Principal.ID != "local-insecure-operator" {
			t.Error("operator attribution lost")
		}
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if string(body["model"]) != `"code:quick"` || string(body["store"]) != "false" || string(body["stream"]) != "true" {
			t.Fatalf("wrong body: %s", body)
		}
		if r.URL.Path == "/v1/responses" {
			if body["input"] == nil || body["max_output_tokens"] == nil {
				t.Fatal(body)
			}
		} else if r.URL.Path != "/v1/chat/completions" {
			t.Fatal(r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	})})
	list := httptest.NewRecorder()
	handler.ServeHTTP(list, httptest.NewRequest("GET", "/v1/chat-test/models", nil))
	if list.Code != 200 || !strings.Contains(list.Body.String(), "code:quick") || strings.Contains(list.Body.String(), "internal") {
		t.Fatal(list.Code, list.Body)
	}
	for _, op := range []string{"chat_completions", "responses"} {
		request := httptest.NewRequest("POST", "/v1/chat-test?ignored=yes", strings.NewReader(`{"model":"code:quick","operation":"`+op+`","messages":[{"role":"user","content":"hi"}],"max_tokens":20}`))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Authorization", "Bearer secret-admin")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != 200 || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatal(response.Code, response.Body)
		}
	}
	if calls != 2 {
		t.Fatal(calls)
	}
}
func TestChatTestRejectsReadersAndUnboundedOrStatefulInput(t *testing.T) {
	called := false
	inference := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true })
	reader := NewHandler(config.Document{}, "test", Options{Authenticator: metadataTestAuthenticator{}, ChatTest: inference})
	request := httptest.NewRequest("POST", "/v1/chat-test", strings.NewReader(`{}`))
	request.Header.Set("Authorization", "Bearer metadata-token")
	response := httptest.NewRecorder()
	reader.ServeHTTP(response, request)
	if response.Code != 403 || called {
		t.Fatal(response.Code, called)
	}
	handler := NewHandler(config.Document{VirtualModels: []config.VirtualModel{{Name: "coding"}}}, "test", Options{AllowInsecureAdmin: true, ChatTest: inference})
	for _, body := range []string{
		`{"model":"coding","messages":[{"role":"user","content":"hi"}],"store":true}`,
		`{"model":"coding","messages":[{"role":"user","content":"hi"}],"max_tokens":999999}`,
		`{"model":"coding","messages":[{"role":"tool","content":"hi"}]}`,
		`{"model":"missing","messages":[{"role":"user","content":"hi"}]}`,
		`{"model":"coding","messages":[]}`, strings.Repeat("x", 300<<10),
	} {
		request := httptest.NewRequest("POST", "/v1/chat-test", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != 400 || called {
			t.Fatal(response.Code, called)
		}
	}
}
