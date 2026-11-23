package application

import (
	"context"

	"goravel/app/modules/policy/domain"
)

// Draft is a version with the content and the dictionary it is evaluated against. The
// simulator (RF-017) reads it to run a version that has no frozen snapshot yet.
type Draft struct {
	Version    domain.Version
	Content    domain.Content
	Dictionary []domain.DictionaryVariable
}

// LoadForSimulation reads the content of any version, draft or frozen, and the dictionary
// version it is pinned to. It only reads: nothing is locked, written, or frozen, and the active
// binding is not consulted. For a draft the content is what the analyst has saved so far, so it
// may be incomplete; the caller compiles it and reports what is missing.
func (s *PolicyService) LoadForSimulation(ctx context.Context, id string) (Draft, error) {
	if !domain.IsValidID(id) {
		return Draft{}, ErrVersionNotFound
	}
	v, err := s.repo.FindVersion(ctx, id, false)
	if err != nil {
		return Draft{}, err
	}
	content, dictionary, err := s.loadContent(ctx, v)
	if err != nil {
		return Draft{}, err
	}
	return Draft{Version: v, Content: content, Dictionary: dictionary}, nil
}
