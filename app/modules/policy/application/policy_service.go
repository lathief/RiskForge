// Package application contains the policy package and version use cases (RF-006).
package application

import (
	"context"
	"errors"

	"github.com/lathief/RiskForge/app/modules/policy/domain"
	"github.com/lathief/RiskForge/app/platform/tx"
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
	// ErrNoActivePolicy means the product has no active policy version for the scope (SYSTEM_DESIGN.md 13.4: 409).
	ErrNoActivePolicy = errors.New("no active policy version")
	// ErrVersionNotPending means an approval outcome was recorded for a version that is not
	// PENDING_APPROVAL, or whose checksum is not the one that was approved.
	ErrVersionNotPending = errors.New("policy version is not pending approval with this checksum")
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
	// CheckInput validates applicant facts against the given dictionary variables with the
	// dictionary module's input rules and returns every problem, ordered by variable code.
	CheckInput(dictionary []domain.DictionaryVariable, facts map[string]any) []domain.FieldError
	// PrepareInput validates applicant input against the dictionary and builds the facts the
	// evaluator reads, including the derived features (RF-011). It returns the issues when the
	// input is invalid, or an error when the dictionary cannot be computed.
	PrepareInput(dictionary []domain.DictionaryVariable, input map[string]any) (domain.Prepared, []domain.FieldError, error)
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
	// GetScorecard returns the scorecard of a version, or nil when none is configured.
	GetScorecard(ctx context.Context, versionID string) (*domain.Scorecard, error)
	// ReplaceScorecard replaces the scorecard of a DRAFT with sc, which carries its orders.
	ReplaceScorecard(ctx context.Context, versionID string, sc *domain.Scorecard) error
	// CopyScorecard copies the scorecard of one version onto another (nothing if there is none).
	CopyScorecard(ctx context.Context, fromVersionID, toVersionID string) error
	// GetMatrix returns the decision matrix rows of a version in order (empty when none).
	GetMatrix(ctx context.Context, versionID string) (domain.DecisionMatrix, error)
	// ReplaceMatrix replaces the decision matrix of a DRAFT with m (empty only deletes).
	ReplaceMatrix(ctx context.Context, versionID string, m domain.DecisionMatrix) error
	// CopyMatrix copies the decision matrix of one version onto another.
	CopyMatrix(ctx context.Context, fromVersionID, toVersionID string) error
	// ListTestCases returns the test cases of a version in order.
	ListTestCases(ctx context.Context, versionID string) (domain.TestCases, error)
	// ReplaceTestCases replaces the test cases of a DRAFT with cases (empty deletes them all).
	ReplaceTestCases(ctx context.Context, versionID string, cases domain.TestCases) error
	// CopyTestCases copies the test cases of one version onto another.
	CopyTestCases(ctx context.Context, fromVersionID, toVersionID string) error
	// FindActiveReference follows the active binding of the product and scope and returns which
	// version it points to, without the snapshot, or ErrNoActivePolicy. It is the one small query a
	// decision makes before using the compiled policy cache (SYSTEM_DESIGN.md 10.6).
	FindActiveReference(ctx context.Context, productCode, scope string) (ActiveRef, error)
	// TouchDraft records that a DRAFT changed, by moving its updated_at.
	TouchDraft(ctx context.Context, versionID string) error
	// SetStatus moves a version from one status to another; ErrVersionNotPending when the
	// version is no longer in the from status.
	SetStatus(ctx context.Context, versionID string, from, to domain.Status) error
	// FindBinding returns the version the active binding of a package points to, with found
	// false when the package has none. forUpdate locks the binding row.
	FindBinding(ctx context.Context, packageID string, forUpdate bool) (domain.Version, bool, error)
	// BindActive points the active binding of a package at a version (insert or move).
	BindActive(ctx context.Context, packageID, versionID, actorID string) error
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
		// The copy of every content section (rules, scorecard, matrix, test cases) is exact. None is re-validated here: the draft may be written
		// against a newer dictionary, and validation happens when the content is edited and
		// again at submit.
		if err := s.repo.CopyRules(ctx, source.ID, out.ID); err != nil {
			return err
		}
		if err := s.repo.CopyScorecard(ctx, source.ID, out.ID); err != nil {
			return err
		}
		if err := s.repo.CopyMatrix(ctx, source.ID, out.ID); err != nil {
			return err
		}
		return s.repo.CopyTestCases(ctx, source.ID, out.ID)
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

// Submit freezes a draft: it runs the full policy validation of SYSTEM_DESIGN.md 9.3 (RF-010,
// see domain.ValidatePolicy) against the dictionary the draft is pinned to, and when it passes it
// captures the canonical snapshot, stores its checksum, and moves the version to
// PENDING_APPROVAL. After this nothing about the version changes. A failed validation returns an
// InvalidPolicyError with every problem and leaves the draft as it is. The approval workflow
// that consumes a pending version arrives with RF-018 and RF-019.
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
		content, dictionary, err := s.loadContent(ctx, current)
		if err != nil {
			return err
		}
		if report := s.validate(content, dictionary); !report.Valid() {
			return &InvalidPolicyError{Fields: report.Issues}
		}
		snapshot, checksum, err := domain.BuildSnapshot(current, dictionary, content)
		if err != nil {
			return err
		}
		out, err = s.repo.Freeze(ctx, current.ID, snapshot, checksum, actorID)
		return err
	})
	return out, err
}

// Validate runs the full policy validation on a version and returns the report without changing
// anything. It works for any status: for a frozen version it re-checks the stored content
// against the dictionary version it is pinned to.
func (s *PolicyService) Validate(ctx context.Context, id string) (domain.Version, domain.Report, error) {
	if !domain.IsValidID(id) {
		return domain.Version{}, domain.Report{}, ErrVersionNotFound
	}
	v, err := s.repo.FindVersion(ctx, id, false)
	if err != nil {
		return domain.Version{}, domain.Report{}, err
	}
	content, dictionary, err := s.loadContent(ctx, v)
	if err != nil {
		return domain.Version{}, domain.Report{}, err
	}
	return v, s.validate(content, dictionary), nil
}

func (s *PolicyService) validate(content domain.Content, dictionary []domain.DictionaryVariable) domain.Report {
	return domain.ValidatePolicy(content, dictionary, func(facts map[string]any) []domain.FieldError {
		return s.dict.CheckInput(dictionary, facts)
	}, func(input map[string]any) (domain.Prepared, []domain.FieldError, error) {
		return s.dict.PrepareInput(dictionary, input)
	})
}

// loadContent reads every content section of a version and the dictionary it is pinned to.
func (s *PolicyService) loadContent(ctx context.Context, v domain.Version) (domain.Content, []domain.DictionaryVariable, error) {
	dictionary, err := s.dict.Snapshot(ctx, v.DictionarySchemaVersion)
	if err != nil {
		return domain.Content{}, nil, err
	}
	rules, err := s.repo.ListRules(ctx, v.ID)
	if err != nil {
		return domain.Content{}, nil, err
	}
	scorecard, err := s.repo.GetScorecard(ctx, v.ID)
	if err != nil {
		return domain.Content{}, nil, err
	}
	matrix, err := s.repo.GetMatrix(ctx, v.ID)
	if err != nil {
		return domain.Content{}, nil, err
	}
	cases, err := s.repo.ListTestCases(ctx, v.ID)
	if err != nil {
		return domain.Content{}, nil, err
	}
	return domain.Content{Rules: rules, Scorecard: scorecard, Matrix: matrix, TestCases: cases}, dictionary, nil
}
