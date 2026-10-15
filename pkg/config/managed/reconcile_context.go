// SPDX-FileCopyrightText: 2026 Scitrera LLC
// SPDX-FileCopyrightText: 2026 Fox Engine Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

package managed

import (
	"context"
	"sort"

	"github.com/sparksq/sparkroute/pkg/config"
)

// ReconcileContext exposes only public namespace claims and references into the
// caller's own set. Other owners' credentials, recipes and policies stay private.
// All fields belong to ActiveRevision; a writer must use it for ReplaceSet CAS.
type ReconcileContext struct {
	ActiveRevision        config.Version  `json:"active_revision"`
	Document              config.Document `json:"document"`
	ReservedNames         []NameClaim     `json:"reserved_names"`
	ReferencedDeployments []string        `json:"referenced_deployments"`
	ReferencedModels      []string        `json:"referenced_models"`
}

type NameClaim struct {
	Name  string `json:"name"`
	Owner Owner  `json:"owner"`
	Model string `json:"model"`
	Kind  string `json:"kind"`
}

type ReconcileContextStore interface {
	ReconcileContext(context.Context, Owner) (ReconcileContext, error)
}

func BuildReconcileContext(sets map[Owner]config.Document, owner Owner, revision config.Version) (ReconcileContext, error) {
	result := ReconcileContext{ActiveRevision: revision, Document: sets[owner], ReservedNames: []NameClaim{}, ReferencedDeployments: []string{}, ReferencedModels: []string{}}
	merged, err := Merge(sets)
	if err != nil {
		return ReconcileContext{}, err
	}
	effective, err := merged.ResolveRequestProfiles()
	if err != nil {
		return ReconcileContext{}, err
	}
	effectiveModels := map[string]config.VirtualModel{}
	modelOwners := map[string]Owner{}
	ownNames := map[string]string{}
	ownDeployments := map[string]bool{}
	for _, deployment := range sets[owner].Deployments {
		ownDeployments[deployment.Name] = true
	}
	for other, document := range sets {
		for _, model := range document.VirtualModels {
			modelOwners[model.Name] = other
		}
	}
	for _, model := range effective.VirtualModels {
		effectiveModels[model.Name] = model
		if modelOwners[model.Name] == owner {
			ownNames[model.Name] = model.Name
			for _, alias := range model.Aliases {
				ownNames[alias] = model.Name
			}
		}
	}
	references := map[string]bool{}
	referencedModels := map[string]bool{}
	addTargets := func(model config.VirtualModel) {
		for _, pool := range model.Pools {
			for _, target := range pool.Targets {
				if ownDeployments[target.Deployment] {
					references[target.Deployment] = true
				}
			}
		}
	}
	for _, model := range effective.VirtualModels {
		other := modelOwners[model.Name]
		if other == owner {
			continue
		}
		result.ReservedNames = append(result.ReservedNames, NameClaim{Name: model.Name, Owner: other, Model: model.Name, Kind: "model"})
		for _, alias := range model.Aliases {
			result.ReservedNames = append(result.ReservedNames, NameClaim{Name: alias, Owner: other, Model: model.Name, Kind: "alias"})
		}
		addTargets(model)
		if model.Profile != nil && ownNames[model.Profile.Parent] != "" {
			referencedModels[ownNames[model.Profile.Parent]] = true
		}
		for _, phase := range [][]config.Guardrail{model.Guardrails.Pre, model.Guardrails.Post} {
			for _, guardrail := range phase {
				if name := ownNames[guardrail.Model]; name != "" {
					referencedModels[name] = true
				}
			}
		}
	}
	// Retain a generated model referenced by an operator profile/guardrail,
	// together with its parent and deployments, without copying that policy.
	for name := range referencedModels {
		model := effectiveModels[name]
		if model.Profile != nil && ownNames[model.Profile.Parent] != "" {
			referencedModels[ownNames[model.Profile.Parent]] = true
		}
		addTargets(model)
	}
	for name := range referencedModels {
		result.ReferencedModels = append(result.ReferencedModels, name)
	}
	for name := range references {
		result.ReferencedDeployments = append(result.ReferencedDeployments, name)
	}
	sort.Strings(result.ReferencedModels)
	sort.Strings(result.ReferencedDeployments)
	sort.Slice(result.ReservedNames, func(i, j int) bool { return result.ReservedNames[i].Name < result.ReservedNames[j].Name })
	return result, nil
}
