// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Scitrera LLC
// Copyright 2026 Fox Engine Ltd.

package config

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

type RequestParameters = map[string]map[string]json.RawMessage

// RequestProfileImplementation belongs to one deployment. Shorthand recipe
// objects retain their historical ingress-API meaning.
type RequestProfileImplementation struct {
	IngressOverrides    RequestParameters `json:"ingress_overrides,omitempty"`
	UpstreamOverrides   RequestParameters `json:"upstream_overrides,omitempty"`
	SupportedOperations []string          `json:"supported_operations,omitempty"`
}

func (p *RequestProfileImplementation) UnmarshalJSON(raw []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return fmt.Errorf("profile must be an object")
	}
	expanded := false
	for _, key := range []string{"ingress_overrides", "upstream_overrides", "supported_operations"} {
		if _, ok := fields[key]; ok {
			expanded = true
		}
	}
	if expanded {
		for key, value := range fields {
			if string(value) == "null" {
				return fmt.Errorf("profile %s cannot be null", key)
			}
		}
		type plain RequestProfileImplementation
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		return dec.Decode((*plain)(p))
	}
	return json.Unmarshal(raw, &p.IngressOverrides)
}
func (p RequestProfileImplementation) Supports(operation string) bool {
	if len(p.SupportedOperations) > 0 {
		return slices.Contains(p.SupportedOperations, operation)
	}
	_, ok := p.IngressOverrides[operation]
	return ok
}

// Remove contains JSON pointers into the configured parameter object, per API.
// It suppresses inherited overrides; it does not delete caller request fields.
type RequestProfilePatch struct {
	IngressOverrides  RequestParameters   `json:"ingress_overrides,omitempty"`
	UpstreamOverrides RequestParameters   `json:"upstream_overrides,omitempty"`
	RemoveIngress     map[string][]string `json:"remove_ingress,omitempty"`
	RemoveUpstream    map[string][]string `json:"remove_upstream,omitempty"`
}
type RequestProfileBinding struct {
	Parent              string                         `json:"parent"`
	Selector            string                         `json:"selector"`
	Manual              bool                           `json:"manual,omitempty"`
	Patch               RequestProfilePatch            `json:"patch,omitempty"`
	DeploymentOverrides map[string]RequestProfilePatch `json:"deployment_overrides,omitempty"`
	ExcludedDeployments []string                       `json:"excluded_deployments,omitempty"`
}

// Source identity and profile revision are independent of workload identity.
// Profiles is refreshed by the gateway source owner; operator patches live on models.
type RequestProfileSource struct {
	Recipe           string                                  `json:"recipe"`
	RecipeRevision   string                                  `json:"recipe_revision"`
	Kind             string                                  `json:"kind,omitempty"`
	BaselineRevision string                                  `json:"baseline_revision,omitempty"`
	Revision         string                                  `json:"revision,omitempty"`
	Profiles         map[string]RequestProfileImplementation `json:"profiles,omitempty"`
	Error            string                                  `json:"error,omitempty"`
}

var ProfileSelector = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)

