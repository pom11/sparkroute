// SPDX-FileCopyrightText: 2026 Scitrera LLC
// SPDX-FileCopyrightText: 2026 Fox Engine Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

package admin

import (
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/sparksq/sparkroute/pkg/endpointregistry"
)

// Inventory deliberately exposes only presentation fields, never endpoint URLs,
// controller metadata maps, credentials, or fencing tokens.
type runtimeEndpoint struct {
	EndpointID      string                 `json:"endpoint_id"`
	Target          string                 `json:"target"`
	Protocol        string                 `json:"protocol,omitempty"`
	ServedModels    []string               `json:"served_models"`
	Controller      string                 `json:"controller,omitempty"`
	ClusterID       string                 `json:"cluster_id,omitempty"`
	ClusterName     string                 `json:"cluster_name,omitempty"`
	JobID           string                 `json:"job_id,omitempty"`
	BindingRevision string                 `json:"binding_revision,omitempty"`
	RecipeRevision  string                 `json:"recipe_revision,omitempty"`
	State           endpointregistry.State `json:"state"`
	ActiveRequests  int                    `json:"active_requests"`
	MaxConcurrency  int                    `json:"max_concurrency"`
	RegisteredAt    *time.Time             `json:"registered_at,omitempty"`
	HeartbeatAt     *time.Time             `json:"heartbeat_at,omitempty"`
	ExpiresAt       *time.Time             `json:"expires_at,omitempty"`
	Expired         bool                   `json:"expired"`
}

type runtimeEndpointPage struct {
	Endpoints  []runtimeEndpoint `json:"endpoints"`
	NextCursor string            `json:"next_cursor,omitempty"`
}

func (h *handler) runtimeEndpoints(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		methodNotAllowed(writer, http.MethodGet)
		return
	}
	if !requireRole(writer, request, RoleStatusRead) {
		return
	}
	values, err := url.ParseQuery(request.URL.RawQuery)
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_query", "endpoint query is malformed")
		return
	}
	if !strictManagedQuery(writer, values, "target", "controller", "state", "limit", "cursor") {
		return
	}
	limit := 0
	if values.Get("limit") != "" {
		limit, err = strconv.Atoi(values.Get("limit"))
		if err != nil {
			writeError(writer, http.StatusBadRequest, "invalid_query", "limit must be an integer")
			return
		}
	}
	query, err := endpointregistry.ValidateQuery(endpointregistry.Query{
		Target: values.Get("target"), Controller: values.Get("controller"),
		State: endpointregistry.State(values.Get("state")), Limit: limit, Cursor: values.Get("cursor"),
	})
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_query", "endpoint filters or pagination are invalid")
		return
	}
	page, err := h.options.EndpointInventory.List(request.Context(), query)
	if err != nil || len(page.Endpoints) > query.Limit {
		writeError(writer, http.StatusServiceUnavailable, "runtime_unavailable", "Endpoint inventory is unavailable")
		return
	}
	result := runtimeEndpointPage{Endpoints: make([]runtimeEndpoint, 0, len(page.Endpoints)), NextCursor: page.NextCursor}
	now := time.Now()
	for _, endpoint := range page.Endpoints {
		if err := endpointregistry.ValidateEndpoint(endpoint); err != nil {
			writeError(writer, http.StatusServiceUnavailable, "runtime_unavailable", "Endpoint inventory is unavailable")
			return
		}
		state := endpoint.State
		if state == "" {
			state = endpointregistry.StateUnknown
		}
		result.Endpoints = append(result.Endpoints, runtimeEndpoint{
			EndpointID: endpoint.ID, Target: endpoint.Target, Protocol: endpoint.Protocol,
			ServedModels: append([]string{}, endpoint.ServedModels...), Controller: endpoint.Controller,
			ClusterID: endpoint.ClusterID, ClusterName: endpoint.Metadata["cluster_name"], JobID: endpoint.JobID,
			BindingRevision: endpoint.BindingRevision, RecipeRevision: endpoint.RecipeRevision,
			State: state, ActiveRequests: endpoint.ActiveRequests, MaxConcurrency: endpoint.MaxConcurrency,
			RegisteredAt: endpointTime(endpoint.RegisteredAt), HeartbeatAt: endpointTime(endpoint.HeartbeatAt),
			ExpiresAt: endpointTime(endpoint.ExpiresAt), Expired: !endpoint.ExpiresAt.IsZero() && !endpoint.ExpiresAt.After(now),
		})
	}
	writer.Header().Set("Cache-Control", "no-store")
	writeJSON(writer, http.StatusOK, result)
}

func endpointTime(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	utc := value.UTC()
	return &utc
}
