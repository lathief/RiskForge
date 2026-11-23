// Package infrastructure adapts the simulator to the policy module and to the evaluation core
// of the Decision API.
package infrastructure

import (
	"context"
	"errors"

	decision "goravel/app/modules/decision/application"
	decisioninfra "goravel/app/modules/decision/infrastructure"
	policyapp "goravel/app/modules/policy/application"
	"goravel/app/modules/simulation/application"
)

// DraftLoader is the part of the policy use cases the source needs.
type DraftLoader interface {
	LoadForSimulation(ctx context.Context, id string) (policyapp.Draft, error)
}

var _ DraftLoader = (*policyapp.PolicyService)(nil)

// PolicySource reads a version from the policy module and compiles it with the evaluation core
// the Decision API uses (decisioninfra.NewEvaluablePolicy), so a simulation and a decision
// cannot disagree about the same content. It does not use the policy cache.
type PolicySource struct {
	policies DraftLoader
}

var _ application.Source = (*PolicySource)(nil)

func NewPolicySource(policies DraftLoader) *PolicySource { return &PolicySource{policies: policies} }

func (s *PolicySource) Prepare(ctx context.Context, versionID string) (application.Prepared, error) {
	d, err := s.policies.LoadForSimulation(ctx, versionID)
	if errors.Is(err, policyapp.ErrVersionNotFound) {
		return application.Prepared{}, application.ErrVersionNotFound
	}
	if err != nil {
		return application.Prepared{}, err
	}
	v := d.Version
	policy, err := decisioninfra.NewEvaluablePolicy(decision.PolicyRef{
		VersionID: v.ID, VersionNumber: v.Number, Checksum: v.Checksum, ProductCode: v.ProductCode, Scope: v.Scope,
	}, d.Content, d.Dictionary)
	if err != nil {
		return application.Prepared{}, err
	}
	return application.Prepared{
		Target: application.Target{
			VersionID: v.ID, VersionNumber: v.Number, Status: string(v.Status), ProductCode: v.ProductCode,
			Scope: v.Scope, Checksum: v.Checksum,
		},
		Policy: policy,
	}, nil
}
