// SPDX-FileCopyrightText: 2026 Scitrera LLC
// SPDX-FileCopyrightText: 2026 Fox Engine Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

package sparkrun

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestDaemonCoreActivationUsesFlatBindingAndPreservesAdoption(t *testing.T) {
	for _, owner := range []string{"sparkroute", "daemon"} {
		t.Run(owner, func(t *testing.T) {
			var submitted atomic.Int32
			base := daemonHandler(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1/ops/workloads.ensure_ready":
					var args map[string]any
					json.NewDecoder(r.Body).Decode(&args)
					if args["recipe"] != "recipe" || args["recipe_revision"] != "rev" || args["binding"] != nil || args["owner"] != nil || args["manager"] != nil {
						t.Errorf("wrong core request %v", args)
					}
					submitted.Add(1)
					w.WriteHeader(202)
					fmt.Fprintf(w, `{"job_id":%q}`, testDaemonJob)
				case "/v1/jobs/" + testDaemonJob:
					raw := strings.ReplaceAll(testDaemonReady, `"owner":"sparkroute"`, `"owner":"`+owner+`"`)
					if owner != "sparkroute" {
						raw = strings.ReplaceAll(raw, `"owned":true`, `"owned":false`)
					}
					fmt.Fprintf(w, `{"id":%q,"state":"succeeded","result":%s}`, testDaemonJob, raw)
				case "/v1/ops/plugins.sparkroute.endpoints.metadata":
					fmt.Fprint(w, `{"model_metadata":{"model":{"size_b":7}}}`)
				default:
					t.Errorf("unexpected route %s", r.URL.Path)
					w.WriteHeader(404)
				}
			})
			c, _ := testDaemonClient(t, base)
			value, err := c.EnsureReady(context.Background(), Binding{Recipe: "recipe", RecipeRevision: "rev"}, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			if submitted.Load() != 1 || !value.Adopted || value.Endpoint.Owned != (owner == "sparkroute") || value.Endpoint.JobID != "work" || value.Endpoint.ObservedAt != 123.5 {
				t.Fatalf("bad adoption %+v", value)
			}
			if value.Endpoint.ModelMetadata["model"].SizeB == nil {
				t.Fatal("lost advisory model metadata")
			}
		})
	}
}
func TestDaemonWrongManagerRejectedBeforeSubmission(t *testing.T) {
	var posts atomic.Int32
	base := daemonHandler(func(w http.ResponseWriter, r *http.Request) { posts.Add(1) })
	c, _ := testDaemonClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/info" {
			fmt.Fprint(w, `{"protocol":1,"binding":"controller","profile":"sparkrun","manager":"daemon"}`)
			return
		}
		base(w, r)
	}))
	if _, err := c.EnsureReady(context.Background(), Binding{Recipe: "r"}, time.Second); err == nil {
		t.Fatal("accepted wrong management identity")
	}
	if posts.Load() != 0 {
		t.Fatal("submitted with wrong owner")
	}
}
func TestDaemonReadyRejectsInconsistentOwnershipAndIdentity(t *testing.T) {
	for _, raw := range []string{
		strings.ReplaceAll(testDaemonReady, `"owned":true`, `"owned":false`),
		strings.Replace(testDaemonReady, `"workload_id":"work"`, `"workload_id":"other"`, 1),
		strings.ReplaceAll(testDaemonReady, `"healthy":true`, `"healthy":false`),
		strings.ReplaceAll(testDaemonReady, `"observed_at":123.5`, `"observed_at":0`),
	} {
		if _, err := decodeDaemonReady(json.RawMessage(raw), Binding{Recipe: "recipe", RecipeRevision: "rev"}); err == nil {
			t.Fatal("accepted inconsistent readiness")
		}
	}
}
func TestDaemonLifecycleAndInventoryUseCoreContracts(t *testing.T) {
	for _, action := range []string{"sleep", "wake", "workload_status", "workloads", "catalog_plugins"} {
		t.Run(action, func(t *testing.T) {
			expected := map[string]string{"sleep": "workloads.sleep", "wake": "workloads.wake", "workload_status": "workloads.status", "workloads": "workloads.inventory", "catalog_plugins": "workloads.plugins"}[action]
			base := daemonHandler(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/ops/"+expected && r.URL.Path != "/v1/ops/workloads.plugins" {
					t.Errorf("unexpected route %s", r.URL.Path)
				}
				if r.URL.Path == "/v1/ops/workloads.plugins" {
					fmt.Fprint(w, `{"plugins":[{"name":"coldsnap","plugin_id":"coldsnap","enabled":true,"loaded":true,"version":"1","lifecycle":true}]}`)
					return
				}
				var args map[string]any
				json.NewDecoder(r.Body).Decode(&args)
				if action != "workloads" && (args["recipe"] != "recipe" || args["workload_id"] != "work" || args["binding"] != nil) {
					t.Errorf("bad lifecycle arguments %v", args)
				}
				row := `{"workload_id":"work","cluster_name":"lab","recipe_revision":"rev","owner":"sparkroute","owned":true,"provider":"coldsnap","actions":["status","sleep","wake"],"state":"sleeping","observation":"recorded"}`
				if action == "workloads" {
					fmt.Fprintf(w, `{"workloads":[%s],"providers":[{"name":"coldsnap","version":1}]}`, row)
				} else {
					fmt.Fprint(w, row)
				}
			})
			c, _ := testDaemonClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/ops" {
					fmt.Fprintf(w, `[{"name":%q,"version":1},{"name":"workloads.plugins","version":1}]`, expected)
					return
				}
				base(w, r)
			}))
			switch action {
			case "workloads":
				value, err := c.InspectWorkloads(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				if len(value.Workloads) != 1 || !value.Workloads[0].Owned || len(value.Plugins) != 1 || !value.Plugins[0].Lifecycle {
					t.Fatalf("bad report %+v", value)
				}
			case "catalog_plugins":
				var value struct {
					Plugins []PluginAvailability `json:"plugins"`
				}
				if err := c.Catalog(context.Background(), action, nil, &value); err != nil {
					t.Fatal(err)
				}
				if len(value.Plugins) != 1 || !value.Plugins[0].Enabled {
					t.Fatal("lost plugin status")
				}
			default:
				var value WorkloadInfo
				if err := c.invokeDaemon(context.Background(), bridgeRequest{Operation: action, Binding: &Binding{Recipe: "recipe", RecipeRevision: "rev"}, WorkloadID: "work"}, &value); err != nil {
					t.Fatal(err)
				}
				if value.JobID != "work" || !value.Owned || value.LifecycleState != "sleeping" || len(value.PluginsInUse) != 1 {
					t.Fatalf("bad lifecycle %+v", value)
				}
			}
		})
	}
}