func ProfileDigest(profiles map[string]RequestProfileImplementation) string {
	raw, _ := json.Marshal(profiles)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func CloneProfileValue[T any](value T) T {
	raw, _ := json.Marshal(value)
	var clone T
	_ = json.Unmarshal(raw, &clone)
	return clone
}

func ValidateProfileImplementations(profiles map[string]RequestProfileImplementation) error {
	if len(profiles) > 64 {
		return fmt.Errorf("at most 64 request profiles allowed")
	}
	raw, err := json.Marshal(profiles)
	if err != nil || len(raw) > 256<<10 {
		return fmt.Errorf("request profiles must be valid JSON within 256 KiB")
	}
	for selector, p := range profiles {
		if !ProfileSelector.MatchString(selector) {
			return fmt.Errorf("invalid profile selector %q", selector)
		}
		if len(p.IngressOverrides) == 0 && len(p.SupportedOperations) == 0 {
			return fmt.Errorf("profile %s must declare supported_operations or ingress parameters", selector)
		}
		if err := ValidateProfileParameters(p.IngressOverrides); err != nil {
			return fmt.Errorf("profile %s ingress: %w", selector, err)
		}
		if err := ValidateProfileParameters(p.UpstreamOverrides); err != nil {
			return fmt.Errorf("profile %s upstream: %w", selector, err)
		}
		seen := map[string]bool{}
		for _, op := range p.SupportedOperations {
			if seen[op] {
				return fmt.Errorf("duplicate supported operation %q", op)
			}
			seen[op] = true
			if err := ValidateRequestOverrides(RequestParameters{op: nil}); err != nil {
				return err
			}
		}
	}
	return nil
}
func validateProfilePatch(p RequestProfilePatch) error {
	raw, err := json.Marshal(p)
	if err != nil || len(raw) > 256<<10 {
		return fmt.Errorf("profile patch exceeds 256 KiB")
	}
	for _, v := range []RequestParameters{p.IngressOverrides, p.UpstreamOverrides} {
		if err := ValidateProfileParameters(v); err != nil {
			return err
		}
	}
	for _, removes := range []map[string][]string{p.RemoveIngress, p.RemoveUpstream} {
		for op, paths := range removes {
			if err := ValidateRequestOverrides(RequestParameters{op: nil}); err != nil {
				return err
			}
			if len(paths) > 64 {
				return fmt.Errorf("too many suppressed parameter paths")
			}
			for _, path := range paths {
				if len(path) > 1024 || !strings.HasPrefix(path, "/") || path == "/" {
					return fmt.Errorf("invalid parameter pointer %q", path)
				}
			}
		}
	}
	return nil
}
func MergeProfileParameters(base, overlay map[string]json.RawMessage) map[string]json.RawMessage {
	result := CloneProfileValue(base)
	if result == nil {
		result = map[string]json.RawMessage{}
	}
	for k, v := range overlay {
		var a, b map[string]json.RawMessage
		if json.Unmarshal(result[k], &a) == nil && a != nil && json.Unmarshal(v, &b) == nil && b != nil {
			v, _ = json.Marshal(MergeProfileParameters(a, b))
		}
		result[k] = append(json.RawMessage(nil), v...)
	}
	return result
}
func removeProfileParameter(values map[string]json.RawMessage, path []string) {
	if len(path) == 0 {
		return
	}
	key := strings.ReplaceAll(strings.ReplaceAll(path[0], "~1", "/"), "~0", "~")
	if len(path) == 1 {
		delete(values, key)
		return
	}
	var nested map[string]json.RawMessage
	if json.Unmarshal(values[key], &nested) == nil && nested != nil {
		removeProfileParameter(nested, path[1:])
		if len(nested) == 0 {
			delete(values, key)
		} else {
			values[key], _ = json.Marshal(nested)
		}
	}
}
func patchProfileParameters(values map[string]json.RawMessage, set map[string]json.RawMessage, remove []string) map[string]json.RawMessage {
	result := MergeProfileParameters(values, set)
	for _, pointer := range remove {
		removeProfileParameter(result, strings.Split(strings.TrimPrefix(pointer, "/"), "/"))
	}
	return result
}

// EffectiveProfileParameters applies operator patches only to this deployment.
func (m VirtualModel) EffectiveProfileParameters(d Deployment, ingress, upstream string) (map[string]json.RawMessage, map[string]json.RawMessage, bool) {
	if m.Profile == nil {
		return nil, nil, false
	}
	b := m.Profile
	if slices.Contains(b.ExcludedDeployments, d.Name) {
		return nil, nil, false
	}
	impl, ok := d.RequestProfiles[b.Selector]
	if b.Manual {
		ok = true
		impl = RequestProfileImplementation{SupportedOperations: []string{ingress}}
	}
	if !ok || !impl.Supports(ingress) {
		return nil, nil, false
	}
	in := MergeProfileParameters(impl.IngressOverrides[ingress], m.RequestOverrides[ingress])
	out := CloneProfileValue(impl.UpstreamOverrides[upstream])
	for _, p := range []RequestProfilePatch{b.Patch, b.DeploymentOverrides[d.Name]} {
		in = patchProfileParameters(in, p.IngressOverrides[ingress], p.RemoveIngress[ingress])
		out = patchProfileParameters(out, p.UpstreamOverrides[upstream], p.RemoveUpstream[upstream])
	}
	return in, out, true
}

// ResolveRequestProfiles materializes inherited routing without modifying stored
// fragments. Explicit parent links also work across managed owners.
func (d Document) ResolveRequestProfiles() (Document, error) {
	result := d
	result.Deployments = slices.Clone(d.Deployments)
	result.VirtualModels = slices.Clone(d.VirtualModels)
	for i, dep := range result.Deployments {
		if len(dep.DiscoveryJobIDs) > 256 {
			return Document{}, fmt.Errorf("too many discovery job IDs")
		}
		if len(dep.DiscoveryJobIDs) > 0 && dep.EndpointSource.Type != EndpointSourceDiscovered {
			return Document{}, fmt.Errorf("discovery_job_ids requires a discovered deployment")
		}
		for _, job := range dep.DiscoveryJobIDs {
			if err := validateID(job); err != nil {
				return Document{}, fmt.Errorf("invalid discovery job ID: %w", err)
			}
		}
		if dep.ProfileSource != nil {
			source := dep.ProfileSource
			if dep.EndpointSource.Controller != "sparkrun" || dep.EndpointSource.Type != EndpointSourceActivatable || source.Recipe != dep.EndpointSource.Recipe || source.RecipeRevision != dep.EndpointSource.RecipeRevision {
				return Document{}, fmt.Errorf("profile_source must match the deployment recipe binding")
			}
			if source.Kind != "" && source.Kind != "recipe" && source.Kind != "snapshot" {
				return Document{}, fmt.Errorf("invalid profile source kind")
			}
		}
		if source, ok := d.RequestProfileSources[dep.Name]; ok && dep.ProfileSource != nil && source.Recipe == dep.ProfileSource.Recipe && source.RecipeRevision == dep.ProfileSource.RecipeRevision && source.BaselineRevision == dep.ProfileSource.Revision {
			dep.RequestProfiles = CloneProfileValue(source.Profiles)
			result.Deployments[i] = dep
		}
		if err := ValidateProfileImplementations(dep.RequestProfiles); err != nil {
			return Document{}, fmt.Errorf("deployment %s: %w", dep.Name, err)
		}
	}
	byName := map[string]VirtualModel{}
	for _, m := range d.VirtualModels {
		byName[m.Name] = m
	}
	for i, child := range d.VirtualModels {
		if child.Profile == nil {
			continue
		}
		b := child.Profile
		if err := ValidateProfileParameters(child.RequestOverrides); err != nil {
			return Document{}, err
		}
		parent, ok := byName[b.Parent]
		if !ok || parent.Profile != nil || child.Name == b.Parent {
			return Document{}, fmt.Errorf("profile %s requires a non-profile parent %q", child.Name, b.Parent)
		}
		if !ProfileSelector.MatchString(b.Selector) {
			return Document{}, fmt.Errorf("profile %s has invalid selector", child.Name)
		}
		if err := validateProfilePatch(b.Patch); err != nil {
			return Document{}, err
		}
		if len(b.DeploymentOverrides) > 256 {
			return Document{}, fmt.Errorf("too many deployment profile patches")
		}
		for _, patch := range b.DeploymentOverrides {
			if err := validateProfilePatch(patch); err != nil {
				return Document{}, err
			}
		}
		inherited := CloneProfileValue(parent)
		inherited.Name = child.Name
		inherited.Profile = CloneProfileValue(b)
		inherited.RequestOverrides = CloneProfileValue(parent.RequestOverrides)
		if inherited.RequestOverrides == nil {
			inherited.RequestOverrides = RequestParameters{}
		}
		for op, values := range child.RequestOverrides {
			inherited.RequestOverrides[op] = MergeProfileParameters(inherited.RequestOverrides[op], values)
		}
		inherited.Aliases = slices.Clone(child.Aliases)
		suffix := strings.TrimPrefix(child.Name, parent.Name+":")
		if suffix != child.Name {
			for _, alias := range parent.Aliases {
				derived := alias + ":" + suffix
				if !slices.Contains(inherited.Aliases, derived) {
					inherited.Aliases = append(inherited.Aliases, derived)
				}
			}
		}
		result.VirtualModels[i] = inherited
	}
	return result, nil
}

// State/transport fields must stay stable across candidate preparation and retries.
func ValidateProfileParameters(values RequestParameters) error {
	if err := ValidateRequestOverrides(values); err != nil {
		return err
	}
	for _, params := range values {
		for _, raw := range params {
			var value any
			if json.Unmarshal(raw, &value) != nil || !boundedProfileJSON(value, 4) {
				return fmt.Errorf("profile parameter JSON exceeds 32 nesting levels")
			}
		}
		if len(params) == 0 {
			return fmt.Errorf("profile parameter objects cannot be empty; use supported_operations for an intentional no-op")
		}
		for key := range params {
			switch strings.ToLower(key) {
			case "store", "background", "contents", "system", "system_instruction", "systeminstruction", "instructions", "history", "conversation", "previous_response_id":
				return fmt.Errorf("profile cannot override state or request structure field %s", key)
			}
		}
	}
	return nil
}

func boundedProfileJSON(value any, depth int) bool {
	if depth > 32 {
		return false
	}
	switch v := value.(type) {
	case map[string]any:
		for _, child := range v {
			if !boundedProfileJSON(child, depth+1) {
				return false
			}
		}
	case []any:
		for _, child := range v {
			if !boundedProfileJSON(child, depth+1) {
				return false
			}
		}
	}
	return true
}
