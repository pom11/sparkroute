// SPDX-FileCopyrightText: 2026 Scitrera LLC
// SPDX-FileCopyrightText: 2026 Fox Engine Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/sparksq/sparkroute/pkg/admin"
	"github.com/sparksq/sparkroute/pkg/adminapi"
	"github.com/sparksq/sparkroute/pkg/telemetry/otlpexport"
)

func TestStandaloneUnroutedEndpointInventoryTracksDiscovery(t *testing.T) {
	upstream := newAcceptanceUpstream(t)
	statePath := newAcceptanceBridgeFixture(t, upstream.server.URL, acceptanceBridgeState{WarmVisible: true})
	document, _ := acceptanceRoutingDocument(true, 32, 1<<20, 10*time.Second, 0)
	document.ModelRouting = nil
	document.VirtualModels = document.VirtualModels[:1]
	document.VirtualModels[0].Aliases = []string{"warm-upstream"}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	generation, err := buildRuntimeGeneration(document, "inventory-test", runtimeBuildOptions{
		Context: ctx, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Telemetry: &otlpexport.Runtime{},
		SparkrunEnabled: true, SparkrunCommand: executable, SparkrunEndpointTTL: 10 * time.Second,
		SparkrunReconcile: time.Second, SparkrunStopTimeout: time.Second,
		AdminEnabled: true, AllowInsecureAdmin: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer generation.close()
	get := func(path string, result any) {
		t.Helper()
		response := httptest.NewRecorder()
		generation.admin.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), result) != nil {
			t.Fatalf("%s: %d %s", path, response.Code, response.Body)
		}
	}
	var bootstrap adminapi.Bootstrap
	get("/v1/ui/bootstrap", &bootstrap)
	if !bootstrap.Features[adminapi.FeatureEndpointInventory] {
		t.Fatal("endpoint inventory is not advertised")
	}
	type endpoint struct {
		Target      string `json:"target"`
		State       string `json:"state"`
		Cluster     string `json:"cluster_id"`
		ClusterName string `json:"cluster_name"`
		Expired     bool   `json:"expired"`
	}
	inventory := func() []endpoint {
		var page struct {
			Endpoints []endpoint `json:"endpoints"`
		}
		get("/v1/runtime/endpoints?target=warm-deployment", &page)
		return page.Endpoints
	}
	waitAcceptance(t, 5*time.Second, func() bool {
		endpoints := inventory()
		return len(endpoints) == 1 && endpoints[0].State == "ready" && endpoints[0].Cluster == "warm-cluster" && endpoints[0].ClusterName == "local-cluster" && !endpoints[0].Expired
	}, "unrouted discovered endpoint is ready")
	checkUnrouted := func() {
		t.Helper()
		var status admin.Status
		get("/v1/status", &status)
		for _, target := range status.Targets {
			if target.Deployment == "warm-deployment" {
				if target.RoutingState != "unrouted" || len(target.RoutingConflicts) != 1 {
					t.Fatalf("routing changed: %#v", target)
				}
				return
			}
		}
		t.Fatal("discovered deployment disappeared from inventory")
	}
	checkUnrouted()
	if _, err := updateAcceptanceBridgeState(statePath, func(state *acceptanceBridgeState) error {
		state.WarmVisible = false
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	waitAcceptance(t, 5*time.Second, func() bool { return len(inventory()) == 0 }, "removed endpoint disappears from live inventory")
	checkUnrouted()
	state := mustAcceptanceBridgeState(t, statePath)
	if state.EnsureCalls != 0 || state.StopCalls != 0 {
		t.Fatalf("inventory caused workload lifecycle changes: %#v", state)
	}
}
