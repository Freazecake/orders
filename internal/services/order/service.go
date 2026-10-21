// Package order — сервисный слой заказов: сценарии поверх домена и хранилища.
//
// Здесь решается, что вообще происходит: провалидировать, посчитать сумму,
// сходить в хранилище, вернуть доменную ошибку. Контроллер после этого
// занимается только переводом в HTTP.
package order

import (
	"context"
	"time"

	"github.com/google/uuid"

	domain "github.com/Freazecake/orders/internal/models/order"
)

type Repository interface {
	Save(ctx context.Context, o domain.Order) error
	GetByID(ctx context.Context, id uuid.UUID) (domain.Order, error)
}

type Service struct {
	repo Repository
}

func New(repo Repository) *Service {
	return &Service{repo: repo}
}

func (svc *Service) Create(ctx context.Context, customerID string, items_r []domain.ItemRequest) (domain.Order, error) {
	if customerID == "" {
		return domain.Order{}, domain.ErrInvalidOrder
	}

	items, err := Validate(items_r)
	if err != nil {
		return domain.Order{}, err
	}

	var total int
	for i := range items {
		total += items[i].PriceCents * items[i].QTY
	}

	ID, err := uuid.NewV7()
	if err != nil {
		return domain.Order{}, err
	}

	o := domain.Order{
		ID:         ID,
		CustomerID: customerID,
		Status:     domain.StatusCreated,
		Items:      items,
		TotalCents: total,
		CreatedAt:  time.Now().UTC(),
	}

	if err := svc.repo.Save(ctx, o); err != nil {
		return domain.Order{}, err
	}
	return o, nil
}

func (svc *Service) GetByID(ctx context.Context, id uuid.UUID) (domain.Order, error) {
	o, err := svc.repo.GetByID(ctx, id)
	if err != nil {
		return domain.Order{}, err
	}
	return o, nil
}

func Validate(items_r []domain.ItemRequest) ([]domain.Item, error) {
	items := make([]domain.Item, 0)
	for _, item := range items_r {
		if item.PriceCents == nil || item.Qty == nil || item.SKU == nil || *item.Qty <= 0 || *item.PriceCents < 0 {
			return make([]domain.Item, 0), domain.ErrInvalidOrder
		}

		it := domain.Item{
			PriceCents: *item.PriceCents,
			SKU:        *item.SKU,
			QTY:        *item.Qty}

		items = append(items, it)
	}

	return items, nil
}
