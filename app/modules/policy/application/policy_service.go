// Package application contains the policy package and version use cases (RF-006).
package application

import (
	"context"
	"errors"

	"goravel/app/modules/policy/domain"
	"goravel/app/platform/tx"
)

var (
	ErrPolicyNotFound  = errors.New("policy not found")
	ErrVersionNotFound = errors.New("policy version not found")
	// ErrPolicyExists means the product and scope already have a package; new drafts come from cloning.
	ErrPolicyExists = errors.New("a policy already exists for this product and scope")
	// ErrVersionFrozen means a change or submit was attempted on a version that is not a DRAFT.
	ErrVersionFrozen = errors.New("policy version is not a draft")
	// ErrProductNotFound and ErrProductInactive come from the catalog lookup.
	ErrProductNotFound = errors.New("product not found")
	ErrProductInactive = errors.New("product is not active")
	// ErrSourceNotInPolicy means a clone source version belongs to another policy.
	ErrSourceNotInPolicy = errors.New("source version does not belong to this policy")
)

const (
	DefaultPerPage = 20
	MaxPerPage     = 100
	MaxPage        = 10000
)

// ProductRef is what the policy module needs to know about a catalog product.
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

// DictionaryReader reads the data dictionary through the dictionary module.
type DictionaryReader interface {
	CurrentSchemaVersion(ctx context.Context) (int64, error)
	// Snapshot returns the variables as defined at schemaVersion.
	Snapshot(ctx context.Context, schemaVersion int64) ([]domain.DictionaryVariable, error)
}

// PackageSummary is a package with its newest version, for lists.
type PackageSummary struct {
	domain.Package
	VersionCount  int
	LatestVersion domain.Version // zero value when the package has no version
}

// ListFilter selects a page of packages ordered by product code and scope.
type ListFilter struct {
	ProductCode string // "" means every product
	Page        int    // 1-based
	PerPage     int
}

// Page is one page of packages and the total number of matches.
type Page struct {
	Items   []PackageSummary
	Total   int64
	Page    int
	PerPage int
}

// PolicyRepository persists packages and versions.
//
// Operations that add a version must hold LockPackage in the same transaction, which
// serializes version numbering per package.
type PolicyRepository interface {
	// InsertPackage stores a package or returns ErrPolicyExists.
	InsertPackage(ctx context.Context, product ProductRef, scope, actorID string) (domain.Package, error)
	// FindPackage returns ErrPolicyNotFound when absent. forUpdate locks the row until the transaction ends.
	FindPackage(ctx context.Context, id string, forUpdate bool) (domain.Package, error)
	ListPackages(ctx context.Context, f ListFilter) ([]PackageSummary, int64, error)
	// InsertDraft stores the next numbered DRAFT of the package. basedOnID may be "".
	InsertDraft(ctx context.Context, packageID, description, basedOnID string, dictionarySchemaVersion int64, actorID string) (domain.Version, error)
	// FindVersion returns ErrVersionNotFound when absent. forUpdate locks the row.
	FindVersion(ctx context.Context, id string, forUpdate bool) (domain.Version, error)
	// ListVersions returns the versions of a package, newest first.
	ListVersions(ctx context.Context, packageID string) ([]domain.Version, error)
	// UpdateDraft stores the editable fields of a DRAFT.
	UpdateDraft(ctx context.Context, v domain.Version) (domain.Version, error)
	// Freeze moves a DRAFT to PENDING_APPROVAL with its snapshot and checksum.
	Freeze(ctx context.Context, id string, snapshot []byte, checksum, actorID string) (domain.Version, error)
	// ListRules returns the RAC rules of a version in evaluation order.
	ListRules(ctx context.Context, versionID string) (domain.RuleSet, error)
	// ReplaceRules replaces all RAC rules of a DRAFT with rules, which carry their order.
	ReplaceRules(ctx context.Context, versionID string, rules domain.RuleSet) error
	// CopyRules copies the RAC rules of one version onto another.
	CopyRules(ctx context.Context, fromVersionID, toVersionID string) error
	// TouchDraft records that a DRAFT changed, by moving its updated_at.
	TouchDraft(ctx context.Context, versionID string) error
}

