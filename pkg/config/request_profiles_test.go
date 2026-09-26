// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Scitrera LLC
// Copyright 2026 Fox Engine Ltd.
package config

import (
	"encoding/json"
	"os"
	"testing"
)

func TestProfilePatchInheritsNestedUpdatesAndDistinguishesNull(t *testing.T) {
	deployment := Deployment{Name: "one", RequestProfiles: map[string]RequestProfileImplementation{"low": {IngressOverrides: RequestParameters{"chat_completions": {"temperature": json.RawMessage(`0.2`), "nested": json.RawMessage(`{"keep":1,"change":2}`), "suppress": json.RawMessage(`true`)}}}}}
	model := VirtualModel{Profile: &RequestProfileBinding{Selector: "low", Patch: RequestProfilePatch{IngressOverrides: RequestParameters{"chat_completions": {"nested": json.RawMessage(`{"change":9}`), "temperature": json.RawMessage(`null`)}}, RemoveIngress: map[string][]string{"chat_completions": {"/suppress"}}}}}
	in, _, ok := model.EffectiveProfileParameters(deployment, "chat_completions", "")
	if !ok || string(in["temperature"]) != "null" || string(in["nested"]) != `{"change":9,"keep":1}` || in["suppress"] != nil {
		t.Fatalf("%s", in)
	}
	deployment.RequestProfiles["low"].IngressOverrides["chat_completions"]["nested"] = json.RawMessage(`{"keep":3,"change":4,"added":5}`)
	in, _, _ = model.EffectiveProfileParameters(deployment, "chat_completions", "")
	if string(in["nested"]) != `{"added":5,"change":9,"keep":3}` {
		t.Fatal(string(in["nested"]))
	}
	model.Profile.Patch = RequestProfilePatch{}
	in, _, _ = model.EffectiveProfileParameters(deployment, "chat_completions", "")
	if string(in["temperature"]) != "0.2" {
		t.Fatal("reset did not inherit")
	}
}
func TestProfileParentChangesPropagateWithoutMutatingStoredChild(t *testing.T) {
	doc := Document{VirtualModels: []VirtualModel{{Name: "coding", Aliases: []string{"code"}, Pools: []RoutingPool{{Priority: 4, Targets: []WeightedTarget{{Deployment: "one", Weight: 7}}}}}, {Name: "coding:low", Profile: &RequestProfileBinding{Parent: "coding", Selector: "low"}}}}
	resolved, err := doc.ResolveRequestProfiles()
	if err != nil {
		t.Fatal(err)
	}
	if resolved.VirtualModels[1].Pools[0].Targets[0].Weight != 7 || resolved.VirtualModels[1].Aliases[0] != "code:low" || len(doc.VirtualModels[1].Pools) != 0 {
		t.Fatal(resolved)
	}
	doc.VirtualModels[0].Pools[0].Targets[0].Weight = 42
	again, _ := doc.ResolveRequestProfiles()
	if again.VirtualModels[1].Pools[0].Targets[0].Weight != 42 || resolved.VirtualModels[1].Pools[0].Targets[0].Weight != 7 {
		t.Fatal("inheritance or snapshot isolation failed")
	}
}

func TestSharedRecipeProfileContract(t *testing.T) {
	raw, err := os.ReadFile("testdata/request_profiles.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []struct {
		Name     string
		Valid    bool
		Profiles json.RawMessage
	}
	if err = json.Unmarshal(raw, &fixtures); err != nil {
		t.Fatal(err)
	}
	for _, fixture := range fixtures {
		t.Run(fixture.Name, func(t *testing.T) {
			var profiles map[string]RequestProfileImplementation
			err := json.Unmarshal(fixture.Profiles, &profiles)
			if err == nil {
				err = ValidateProfileImplementations(profiles)
			}
			if (err == nil) != fixture.Valid {
				t.Fatalf("valid=%v error=%v", fixture.Valid, err)
			}
		})
	}
}

func TestManualProfileDoesNotUseRecipeParameters(t *testing.T) {
	d := Deployment{Name: "d", RequestProfiles: map[string]RequestProfileImplementation{"low": {IngressOverrides: RequestParameters{"chat_completions": {"temperature": json.RawMessage(`0.2`)}}}}}
	m := VirtualModel{Profile: &RequestProfileBinding{Selector: "low", Manual: true}}
	in, _, ok := m.EffectiveProfileParameters(d, "chat_completions", "")
	if !ok || len(in) != 0 {
		t.Fatalf("manual profile inherited recipe: %s", in)
	}
}

func TestInheritedProfilePolicyAssignmentUsesExplicitParent(t *testing.T) {
	doc := Document{PIIProfiles: map[string]PIIPolicy{"privacy": {Mode: PIIModeDisabled}}, ModelPolicies: map[string]ModelPolicyAssignment{}, VirtualModels: []VirtualModel{{Name: "coding"}, {Name: "custom-public-profile", Profile: &RequestProfileBinding{Parent: "coding", Selector: "low"}}}}
	ref := "privacy"
	doc.ModelPolicies["coding"] = ModelPolicyAssignment{PIIProfile: &ref}
	resolved, err := doc.ResolveModelPolicies()
	if err != nil {
		t.Fatal(err)
	}
	if resolved.VirtualModels[1].Privacy == nil {
		t.Fatal("explicit profile did not inherit parent policy")
	}
}

func TestExplicitAndLegacyProfileParentsCannotFormPolicyCycle(t *testing.T) {
	document := Document{VirtualModels: []VirtualModel{
		{Name: "coding", Profile: &RequestProfileBinding{Parent: "coding:low", Selector: "low"}},
		{Name: "coding:low", RequestOverrides: RequestParameters{"chat_completions": {"temperature": json.RawMessage(`0.2`)}}},
	}}
	if _, err := document.ResolveModelPolicies(); err == nil {
		t.Fatal("accepted a cycle between explicit and legacy parent links")
	}
}
