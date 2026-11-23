// Package application contains the approval use cases. RF-018: configure the approval workflow
// of a product and policy scope as numbered, immutable versions of sequential steps. The
// approval request and its actions (RF-019) pin one of these versions.
package application

import (
	"context"
	"errors"

	"goravel/app/modules/approval/domain"
	"goravel/app/platform/tx"
)

var (
	ErrWorkflowNotFound = errors.New("approval workflow not found")
	ErrVersionNotFound  = errors.New("approval workflow version not found")
	// ErrWorkflowExists: the product and policy scope already have a workflow; add a version to it.
	ErrWorkflowExists = errors.New("an approval workflow already exists for this product and policy scope")
	// ErrProductNotFound comes from the catalog lookup.
	ErrProductNotFound = errors.New("product not found")
)

const (
	DefaultPerPage = 20
	MaxPerPage     = 100
	MaxPage        = 10000
)

// ProductRef is what the approval module needs to know about a catalog product.
type ProductRef struct {
	ID     string
	Code   string
	Active bool
}

// ProductLookup resolves a product code through the catalog module.
type ProductLookup interface {
	// Lookup returns ErrProductNotFound when the code is unknown.
	Lookup(ctx context.Context, code string) (ProductRef, error)
}

// Summary is a workflow with its newest version, for lists.
type Summary struct {
	domain.Definition
	VersionCount   int
	CurrentVersion domain.Version
}

// ListFilter selects a page of workflows ordered by product code and policy scope.
type ListFilter struct {
	ProductCode string
	Page        int
	PerPage     int
}

// Page is one page of workflows and the total number of matches.
type Page struct {
	Items   []Summary
	Total   int64
	Page    int
	PerPage int
}

// Repository persists workflows. Adding a version must hold FindDefinition(forUpdate) in the
// same transaction, which serializes numbering per workflow.
type Repository interface {
	// InsertDefinition stores a workflow or returns ErrWorkflowExists.
	InsertDefinition(ctx context.Context, product ProductRef, policyScope, name, actorID string) (domain.Definition, error)
	// FindDefinition returns ErrWorkflowNotFound when absent. forUpdate locks the row.
	FindDefinition(ctx context.Context, id string, forUpdate bool) (domain.Definition, error)
	ListDefinitions(ctx context.Context, f ListFilter) ([]Summary, int64, error)
	// InsertVersion stores the next numbered version of a workflow with its steps.
	InsertVersion(ctx context.Context, definitionID, changeNote string, steps []domain.Step, actorID string) (domain.Version, error)
	// ListVersions returns the versions of a workflow with their steps, newest first.
	ListVersions(ctx context.Context, definitionID string) ([]domain.Version, error)
	// FindVersion returns ErrVersionNotFound when absent.
	FindVersion(ctx context.Context, id string) (domain.Version, error)
	// FindCurrentVersion returns the newest version of the workflow of a product and policy
	// scope, or ErrWorkflowNotFound.
	FindCurrentVersion(ctx context.Context, productCode, policyScope string) (domain.Version, error)
}

// WorkflowService configures approval workflows.
type WorkflowService struct {
	repo     Repository
	products ProductLookup
	vocab    domain.RoleVocabulary
	tx       tx.Manager
}

func NewWorkflowService(repo Repository, products ProductLookup, vocab domain.RoleVocabulary, txm tx.Manager) *WorkflowService {
	return &WorkflowService{repo: repo, products: products, vocab: vocab, tx: txm}
}

