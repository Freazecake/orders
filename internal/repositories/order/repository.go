// Package order — хранилище заказов.
//
// В Модуле 0 это карта в памяти. В Модуле 5 на её место встанет Postgres,
// и если границы проведены правильно, менять придётся только этот пакет.
package order

import (
	"context"
	"sync"

	"github.com/google/uuid"

	domain "github.com/Freazecake/orders/internal/models/order"
)

type Repo struct {
	mu     sync.Mutex
	orders map[uuid.UUID]domain.Order
}

func New() *Repo {
	return &Repo{orders: make(map[uuid.UUID]domain.Order)}
}

func (r *Repo) Save(ctx context.Context, o domain.Order) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.orders[o.ID] = o
	return nil
}

func (r *Repo) GetByID(ctx context.Context, id uuid.UUID) (domain.Order, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	o, ok := r.orders[id]
	if !ok {
		return domain.Order{}, domain.ErrNotFound
	}
	return o, nil
}
