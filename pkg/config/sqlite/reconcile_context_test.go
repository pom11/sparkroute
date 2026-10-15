// SPDX-FileCopyrightText: 2026 Scitrera LLC
// SPDX-FileCopyrightText: 2026 Fox Engine Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/sparksq/sparkroute/pkg/config"
	"github.com/sparksq/sparkroute/pkg/config/managed"
)

func TestReconcileContextRetainsReferencesAndFencesOperatorChanges(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	operator := generatedDocument()
	operator.Providers[0].Name = "private-provider"
	operator.Providers[0].BaseURL = "http://private-host/v1"
	operator.Deployments[0].Name = "private-deployment"
	operator.Deployments[0].Provider = "private-provider"
	operator.VirtualModels[0].Name = "deepseek"
	operator.VirtualModels[0].Aliases = []string{"ds4f"}
	operator.VirtualModels[0].Pools[0].Targets[0].Deployment = "private-deployment"
	if _, err := store.Initialize(ctx, operator, "test", ""); err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.ReconcileContext(ctx, managed.OwnerSparkrun)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.ReservedNames) != 2 || snapshot.ReservedNames[0].Name != "deepseek" || snapshot.ReservedNames[1].Kind != "alias" {
		t.Fatalf("claims: %#v", snapshot)
	}
	raw, _ := json.Marshal(snapshot)
	if strings.Contains(string(raw), "private-") {
		t.Fatalf("operator content leaked: %s", raw)
	}
	generated := generatedDocument()
	generated.VirtualModels = nil
	// Deployments without public routes are accepted, and strict duplicate
	// name checks still reject a conflicting wrapper.
	written, err := store.ReplaceSet(ctx, managed.OwnerSparkrun, generated, managed.ReplaceOptions{ExpectedActive: snapshot.ActiveRevision, Actor: "test"})
	if err != nil {
		t.Fatal(err)
	}
	collision := generatedDocument()
	collision.VirtualModels[0].Name = "deepseek"
	if _, err := store.ReplaceSet(ctx, managed.OwnerSparkrun, collision, managed.ReplaceOptions{ExpectedActive: written.Current.Version, Actor: "test"}); !errors.Is(err, managed.ErrInvalidConfiguration) {
		t.Fatalf("duplicate accepted: %v", err)
	}
	operator.VirtualModels[0].Pools[0].Targets = []config.WeightedTarget{{Deployment: "generated", Weight: 1}}
	if _, err := store.ReplaceSet(ctx, managed.OwnerOperator, operator, managed.ReplaceOptions{ExpectedActive: written.Current.Version, Actor: "operator"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReplaceSet(ctx, managed.OwnerSparkrun, generated, managed.ReplaceOptions{ExpectedActive: snapshot.ActiveRevision, Actor: "test"}); !errors.Is(err, managed.ErrRevisionConflict) {
		t.Fatalf("stale context accepted: %v", err)
	}
	corrected, err := store.ReconcileContext(ctx, managed.OwnerSparkrun)
	if err != nil || len(corrected.ReferencedDeployments) != 1 || corrected.ReferencedDeployments[0] != "generated" {
		t.Fatalf("references: %#v %v", corrected, err)
	}
	if len(corrected.Document.Deployments) != 1 {
		t.Fatal("own document missing")
	}
}

func TestReconcileContextIncludesInheritedAliasesAndGeneratedParentReferences(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	if _, err := store.Initialize(ctx, managed.EmptyDocument(), "test", ""); err != nil {
		t.Fatal(err)
	}
	_, revision, _ := store.Load(ctx)
	generated := generatedDocument()
	generated.VirtualModels[0].Aliases = []string{"shortcut"}
	written, err := store.ReplaceSet(ctx, managed.OwnerSparkrun, generated, managed.ReplaceOptions{ExpectedActive: revision, Actor: "test"})
	if err != nil {
		t.Fatal(err)
	}
	operator := managed.EmptyDocument()
	operator.VirtualModels = []config.VirtualModel{{Name: "generated:low", Profile: &config.RequestProfileBinding{Parent: "generated", Selector: "low"}}}
	if _, err := store.ReplaceSet(ctx, managed.OwnerOperator, operator, managed.ReplaceOptions{ExpectedActive: written.Current.Version, Actor: "test"}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.ReconcileContext(ctx, managed.OwnerSparkrun)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.ReservedNames) != 2 || snapshot.ReservedNames[1].Name != "shortcut:low" {
		t.Fatalf("derived alias missing: %#v", snapshot)
	}
	if len(snapshot.ReferencedModels) != 1 || snapshot.ReferencedModels[0] != "generated" || len(snapshot.ReferencedDeployments) != 1 {
		t.Fatalf("parent references missing: %#v", snapshot)
	}
}
