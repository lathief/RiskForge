// Package application contains the product catalog use cases.
package application

import (
	"context"
	"errors"

	"goravel/app/modules/catalog/domain"
	"goravel/app/platform/tx"
)

var (
	ErrProductNotFound = errors.New("product not found")
	ErrCodeTaken       = errors.New("product code already exists")
)

const (
	DefaultPerPage = 20
	MaxPerPage     = 100
	MaxPage        = 10000
)

// ListFilter selects a page of products ordered by code.
type ListFilter struct {
	Status  *domain.Status
	Page    int // 1-based
	PerPage int
}

// Page is one page of products and the total number of matches.
type Page struct {
	Items   []domain.Product
	Total   int64
	Page    int
	PerPage int
}

// ProductRepository persists products.
type ProductRepository interface {
	// Insert stores p and returns it with ID and timestamps, or ErrCodeTaken.
	Insert(ctx context.Context, p domain.Product) (domain.Product, error)
	// FindByCode returns ErrProductNotFound when absent. forUpdate locks the row
	// until the surrounding transaction ends.
	FindByCode(ctx context.Context, code string, forUpdate bool) (domain.Product, error)
	// Update stores the mutable fields of p and returns the stored product.
	Update(ctx context.Context, p domain.Product) (domain.Product, error)
	List(ctx context.Context, f ListFilter) ([]domain.Product, int64, error)
}

type ProductService struct {
	repo ProductRepository
	tx   tx.Manager
}

func NewProductService(repo ProductRepository, txm tx.Manager) *ProductService {
	return &ProductService{repo: repo, tx: txm}
}

// Create validates and stores a new product.
func (s *ProductService) Create(ctx context.Context, code, name, description string, status *domain.Status) (domain.Product, error) {
	p, err := domain.NewProduct(code, name, description, status)
	if err != nil {
		return domain.Product{}, err
	}
	return s.repo.Insert(ctx, p)
}

// Get returns the product with the code.
func (s *ProductService) Get(ctx context.Context, code string) (domain.Product, error) {
	if !domain.IsValidCode(code) {
		return domain.Product{}, ErrProductNotFound
	}
	return s.repo.FindByCode(ctx, code, false)
}

// Update applies a partial change. The row is locked for the read-modify-write so that
// concurrent updates to different fields do not overwrite each other.
func (s *ProductService) Update(ctx context.Context, code string, c domain.Changes) (domain.Product, error) {
	if !domain.IsValidCode(code) {
		return domain.Product{}, ErrProductNotFound
	}
	var out domain.Product
	err := s.tx.WithinTransaction(ctx, func(ctx context.Context) error {
		current, err := s.repo.FindByCode(ctx, code, true)
		if err != nil {
			return err
		}
		next, err := current.Apply(c)
		if err != nil {
			return err
		}
		out, err = s.repo.Update(ctx, next)
		return err
	})
	return out, err
}

// List returns a page of products. Out-of-range paging values are clamped.
func (s *ProductService) List(ctx context.Context, f ListFilter) (Page, error) {
	if f.Page < 1 {
		f.Page = 1
	}
	if f.PerPage < 1 {
		f.PerPage = DefaultPerPage
	}
	if f.PerPage > MaxPerPage {
		f.PerPage = MaxPerPage
	}
	items, total, err := s.repo.List(ctx, f)
	if err != nil {
		return Page{}, err
	}
	return Page{Items: items, Total: total, Page: f.Page, PerPage: f.PerPage}, nil
}
