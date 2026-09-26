// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Scitrera LLC
// Copyright 2026 Fox Engine Ltd.
package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sparksq/sparkroute/pkg/config"
	"github.com/sparksq/sparkroute/pkg/config/managed"
	configsqlite "github.com/sparksq/sparkroute/pkg/config/sqlite"
	"github.com/sparksq/sparkroute/pkg/sparkrun"
	"github.com/sparksq/sparkroute/pkg/telemetry/otlpexport"
)

func TestAutomaticRecipeProfileRefreshReachesNextRequestWithoutRestart(t *testing.T) {
	received := make(chan map[string]json.RawMessage, 2)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		received <- body
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"profile-test","model":"cold-upstream","choices":[]}`)
	}))
	defer upstream.Close()
	profiles := map[string]config.RequestProfileImplementation{"low": {IngressOverrides: config.RequestParameters{"chat_completions": {"temperature": json.RawMessage(`0.2`), "chat_template_kwargs": json.RawMessage(`{"recipe_value":1,"customized":1}`)}}}}
	path := newAcceptanceBridgeFixture(t, upstream.URL, acceptanceBridgeState{ColdActive: true, Profiles: profiles})
	document, _ := acceptanceRoutingDocument(false, 32, 1<<20, 5*time.Second, 0)
	document.ModelRouting = nil
	document.Deployments[0].RequestProfiles = config.CloneProfileValue(profiles)
	document.Deployments[0].ProfileSource = &config.RequestProfileSource{Recipe: "cold-recipe", RecipeRevision: "coldrev00001", Kind: "recipe", Revision: config.ProfileDigest(profiles)}
	document.VirtualModels = append(document.VirtualModels, config.VirtualModel{Name: "cold:low", Profile: &config.RequestProfileBinding{Parent: "cold", Selector: "low", Patch: config.RequestProfilePatch{IngressOverrides: config.RequestParameters{"chat_completions": {"chat_template_kwargs": json.RawMessage(`{"customized":9}`)}}}}})
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store, err := configsqlite.Open(ctx, configsqlite.Options{Path: filepath.Join(root, "config.sqlite")})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	}()
	if _, err = store.Initialize(ctx, document, "test", ""); err != nil {
		t.Fatal(err)
	}
	document, revision, err := store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	operatorBefore, err := store.GetSet(ctx, managed.OwnerOperator)
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	options := runtimeBuildOptions{Context: ctx, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Telemetry: &otlpexport.Runtime{}, ManagedConfig: store, SparkrunEnabled: true, SparkrunCommand: executable, SparkrunEndpointTTL: 10 * time.Second, SparkrunReconcile: time.Second, SparkrunProfileRefresh: 25 * time.Millisecond, SparkrunStopTimeout: time.Second, SparkrunWorkloads: sparkrun.NewWorkloads()}
	initial, err := buildRuntimeGeneration(document, revision, options)
	if err != nil {
		t.Fatal(err)
	}
	slot := newRuntimeSlot(initial)
	defer slot.Close()
	done := make(chan struct{})
	go func() { defer close(done); reconcileManagedConfiguration(ctx, store, slot, options, options.Logger) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("reconciler did not stop")
		}
	}()
	waitApplied := func(want string) {
		waitAcceptance(t, 10*time.Second, func() bool {
			current, rev, err := store.Load(ctx)
			if err != nil {
				return false
			}
			source := current.RequestProfileSources["cold-deployment"]
			generation := slot.current.Load()
			return source.Revision == want && generation != nil && generation.revision == rev
		}, "automatic profile refresh applied")
	}
	request := func() map[string]json.RawMessage {
		response := serveAcceptanceJSON(t, slot.dataHandler(), "/v1/chat/completions", `{"model":"cold:low","messages":[],"temperature":1}`)
		if response.Code != 200 {
			t.Fatalf("%d %s", response.Code, response.Body)
		}
		return <-received
	}
	waitApplied(config.ProfileDigest(profiles))
	before := request()
	if string(before["temperature"]) != "0.2" {
		t.Fatalf("initial profile: %s", before)
	}
	bindingBefore := document.Deployments[0].EndpointSource
	callsBefore := mustAcceptanceBridgeState(t, path)
	profiles["low"].IngressOverrides["chat_completions"]["temperature"] = json.RawMessage(`0.4`)
	profiles["low"].IngressOverrides["chat_completions"]["chat_template_kwargs"] = json.RawMessage(`{"recipe_value":2,"customized":3,"added":true}`)
	if _, err = updateAcceptanceBridgeState(path, func(state *acceptanceBridgeState) error { state.Profiles = profiles; return nil }); err != nil {
		t.Fatal(err)
	}
	waitApplied(config.ProfileDigest(profiles))
	after := request()
	var nested map[string]json.RawMessage
	if err = json.Unmarshal(after["chat_template_kwargs"], &nested); err != nil {
		t.Fatal(err)
	}
	if string(after["temperature"]) != "0.4" || string(nested["recipe_value"]) != "2" || string(nested["customized"]) != "9" || string(nested["added"]) != "true" {
		t.Fatalf("refresh/patch not applied: %s", after)
	}
	operatorAfter, err := store.GetSet(ctx, managed.OwnerOperator)
	if err != nil {
		t.Fatal(err)
	}
	latest, _, err := store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	callsAfter := mustAcceptanceBridgeState(t, path)
	if operatorAfter.Revision != operatorBefore.Revision || latest.Deployments[0].EndpointSource.Revision != bindingBefore.Revision || callsAfter.LaunchCalls != callsBefore.LaunchCalls || callsAfter.StopCalls != callsBefore.StopCalls {
		t.Fatalf("profile-only refresh altered state: operator %s -> %s; binding %s -> %s; launch %d -> %d; stop %d -> %d", operatorBefore.Revision, operatorAfter.Revision, bindingBefore.Revision, latest.Deployments[0].EndpointSource.Revision, callsBefore.LaunchCalls, callsAfter.LaunchCalls, callsBefore.StopCalls, callsAfter.StopCalls)
	}
}