// Create opens the workflow of a product and policy scope with its first version, in one
// transaction. The product must exist and be active.
func (s *WorkflowService) Create(ctx context.Context, actorID string, in domain.NewWorkflowInput) (domain.Definition, domain.Version, error) {
	req, err := domain.ValidateNewWorkflow(in, s.vocab)
	if err != nil {
		return domain.Definition{}, domain.Version{}, err
	}
	var (
		def domain.Definition
		ver domain.Version
	)
	err = s.tx.WithinTransaction(ctx, func(ctx context.Context) error {
		product, err := s.products.Lookup(ctx, req.ProductCode)
		if errors.Is(err, ErrProductNotFound) {
			return &domain.ValidationError{Fields: []domain.FieldError{{Field: "product_code", Issue: "unknown_product"}}}
		}
		if err != nil {
			return err
		}
		if !product.Active {
			return &domain.ValidationError{Fields: []domain.FieldError{{Field: "product_code", Issue: "product_inactive"}}}
		}
		if def, err = s.repo.InsertDefinition(ctx, product, req.PolicyScope, req.Name, actorID); err != nil {
			return err
		}
		ver, err = s.repo.InsertVersion(ctx, def.ID, req.ChangeNote, req.Steps, actorID)
		return err
	})
	if err != nil {
		return domain.Definition{}, domain.Version{}, err
	}
	return def, ver, nil
}

// AddVersion stores a new configuration as the next version of a workflow and returns it with
// created true. When the steps equal those of the current version nothing is stored and the
// current version is returned with created false. Earlier versions, and the requests that pin
// them, are not affected.
func (s *WorkflowService) AddVersion(ctx context.Context, actorID, workflowID string, in domain.NewVersionInput) (domain.Version, bool, error) {
	if !domain.IsValidID(workflowID) {
		return domain.Version{}, false, ErrWorkflowNotFound
	}
	req, err := domain.ValidateNewVersion(in, s.vocab)
	if err != nil {
		return domain.Version{}, false, err
	}
	var (
		out     domain.Version
		created bool
	)
	err = s.tx.WithinTransaction(ctx, func(ctx context.Context) error {
		def, err := s.repo.FindDefinition(ctx, workflowID, true)
		if err != nil {
			return err
		}
		versions, err := s.repo.ListVersions(ctx, def.ID)
		if err != nil {
			return err
		}
		if len(versions) > 0 && domain.SameSteps(versions[0].Steps, req.Steps) {
			out = versions[0]
			return nil
		}
		out, err = s.repo.InsertVersion(ctx, def.ID, req.ChangeNote, req.Steps, actorID)
		created = err == nil
		return err
	})
	if err != nil {
		return domain.Version{}, false, err
	}
	return out, created, nil
}

// Get returns a workflow with all of its versions, newest first.
func (s *WorkflowService) Get(ctx context.Context, id string) (domain.Definition, []domain.Version, error) {
	if !domain.IsValidID(id) {
		return domain.Definition{}, nil, ErrWorkflowNotFound
	}
	def, err := s.repo.FindDefinition(ctx, id, false)
	if err != nil {
		return domain.Definition{}, nil, err
	}
	versions, err := s.repo.ListVersions(ctx, def.ID)
	return def, versions, err
}

// GetVersion returns one version with its steps.
func (s *WorkflowService) GetVersion(ctx context.Context, id string) (domain.Version, error) {
	if !domain.IsValidID(id) {
		return domain.Version{}, ErrVersionNotFound
	}
	return s.repo.FindVersion(ctx, id)
}

// CurrentVersion returns the version a submission of a policy of this product and scope uses:
// the newest version of its workflow. It returns ErrWorkflowNotFound when none is configured.
// The approval request (RF-019) stores the id it got, so later versions do not change it.
func (s *WorkflowService) CurrentVersion(ctx context.Context, productCode, policyScope string) (domain.Version, error) {
	return s.repo.FindCurrentVersion(ctx, productCode, policyScope)
}

// List returns a page of workflows. Out-of-range paging values are clamped.
func (s *WorkflowService) List(ctx context.Context, f ListFilter) (Page, error) {
	if f.Page < 1 {
		f.Page = 1
	}
	if f.Page > MaxPage {
		f.Page = MaxPage
	}
	if f.PerPage < 1 {
		f.PerPage = DefaultPerPage
	}
	if f.PerPage > MaxPerPage {
		f.PerPage = MaxPerPage
	}
	items, total, err := s.repo.ListDefinitions(ctx, f)
	if err != nil {
		return Page{}, err
	}
	return Page{Items: items, Total: total, Page: f.Page, PerPage: f.PerPage}, nil
}