type PolicyService struct {
	repo     PolicyRepository
	products ProductLookup
	dict     DictionaryReader
	tx       tx.Manager
}

func NewPolicyService(repo PolicyRepository, products ProductLookup, dict DictionaryReader, txm tx.Manager) *PolicyService {
	return &PolicyService{repo: repo, products: products, dict: dict, tx: txm}
}

// Create opens the policy package for a product and scope and its first draft (version 1).
func (s *PolicyService) Create(ctx context.Context, actorID string, in domain.NewPackageInput) (domain.Package, domain.Version, error) {
	req, err := domain.ValidateNewPackage(in)
	if err != nil {
		return domain.Package{}, domain.Version{}, err
	}
	var (
		pkg domain.Package
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
		if pkg, err = s.repo.InsertPackage(ctx, product, req.Scope, actorID); err != nil {
			return err
		}
		dictVersion, err := s.dict.CurrentSchemaVersion(ctx)
		if err != nil {
			return err
		}
		ver, err = s.repo.InsertDraft(ctx, pkg.ID, req.Description, "", dictVersion, actorID)
		return err
	})
	if err != nil {
		return domain.Package{}, domain.Version{}, err
	}
	return pkg, ver, nil
}

// Get returns a package with all of its versions, newest first.
func (s *PolicyService) Get(ctx context.Context, id string) (domain.Package, []domain.Version, error) {
	if !domain.IsValidID(id) {
		return domain.Package{}, nil, ErrPolicyNotFound
	}
	pkg, err := s.repo.FindPackage(ctx, id, false)
	if err != nil {
		return domain.Package{}, nil, err
	}
	versions, err := s.repo.ListVersions(ctx, pkg.ID)
	return pkg, versions, err
}

// List returns a page of packages. Out-of-range paging values are clamped.
func (s *PolicyService) List(ctx context.Context, f ListFilter) (Page, error) {
	if f.Page < 1 {
		f.Page = 1
	}
	if f.PerPage < 1 {
		f.PerPage = DefaultPerPage
	}
	if f.PerPage > MaxPerPage {
		f.PerPage = MaxPerPage
	}
	items, total, err := s.repo.ListPackages(ctx, f)
	if err != nil {
		return Page{}, err
	}
	return Page{Items: items, Total: total, Page: f.Page, PerPage: f.PerPage}, nil
}

// CloneInput selects what to copy. SourceVersionID "" means the ACTIVE version if there is
// one, otherwise the highest-numbered version.
type CloneInput struct {
	SourceVersionID string
	Description     string
}

// Clone creates the next draft of a package as a copy of an existing version, whatever its
// status. This is also how a rejected version is revised (SYSTEM_DESIGN.md 9.2). The new
// draft is written against the current dictionary schema version.
func (s *PolicyService) Clone(ctx context.Context, actorID, packageID string, in CloneInput) (domain.Version, error) {
	if !domain.IsValidID(packageID) {
		return domain.Version{}, ErrPolicyNotFound
	}
	if err := domain.ValidateDescription(in.Description); err != nil {
		return domain.Version{}, err
	}
	if in.SourceVersionID != "" && !domain.IsValidID(in.SourceVersionID) {
		return domain.Version{}, &domain.ValidationError{Fields: []domain.FieldError{{Field: "source_version_id", Issue: "invalid_format"}}}
	}
	var out domain.Version
	err := s.tx.WithinTransaction(ctx, func(ctx context.Context) error {
		pkg, err := s.repo.FindPackage(ctx, packageID, true)
		if err != nil {
			return err
		}
		source, err := s.cloneSource(ctx, pkg, in.SourceVersionID)
		if err != nil {
			return err
		}
		dictVersion, err := s.dict.CurrentSchemaVersion(ctx)
		if err != nil {
			return err
		}
		description := in.Description
		if description == "" {
			description = source.Description
		}
		out, err = s.repo.InsertDraft(ctx, pkg.ID, description, source.ID, dictVersion, actorID)
		if err != nil {
			return err
		}
		// The copy is exact. The rules are not re-validated here: the draft may be written
		// against a newer dictionary, and validation happens when the rules are edited and
		// again at submit.
		return s.repo.CopyRules(ctx, source.ID, out.ID)
	})
	return out, err
}

