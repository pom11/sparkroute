// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Scitrera LLC
// Copyright 2026 Fox Engine Ltd.
package sparkrun

import (
	"context"
	"errors"
	"fmt"

	"github.com/sparksq/sparkroute/pkg/config"
	"github.com/sparksq/sparkroute/pkg/config/managed"
)

// RefreshRecipeProfiles only replaces the source owner. CAS conflicts retry from
// current operator state; patches and presets are never rewritten by refresh.
func RefreshRecipeProfiles(ctx context.Context, store managed.Store, catalog Catalog) error {
	for attempt := 0; attempt < 3; attempt++ {
		document, revision, err := store.Load(ctx)
		if err != nil {
			return err
		}
		next := managed.EmptyDocument()
		next.RequestProfileSources = map[string]config.RequestProfileSource{}
		for _, deployment := range document.Deployments {
			if deployment.ProfileSource == nil {
				continue
			}
			source := *deployment.ProfileSource
			source.BaselineRevision = deployment.ProfileSource.Revision
			source.Profiles = config.CloneProfileValue(deployment.RequestProfiles)
			if previous, ok := document.RequestProfileSources[deployment.Name]; ok && previous.Recipe == source.Recipe && previous.RecipeRevision == source.RecipeRevision && previous.BaselineRevision == source.BaselineRevision {
				source = previous
			}
			details, resolveErr := resolveDetails(ctx, catalog, source.Recipe, deployment.EndpointSource.Overrides)
			switch {
			case resolveErr != nil:
				source.Error = "recipe profile source is unavailable or invalid"
			case details.Revision != source.RecipeRevision:
				source.Error = "recipe workload changed; update the deployment before applying its profiles"
			default:
				source.Profiles = config.CloneProfileValue(details.Sparkroute.RequestProfiles)
				source.Revision = config.ProfileDigest(source.Profiles)
				source.Kind = details.ProfileSourceKind
				source.Error = ""
			}
			next.RequestProfileSources[deployment.Name] = source
		}
		if len(next.RequestProfileSources) == 0 && len(document.RequestProfileSources) == 0 {
			return nil
		}
		_, err = store.ReplaceSet(ctx, managed.OwnerRecipeProfiles, next, managed.ReplaceOptions{ExpectedActive: revision, Actor: "sparkroute:recipe-profiles", Reason: "automatic recipe profile refresh"})
		if errors.Is(err, managed.ErrRevisionConflict) {
			continue
		}
		return err
	}
	return fmt.Errorf("recipe profile refresh: concurrent configuration updates; retry on next reconciliation")
}
