// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Scitrera LLC
// Copyright 2026 Fox Engine Ltd.
package sparkrun

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/sparksq/sparkroute/pkg/config"
	"github.com/sparksq/sparkroute/pkg/config/managed"
	configsqlite "github.com/sparksq/sparkroute/pkg/config/sqlite"
)

func TestRecipeProfileRefreshPreservesOperatorPatchesAndWorkload(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "config.sqlite")
	if err := os.Chmod(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	store, err := configsqlite.Open(ctx, configsqlite.Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	}()
	f := &catalogFixture{revision: "same", settings: RecipeSettings{RequestProfiles: profileImplementations(map[string]map[string]map[string]json.RawMessage{"low": {"chat_completions": {"temperature": json.RawMessage(`0.2`), "top_p": json.RawMessage(`0.8`)}}})}}
	doc, id, _, err := PrepareRecipeDraft(ctx, f, managed.EmptyDocument(), managed.EmptyDocument(), RecipeDraft{Reference: "catalog:123", RecipeRevision: "same", Name: "coding", Cluster: "lab"})
	if err != nil {
		t.Fatal(err)
	}
	doc.VirtualModels[1].Profile.Patch.IngressOverrides = config.RequestParameters{"chat_completions": {"temperature": json.RawMessage(`0.5`)}}
	if _, err = store.Initialize(ctx, doc, "test", ""); err != nil {
		t.Fatal(err)
	}
	before, _ := store.GetSet(ctx, managed.OwnerOperator)
	f.settings.RequestProfiles["low"].IngressOverrides["chat_completions"]["temperature"] = json.RawMessage(`0.3`)
	f.settings.RequestProfiles["low"].IngressOverrides["chat_completions"]["top_p"] = json.RawMessage(`0.9`)
	if err = RefreshRecipeProfiles(ctx, store, f); err != nil {
		t.Fatal(err)
	}
	after, _ := store.GetSet(ctx, managed.OwnerOperator)
	if before.Revision != after.Revision {
		t.Fatal("source refresh rewrote operator configuration")
	}
	loaded, _, err := store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	effective, err := loaded.ResolveRequestProfiles()
	if err != nil {
		t.Fatal(err)
	}
	in, _, ok := effective.VirtualModels[1].EffectiveProfileParameters(effective.Deployments[0], "chat_completions", "")
	if !ok || string(in["temperature"]) != "0.5" || string(in["top_p"]) != "0.9" || effective.Deployments[0].Name != id || effective.Deployments[0].EndpointSource.RecipeRevision != "same" {
		t.Fatalf("%s %+v", in, effective.Deployments[0])
	}
	f.revision = "different-runtime"
	f.settings.RequestProfiles["low"].IngressOverrides["chat_completions"]["top_p"] = json.RawMessage(`0.1`)
	if err = RefreshRecipeProfiles(ctx, store, f); err != nil {
		t.Fatal(err)
	}
	loaded, _, _ = store.Load(ctx)
	if loaded.RequestProfileSources[id].Error == "" {
		t.Fatal("runtime change was not reported")
	}
	effective, _ = loaded.ResolveRequestProfiles()
	if string(effective.Deployments[0].RequestProfiles["low"].IngressOverrides["chat_completions"]["top_p"]) != "0.9" {
		t.Fatal("applied mismatched runtime profiles")
	}
	_ = store.Close()
	store, err = configsqlite.Open(ctx, configsqlite.Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	}()
	loaded, _, err = store.Load(ctx)
	if err != nil || loaded.RequestProfileSources[id].Revision == "" {
		t.Fatal("source state did not survive restart", err)
	}
}