func (s *PolicyService) cloneSource(ctx context.Context, pkg domain.Package, sourceID string) (domain.Version, error) {
	if sourceID != "" {
		v, err := s.repo.FindVersion(ctx, sourceID, false)
		if errors.Is(err, ErrVersionNotFound) || (err == nil && v.PackageID != pkg.ID) {
			return domain.Version{}, ErrSourceNotInPolicy
		}
		return v, err
	}
	versions, err := s.repo.ListVersions(ctx, pkg.ID)
	if err != nil {
		return domain.Version{}, err
	}
	for _, v := range versions { // newest first
		if v.Status == domain.StatusActive {
			return v, nil
		}
	}
	if len(versions) == 0 {
		return domain.Version{}, ErrVersionNotFound
	}
	return versions[0], nil
}

// GetVersion returns one version, including its snapshot once frozen.
func (s *PolicyService) GetVersion(ctx context.Context, id string) (domain.Version, error) {
	if !domain.IsValidID(id) {
		return domain.Version{}, ErrVersionNotFound
	}
	return s.repo.FindVersion(ctx, id, false)
}

// UpdateDraft changes the editable fields of a draft. A frozen version is refused with
// ErrVersionFrozen. A request that changes nothing returns the draft unchanged.
func (s *PolicyService) UpdateDraft(ctx context.Context, id string, c domain.Changes) (domain.Version, error) {
	if !domain.IsValidID(id) {
		return domain.Version{}, ErrVersionNotFound
	}
	var out domain.Version
	err := s.tx.WithinTransaction(ctx, func(ctx context.Context) error {
		current, err := s.repo.FindVersion(ctx, id, true)
		if err != nil {
			return err
		}
		if current.Status.IsFrozen() {
			return ErrVersionFrozen
		}
		dictVersion, err := s.dict.CurrentSchemaVersion(ctx)
		if err != nil {
			return err
		}
		next, changed, err := current.Apply(c, dictVersion)
		if err != nil {
			return err
		}
		if !changed {
			out = current
			return nil
		}
		out, err = s.repo.UpdateDraft(ctx, next)
		return err
	})
	return out, err
}

// Submit freezes a draft: it captures the canonical snapshot (including the dictionary
// definitions the draft was written against), stores its checksum, and moves the version to
// PENDING_APPROVAL. After this nothing about the version changes.
//
// The content gates of SYSTEM_DESIGN.md 9.3 (expressions compile, bins and matrix are
// unambiguous, golden tests pass) arrive with RF-010. Until the RAC, scorecard, and matrix
// exist there is nothing to check, and the approval workflow that consumes a pending version
// arrives with RF-018 and RF-019.
func (s *PolicyService) Submit(ctx context.Context, actorID, id string) (domain.Version, error) {
	if !domain.IsValidID(id) {
		return domain.Version{}, ErrVersionNotFound
	}
	var out domain.Version
	err := s.tx.WithinTransaction(ctx, func(ctx context.Context) error {
		current, err := s.repo.FindVersion(ctx, id, true)
		if err != nil {
			return err
		}
		if current.Status.IsFrozen() {
			return ErrVersionFrozen
		}
		dictionary, err := s.dict.Snapshot(ctx, current.DictionarySchemaVersion)
		if err != nil {
			return err
		}
		rules, err := s.repo.ListRules(ctx, current.ID)
		if err != nil {
			return err
		}
		// Gate (SYSTEM_DESIGN.md 9.3): every expression must still compile against the
		// dictionary this version is pinned to. Rules copied by clone, or a dictionary
		// version chosen after the rules were written, can break that.
		if _, err := domain.ValidateRules(ruleInputs(rules), domain.DictionaryEnvironment(dictionary)); err != nil {
			var verr *domain.ValidationError
			if errors.As(err, &verr) {
				return &InvalidPolicyError{Fields: verr.Fields}
			}
			return err
		}
		snapshot, checksum, err := domain.BuildSnapshot(current, dictionary, rules)
		if err != nil {
			return err
		}
		out, err = s.repo.Freeze(ctx, current.ID, snapshot, checksum, actorID)
		return err
	})
	return out, err
}
