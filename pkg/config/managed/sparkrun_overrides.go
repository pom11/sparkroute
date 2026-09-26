// SPDX-FileCopyrightText: 2026 Scitrera LLC
// SPDX-FileCopyrightText: 2026 Fox Engine Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

package managed

import "github.com/sparksq/sparkroute/pkg/config"

// ApplySparkrunOverrides filters generated entities without mutating either
// owner fragment. Generated models lose excluded targets and disappear only if
// that removes their final target. Operator references are left for validation
// to report; exclusion never silently rewrites operator routing or policies.
func ApplySparkrunOverrides(operator, generated config.Document) (config.Document, error) {
	if err := operator.SparkrunOverrides.Validate(); err != nil {
		return config.Document{}, err
	}
	if operator.SparkrunOverrides == nil || len(operator.SparkrunOverrides.ExcludedDeployments) == 0 {
		return generated, nil
	}
	excluded := make(map[string]bool)
	for _, name := range operator.SparkrunOverrides.ExcludedDeployments {
		excluded[name] = true
	}
	// The historical discovery aggregate remains addressable for saved routes.
	// Excluding that aggregate still excludes the whole discovered model after
	// profile implementations split into job-specific targets.
	for _, aggregate := range generated.Deployments {
		if !excluded[aggregate.Name] || aggregate.EndpointSource.Type != config.EndpointSourceDiscovered || len(aggregate.DiscoveryJobIDs) != 0 {
			continue
		}
		for _, deployment := range generated.Deployments {
			if deployment.EndpointSource.Type == config.EndpointSourceDiscovered && deployment.Provider == aggregate.Provider && deployment.Model == aggregate.Model {
				excluded[deployment.Name] = true
			}
		}
	}
	result := generated
	result.Deployments = make([]config.Deployment, 0, len(generated.Deployments))
	for _, deployment := range generated.Deployments {
		if !excluded[deployment.Name] {
			result.Deployments = append(result.Deployments, deployment)
		}
	}
	result.VirtualModels = make([]config.VirtualModel, 0, len(generated.VirtualModels))
	for _, model := range generated.VirtualModels {
		changed := false
		pools := make([]config.RoutingPool, 0, len(model.Pools))
		for _, pool := range model.Pools {
			targets := make([]config.WeightedTarget, 0, len(pool.Targets))
			for _, target := range pool.Targets {
				if excluded[target.Deployment] {
					changed = true
				} else {
					targets = append(targets, target)
				}
			}
			// Preserve pre-existing invalid empty pools for normal validation.
			if len(targets) > 0 || len(pool.Targets) == 0 {
				pool.Targets = targets
				pools = append(pools, pool)
			}
		}
		if changed && len(pools) == 0 {
			continue
		}
		model.Pools = pools
		result.VirtualModels = append(result.VirtualModels, model)
	}
	parents := map[string]bool{}
	for _, model := range result.VirtualModels {
		parents[model.Name] = true
	}
	kept := result.VirtualModels[:0]
	for _, model := range result.VirtualModels {
		if model.Profile == nil || parents[model.Profile.Parent] {
			kept = append(kept, model)
		}
	}
	result.VirtualModels = kept
	return result, nil
}
