// SPDX-FileCopyrightText: 2026 Scitrera LLC
// SPDX-FileCopyrightText: 2026 Fox Engine Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/sparksq/sparkroute/pkg/adminapi"
	"github.com/sparksq/sparkroute/pkg/config"
	"github.com/sparksq/sparkroute/pkg/endpointregistry"
	"github.com/sparksq/sparkroute/pkg/identity"
)

type endpointAuthenticator struct{}

func (endpointAuthenticator) Authenticate(_ context.Context, request *http.Request) (identity.Principal, error) {
	switch request.Header.Get("Authorization") {
	case "Bearer status":
		return identity.Principal{ID: "status-reader", Roles: []string{RoleStatusRead}}, nil
	case "Bearer config":
		return identity.Principal{ID: "config-reader", Roles: []string{RoleConfigRead}}, nil
	}
	return identity.Principal{}, identity.ErrInvalidCredentials
}

func endpointRequest(handler http.Handler, method, path, token string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, nil)
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func TestRuntimeEndpointInventory(t *testing.T) {
	registry := endpointregistry.NewMemory()
	now := time.Now()
	for _, endpoint := range []endpointregistry.Endpoint{
		{ID: "a", Target: "discovered", BaseURL: "http://private-host:8000/v1", Protocol: "openai", ServedModels: []string{"deepseek"}, Controller: "sparkrun", ClusterID: "workload-1", JobID: "job", BindingRevision: "binding", RecipeRevision: "recipe", FencingToken: 7, State: endpointregistry.StateReady, RegisteredAt: now, HeartbeatAt: now, ExpiresAt: now.Add(time.Minute), Metadata: map[string]string{"secret": "private-value", "cluster_name": "sparks25"}},
		{ID: "b", Target: "discovered", BaseURL: "http://private-host:8001/v1", Controller: "sparkrun", State: endpointregistry.StateReady, ExpiresAt: now.Add(-time.Minute)},
		{ID: "c", Target: "other", BaseURL: "http://private-host:8002/v1", State: endpointregistry.StateOffline},
	} {
		if err := registry.Register(t.Context(), endpoint); err != nil {
			t.Fatal(err)
		}
	}
	// No virtual model references this deployment; inventory still reports it.
	handler := NewHandler(config.Document{}, "revision", Options{EndpointInventory: registry, AllowInsecureAdmin: true})
	response := endpointRequest(handler, http.MethodGet, "/v1/runtime/endpoints?controller=sparkrun&state=ready&target=discovered&limit=1", "")
	var page runtimeEndpointPage
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &page) != nil {
		t.Fatalf("inventory: %d %s", response.Code, response.Body)
	}
	if len(page.Endpoints) != 1 || page.NextCursor == "" {
		t.Fatalf("page: %#v", page)
	}
	endpoint := page.Endpoints[0]
	if endpoint.EndpointID != "a" || endpoint.ClusterID != "workload-1" || endpoint.ClusterName != "sparks25" || endpoint.State != endpointregistry.StateReady || endpoint.Expired || endpoint.HeartbeatAt == nil || len(endpoint.ServedModels) != 1 {
		t.Fatalf("endpoint: %#v", endpoint)
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("inventory must not be cached")
	}
	for _, secret := range []string{"private-host", "private-value", "base_url", "metadata", "fencing_token"} {
		if strings.Contains(response.Body.String(), secret) {
			t.Fatalf("exposed %q", secret)
		}
	}
	response = endpointRequest(handler, http.MethodGet, "/v1/runtime/endpoints?target=discovered&controller=sparkrun&state=ready&limit=1&cursor="+url.QueryEscape(page.NextCursor), "")
	page = runtimeEndpointPage{}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &page) != nil || len(page.Endpoints) != 1 || page.Endpoints[0].EndpointID != "b" || !page.Endpoints[0].Expired || page.NextCursor != "" {
		t.Fatalf("expired page: %s", response.Body)
	}
	response = endpointRequest(handler, http.MethodGet, "/v1/runtime/endpoints?target=absent", "")
	if !strings.Contains(response.Body.String(), `"endpoints":[]`) {
		t.Fatalf("empty inventory: %s", response.Body)
	}
	if err := registry.Remove(t.Context(), "a"); err != nil {
		t.Fatal(err)
	}
	response = endpointRequest(handler, http.MethodGet, "/v1/runtime/endpoints?target=discovered", "")
	page = runtimeEndpointPage{}
	if json.Unmarshal(response.Body.Bytes(), &page) != nil || len(page.Endpoints) != 1 || page.Endpoints[0].EndpointID != "b" {
		t.Fatalf("removed endpoint survived: %s", response.Body)
	}
}

