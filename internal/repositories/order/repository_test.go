package order

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	domain "github.com/Freazecake/orders/internal/models/order"
)

func newOrder(customer string) domain.Order {
	return domain.Order{
		ID:         uuid.New(),
		CustomerID: customer,
		Status:     domain.StatusCreated,
		Items:      []domain.Item{{SKU: "A", Qty: 2, PriceCents: 150}},
		TotalCents: 300,
		CreatedAt:  time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	}
}

func TestNew(t *testing.T) {
	t.Parallel()

	r := New()
	if r == nil {
		t.Fatal("New вернул nil")
	}
	// Свежее хранилище пусто и готово к работе.
	if _, err := r.GetByID(context.Background(), uuid.New()); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("ошибка = %v, ожидали %v", err, domain.ErrNotFound)
	}
}

func TestSaveAndGetByID(t *testing.T) {
	t.Parallel()

	r := New()
	ctx := context.Background()
	want := newOrder("c-1")

	if err := r.Save(ctx, want); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := r.GetByID(ctx, want.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("заказ = %+v, ожидали %+v", got, want)
	}
}

func TestGetByID_NotFound(t *testing.T) {
	t.Parallel()

	r := New()
	ctx := context.Background()

	if err := r.Save(ctx, newOrder("c-1")); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := r.GetByID(ctx, uuid.New())
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("ошибка = %v, ожидали %v", err, domain.ErrNotFound)
	}
	if !reflect.DeepEqual(got, domain.Order{}) {
		t.Errorf("при отсутствии заказа ожидали нулевое значение, получили %+v", got)
	}
}

func TestSave_OverwritesSameID(t *testing.T) {
	t.Parallel()

	r := New()
	ctx := context.Background()

	first := newOrder("c-1")
	if err := r.Save(ctx, first); err != nil {
		t.Fatalf("Save #1: %v", err)
	}

	second := first
	second.CustomerID = "c-2"
	second.TotalCents = 999
	if err := r.Save(ctx, second); err != nil {
		t.Fatalf("Save #2: %v", err)
	}

	got, err := r.GetByID(ctx, first.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.CustomerID != "c-2" || got.TotalCents != 999 {
		t.Errorf("ожидали перезапись, получили %+v", got)
	}
}

func TestSave_DifferentOrdersAreIndependent(t *testing.T) {
	t.Parallel()

	r := New()
	ctx := context.Background()

	a, b := newOrder("a"), newOrder("b")
	for _, o := range []domain.Order{a, b} {
		if err := r.Save(ctx, o); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}

	for _, want := range []domain.Order{a, b} {
		got, err := r.GetByID(ctx, want.ID)
		if err != nil {
			t.Fatalf("GetByID(%s): %v", want.ID, err)
		}
		if got.CustomerID != want.CustomerID {
			t.Errorf("для %s получили клиента %q, ожидали %q", want.ID, got.CustomerID, want.CustomerID)
		}
	}
}

// Запускать с -race: проверяет, что мьютекс действительно защищает карту.
func TestRepo_ConcurrentAccess(t *testing.T) {
	t.Parallel()

	const n = 100

	r := New()
	ctx := context.Background()
	orders := make([]domain.Order, n)
	for i := range orders {
		orders[i] = newOrder("c")
	}

	var wg sync.WaitGroup
	for i := range orders {
		wg.Add(2)
		go func() {
			defer wg.Done()
			if err := r.Save(ctx, orders[i]); err != nil {
				t.Errorf("Save: %v", err)
			}
		}()
		go func() {
			defer wg.Done()
			// Результат может быть и «найдено», и «не найдено» — важна только безопасность доступа.
			_, _ = r.GetByID(ctx, orders[i].ID)
		}()
	}
	wg.Wait()

	for _, o := range orders {
		if _, err := r.GetByID(ctx, o.ID); err != nil {
			t.Errorf("заказ %s потерян: %v", o.ID, err)
		}
	}
}