func TestProfileRefreshDeletionAndInvalidSource(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	store, err := configsqlite.Open(ctx, configsqlite.Options{Path: filepath.Join(root, "config.sqlite")})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	}()
	f := &catalogFixture{revision: "same", settings: RecipeSettings{RequestProfiles: profileImplementations(map[string]map[string]map[string]json.RawMessage{"low": {"chat_completions": {"temperature": json.RawMessage(`0.2`)}}})}}
	doc, id, _, err := PrepareRecipeDraft(ctx, f, managed.EmptyDocument(), managed.EmptyDocument(), RecipeDraft{Reference: "catalog:123", RecipeRevision: "same", Name: "coding", Cluster: "lab"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Initialize(ctx, doc, "test", ""); err != nil {
		t.Fatal(err)
	}
	if err = RefreshRecipeProfiles(ctx, store, f); err != nil {
		t.Fatal(err)
	}
	f.settings.RequestProfiles["low"].IngressOverrides["chat_completions"]["model"] = json.RawMessage(`"forbidden"`)
	if err = RefreshRecipeProfiles(ctx, store, f); err != nil {
		t.Fatal(err)
	}
	loaded, _, _ := store.Load(ctx)
	if loaded.RequestProfileSources[id].Error == "" || loaded.RequestProfileSources[id].Profiles["low"].IngressOverrides["chat_completions"]["model"] != nil {
		t.Fatal("invalid update replaced last known good profile")
	}
	f.settings.RequestProfiles = nil
	if err = RefreshRecipeProfiles(ctx, store, f); err != nil {
		t.Fatal(err)
	}
	loaded, _, _ = store.Load(ctx)
	effective, _ := loaded.ResolveRequestProfiles()
	if _, _, ok := effective.VirtualModels[1].EffectiveProfileParameters(effective.Deployments[0], "chat_completions", ""); ok {
		t.Fatal("deleted recipe profile remained active")
	}
	if len(loaded.VirtualModels) != 2 {
		t.Fatal("source deletion erased public exposure")
	}
}

// Interleave a real operator save with the source owner's first CAS write.
// Refresh must re-read the new baseline and retain the operator's patch.
type profileSaveConflictStore struct {
	managed.Store
	beforeReplace func()
}

func (s *profileSaveConflictStore) ReplaceSet(ctx context.Context, owner managed.Owner, doc config.Document, options managed.ReplaceOptions) (managed.ReplaceResult, error) {
	if owner == managed.OwnerRecipeProfiles && s.beforeReplace != nil {
		edit := s.beforeReplace
		s.beforeReplace = nil
		edit()
	}
	return s.Store.ReplaceSet(ctx, owner, doc, options)
}
func TestRecipeProfileRefreshRetriesConcurrentOperatorSave(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	store, err := configsqlite.Open(ctx, configsqlite.Options{Path: filepath.Join(root, "config.sqlite")})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	}()
	f := &catalogFixture{revision: "same", settings: RecipeSettings{RequestProfiles: profileImplementations(map[string]map[string]map[string]json.RawMessage{"low": {"chat_completions": {"temperature": json.RawMessage(`0.2`), "top_p": json.RawMessage(`0.8`)}}})}}
	doc, _, _, err := PrepareRecipeDraft(ctx, f, managed.EmptyDocument(), managed.EmptyDocument(), RecipeDraft{Reference: "catalog:123", RecipeRevision: "same", Name: "coding", Cluster: "lab"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Initialize(ctx, doc, "test", ""); err != nil {
		t.Fatal(err)
	}
	f.settings.RequestProfiles["low"].IngressOverrides["chat_completions"]["top_p"] = json.RawMessage(`0.9`)
	conflicting := &profileSaveConflictStore{Store: store, beforeReplace: func() {
		operator, err := store.GetSet(ctx, managed.OwnerOperator)
		if err != nil {
			t.Fatal(err)
		}
		_, revision, err := store.Load(ctx)
		if err != nil {
			t.Fatal(err)
		}
		operator.Document.VirtualModels[1].Profile.Patch.IngressOverrides = config.RequestParameters{"chat_completions": {"temperature": json.RawMessage(`0.7`)}}
		if _, err = store.ReplaceSet(ctx, managed.OwnerOperator, operator.Document, managed.ReplaceOptions{ExpectedActive: revision, Actor: "operator"}); err != nil {
			t.Fatal(err)
		}
	}}
	if err = RefreshRecipeProfiles(ctx, conflicting, f); err != nil {
		t.Fatal(err)
	}
	loaded, _, err := store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	effective, err := loaded.ResolveRequestProfiles()
	if err != nil {
		t.Fatal(err)
	}
	values, _, ok := effective.VirtualModels[1].EffectiveProfileParameters(effective.Deployments[0], "chat_completions", "")
	if !ok || string(values["temperature"]) != "0.7" || string(values["top_p"]) != "0.9" {
		t.Fatalf("concurrent save lost: %s", values)
	}
}