func TestRuntimeEndpointInventoryAccessAndQueries(t *testing.T) {
	registry := endpointregistry.NewMemory()
	handler := NewHandler(config.Document{}, "revision", Options{EndpointInventory: registry, Authenticator: endpointAuthenticator{}})
	for _, tc := range []struct {
		token   string
		code    int
		feature bool
	}{
		{"", http.StatusUnauthorized, false}, {"config", http.StatusForbidden, false}, {"status", http.StatusOK, true},
	} {
		response := endpointRequest(handler, http.MethodGet, "/v1/runtime/endpoints", tc.token)
		if response.Code != tc.code {
			t.Fatalf("token %q: %d %s", tc.token, response.Code, response.Body)
		}
		if tc.token == "" {
			continue
		}
		response = endpointRequest(handler, http.MethodGet, "/v1/ui/bootstrap", tc.token)
		var bootstrap adminapi.Bootstrap
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &bootstrap) != nil || bootstrap.Features[adminapi.FeatureEndpointInventory] != tc.feature || bootstrap.Features[adminapi.FeatureLifecycle] != tc.feature {
			t.Fatalf("bootstrap: %s", response.Body)
		}
	}
	for _, query := range []string{"target=%zz", "limit=-1", "limit=501", "limit=bad", "cursor=bad", "state=bad", "target=a&target=b", "unknown=1", "controller=" + strings.Repeat("x", 1025)} {
		response := endpointRequest(handler, http.MethodGet, "/v1/runtime/endpoints?"+query, "status")
		if response.Code != http.StatusBadRequest {
			t.Errorf("query %q: %d %s", query, response.Code, response.Body)
		}
	}
	if response := endpointRequest(handler, http.MethodPost, "/v1/runtime/endpoints", "status"); response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("method: %d", response.Code)
	}
	for _, options := range []Options{{EndpointInventory: registry}, {AllowInsecureAdmin: true}} {
		unavailable := NewHandler(config.Document{}, "revision", options)
		response := endpointRequest(unavailable, http.MethodGet, "/v1/runtime/endpoints", "")
		if response.Code != http.StatusNotFound {
			t.Fatalf("unsupported inventory: %d", response.Code)
		}
		response = endpointRequest(unavailable, http.MethodGet, "/v1/ui/bootstrap", "")
		var bootstrap adminapi.Bootstrap
		if json.Unmarshal(response.Body.Bytes(), &bootstrap) != nil || bootstrap.Features[adminapi.FeatureEndpointInventory] {
			t.Fatalf("unsupported bootstrap: %s", response.Body)
		}
	}
}

type unavailableEndpoints struct{}

func (unavailableEndpoints) List(context.Context, endpointregistry.Query) (endpointregistry.Page, error) {
	return endpointregistry.Page{}, errors.New("private registry error")
}
func TestRuntimeEndpointInventoryFailureIsSafe(t *testing.T) {
	handler := NewHandler(config.Document{}, "revision", Options{EndpointInventory: unavailableEndpoints{}, AllowInsecureAdmin: true})
	response := endpointRequest(handler, http.MethodGet, "/v1/runtime/endpoints", "")
	if response.Code != http.StatusServiceUnavailable || strings.Contains(response.Body.String(), "private") {
		t.Fatalf("failure: %d %s", response.Code, response.Body)
	}
}
