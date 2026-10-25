package order

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"

	domain "github.com/Freazecake/orders/internal/models/order"
)

// fakeRepo — ручная заглушка Repository.
type fakeRepo struct {
	saveFn func(ctx context.Context, o domain.Order) error
	getFn  func(ctx context.Context, id uuid.UUID) (domain.Order, error)

	saved     []domain.Order
	saveCalls int
	getCalls  int
}

func (f *fakeRepo) Save(ctx context.Context, o domain.Order) error {
	f.saveCalls++
	if f.saveFn != nil {
		if err := f.saveFn(ctx, o); err != nil {
			return err
		}
	}
	f.saved = append(f.saved, o)
	return nil
}

func (f *fakeRepo) GetByID(ctx context.Context, id uuid.UUID) (domain.Order, error) {
	f.getCalls++
	return f.getFn(ctx, id)
}

func ptr[T any](v T) *T { return &v }

func item(sku string, qty, price int) domain.ItemRequest {
	return domain.ItemRequest{SKU: ptr(sku), Qty: ptr(qty), PriceCents: ptr(price)}
}

func TestValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		in      []domain.ItemRequest
		want    []domain.Item
		wantErr error
	}{
		{
			name: "одна позиция",
			in:   []domain.ItemRequest{item("A", 2, 150)},
			want: []domain.Item{{SKU: "A", QTY: 2, PriceCents: 150}},
		},
		{
			name: "несколько позиций сохраняют порядок",
			in:   []domain.ItemRequest{item("A", 1, 10), item("B", 3, 20), item("C", 5, 30)},
			want: []domain.Item{{SKU: "A", QTY: 1, PriceCents: 10}, {SKU: "B", QTY: 3, PriceCents: 20}, {SKU: "C", QTY: 5, PriceCents: 30}},
		},
		{
			name: "нулевая цена допустима",
			in:   []domain.ItemRequest{item("FREE", 1, 0)},
			want: []domain.Item{{SKU: "FREE", QTY: 1, PriceCents: 0}},
		},
		{
			name: "пустой sku допустим (проверяется только наличие поля)",
			in:   []domain.ItemRequest{item("", 1, 1)},
			want: []domain.Item{{SKU: "", QTY: 1, PriceCents: 1}},
		},
		{
			name: "пустой список",
			in:   []domain.ItemRequest{},
			want: []domain.Item{},
		},
		{
			name: "nil-список",
			in:   nil,
			want: []domain.Item{},
		},
		{
			name:    "нет sku",
			in:      []domain.ItemRequest{{Qty: ptr(1), PriceCents: ptr(1)}},
			wantErr: domain.ErrInvalidOrder,
		},
		{
			name:    "нет qty",
			in:      []domain.ItemRequest{{SKU: ptr("A"), PriceCents: ptr(1)}},
			wantErr: domain.ErrInvalidOrder,
		},
		{
			name:    "нет price_cents",
			in:      []domain.ItemRequest{{SKU: ptr("A"), Qty: ptr(1)}},
			wantErr: domain.ErrInvalidOrder,
		},
		{
			name:    "пустая позиция",
			in:      []domain.ItemRequest{{}},
			wantErr: domain.ErrInvalidOrder,
		},
		{
			name:    "qty равно нулю",
			in:      []domain.ItemRequest{item("A", 0, 1)},
			wantErr: domain.ErrInvalidOrder,
		},
		{
			name:    "qty отрицательное",
			in:      []domain.ItemRequest{item("A", -1, 1)},
			wantErr: domain.ErrInvalidOrder,
		},
		{
			name:    "цена отрицательная",
			in:      []domain.ItemRequest{item("A", 1, -1)},
			wantErr: domain.ErrInvalidOrder,
		},
		{
			name:    "валидная позиция не спасает невалидную",
			in:      []domain.ItemRequest{item("A", 1, 1), item("B", 0, 1)},
			wantErr: domain.ErrInvalidOrder,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := Validate(tt.in)

			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("ошибка = %v, ожидали %v", err, tt.wantErr)
			}
			if got == nil {
				t.Fatal("результат не должен быть nil")
			}
			if tt.wantErr != nil {
				if len(got) != 0 {
					t.Errorf("при ошибке результат должен быть пустым, получили %+v", got)
				}
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("результат = %+v, ожидали %+v", got, tt.want)
			}
		})
	}
}

func TestCreate_Success(t *testing.T) {
	t.Parallel()

	repo := &fakeRepo{}
	svc := New(repo)

	before := time.Now().UTC().Add(-time.Second)
	o, err := svc.Create(context.Background(), ptr("c-1"), []domain.ItemRequest{
		item("A", 2, 150),
		item("B", 1, 99),
	})
	after := time.Now().UTC().Add(time.Second)

	if err != nil {
		t.Fatalf("неожиданная ошибка: %v", err)
	}
	if o.ID == uuid.Nil {
		t.Error("ID не должен быть нулевым")
	}
	if o.CustomerID != "c-1" {
		t.Errorf("CustomerID = %q, ожидали %q", o.CustomerID, "c-1")
	}
	if o.Status != domain.StatusCreated {
		t.Errorf("Status = %q, ожидали %q", o.Status, domain.StatusCreated)
	}
	if want := 2*150 + 1*99; o.TotalCents != want {
		t.Errorf("TotalCents = %d, ожидали %d", o.TotalCents, want)
	}
	wantItems := []domain.Item{{SKU: "A", QTY: 2, PriceCents: 150}, {SKU: "B", QTY: 1, PriceCents: 99}}
	if !reflect.DeepEqual(o.Items, wantItems) {
		t.Errorf("Items = %+v, ожидали %+v", o.Items, wantItems)
	}
	if o.CreatedAt.Before(before) || o.CreatedAt.After(after) {
		t.Errorf("CreatedAt = %v вне окна [%v; %v]", o.CreatedAt, before, after)
	}
	if o.CreatedAt.Location() != time.UTC {
		t.Errorf("CreatedAt должен быть в UTC, получили %v", o.CreatedAt.Location())
	}

	// Заказ сохранён ровно один раз и ровно в том виде, в каком возвращён.
	if repo.saveCalls != 1 {
		t.Fatalf("Save вызван %d раз, ожидали 1", repo.saveCalls)
	}
	if !reflect.DeepEqual(repo.saved[0], o) {
		t.Errorf("сохранённый заказ %+v не совпадает с возвращённым %+v", repo.saved[0], o)
	}
}

func TestCreate_IDIsUUIDv7(t *testing.T) {
	t.Parallel()

	o, err := New(&fakeRepo{}).Create(context.Background(), ptr("c-1"), []domain.ItemRequest{item("A", 1, 1)})
	if err != nil {
		t.Fatalf("неожиданная ошибка: %v", err)
	}
	// Версия UUID — первый символ третьей группы.
	if v := o.ID.String()[14]; v != '7' {
		t.Errorf("ожидали UUID версии 7, получили %s", o.ID)
	}
}

func TestCreate_UniqueIDs(t *testing.T) {
	t.Parallel()

	svc := New(&fakeRepo{})
	seen := make(map[uuid.UUID]struct{})
	for i := 0; i < 100; i++ {
		o, err := svc.Create(context.Background(), ptr("c-1"), []domain.ItemRequest{item("A", 1, 1)})
		if err != nil {
			t.Fatalf("неожиданная ошибка: %v", err)
		}
		if _, dup := seen[o.ID]; dup {
			t.Fatalf("повторный ID %s на итерации %d", o.ID, i)
		}
		seen[o.ID] = struct{}{}
	}
}

func TestCreate_TotalCalculation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		items []domain.ItemRequest
		want  int
	}{
		{name: "одна позиция", items: []domain.ItemRequest{item("A", 3, 100)}, want: 300},
		{name: "несколько позиций", items: []domain.ItemRequest{item("A", 1, 1), item("B", 2, 2), item("C", 3, 3)}, want: 14},
		{name: "бесплатная позиция не меняет сумму", items: []domain.ItemRequest{item("A", 2, 50), item("F", 10, 0)}, want: 100},
		{name: "крупные значения", items: []domain.ItemRequest{item("A", 1000, 1_000_000)}, want: 1_000_000_000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			o, err := New(&fakeRepo{}).Create(context.Background(), ptr("c-1"), tt.items)
			if err != nil {
				t.Fatalf("неожиданная ошибка: %v", err)
			}
			if o.TotalCents != tt.want {
				t.Errorf("TotalCents = %d, ожидали %d", o.TotalCents, tt.want)
			}
		})
	}
}

func TestCreate_InvalidInput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		customerID *string
		items      []domain.ItemRequest
	}{
		{name: "customerID не передан (nil)", customerID: nil, items: []domain.ItemRequest{item("A", 1, 1)}},
		{name: "пустой customerID", customerID: ptr(""), items: []domain.ItemRequest{item("A", 1, 1)}},
		{name: "невалидная позиция: qty=0", customerID: ptr("c-1"), items: []domain.ItemRequest{item("A", 0, 1)}},
		{name: "невалидная позиция: цена < 0", customerID: ptr("c-1"), items: []domain.ItemRequest{item("A", 1, -5)}},
		{name: "невалидная позиция: нет полей", customerID: ptr("c-1"), items: []domain.ItemRequest{{}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			repo := &fakeRepo{}
			o, err := New(repo).Create(context.Background(), tt.customerID, tt.items)

			if !errors.Is(err, domain.ErrInvalidOrder) {
				t.Errorf("ошибка = %v, ожидали %v", err, domain.ErrInvalidOrder)
			}
			if !reflect.DeepEqual(o, domain.Order{}) {
				t.Errorf("при ошибке должен возвращаться нулевой заказ, получили %+v", o)
			}
			if repo.saveCalls != 0 {
				t.Errorf("при невалидном вводе Save вызываться не должен, вызовов: %d", repo.saveCalls)
			}
		})
	}
}

func TestCreate_RepoError(t *testing.T) {
	t.Parallel()

	repoErr := errors.New("диск переполнен")
	repo := &fakeRepo{saveFn: func(context.Context, domain.Order) error { return repoErr }}

	o, err := New(repo).Create(context.Background(), ptr("c-1"), []domain.ItemRequest{item("A", 1, 1)})

	if !errors.Is(err, repoErr) {
		t.Errorf("ошибка = %v, ожидали %v", err, repoErr)
	}
	if !reflect.DeepEqual(o, domain.Order{}) {
		t.Errorf("при ошибке хранилища должен возвращаться нулевой заказ, получили %+v", o)
	}
}

func TestCreate_PassesContextToRepo(t *testing.T) {
	t.Parallel()

	type ctxKey struct{}
	var seen any
	repo := &fakeRepo{saveFn: func(ctx context.Context, _ domain.Order) error {
		seen = ctx.Value(ctxKey{})
		return nil
	}}

	ctx := context.WithValue(context.Background(), ctxKey{}, "marker")
	if _, err := New(repo).Create(ctx, ptr("c-1"), []domain.ItemRequest{item("A", 1, 1)}); err != nil {
		t.Fatalf("неожиданная ошибка: %v", err)
	}
	if seen != "marker" {
		t.Errorf("контекст не дошёл до хранилища, получили %v", seen)
	}
}

func TestGetByID_Success(t *testing.T) {
	t.Parallel()

	want := domain.Order{ID: uuid.New(), CustomerID: "c-1", Status: domain.StatusCreated, TotalCents: 10}

	var gotID uuid.UUID
	repo := &fakeRepo{getFn: func(_ context.Context, id uuid.UUID) (domain.Order, error) {
		gotID = id
		return want, nil
	}}

	got, err := New(repo).GetByID(context.Background(), want.ID)
	if err != nil {
		t.Fatalf("неожиданная ошибка: %v", err)
	}
	if gotID != want.ID {
		t.Errorf("в хранилище передан id %s, ожидали %s", gotID, want.ID)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("заказ = %+v, ожидали %+v", got, want)
	}
}

func TestGetByID_Errors(t *testing.T) {
	t.Parallel()

	other := errors.New("сбой хранилища")

	tests := []struct {
		name string
		err  error
	}{
		{name: "не найдено", err: domain.ErrNotFound},
		{name: "обёрнутое «не найдено»", err: fmt.Errorf("repo: %w", domain.ErrNotFound)},
		{name: "прочая ошибка", err: other},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			repo := &fakeRepo{getFn: func(context.Context, uuid.UUID) (domain.Order, error) {
				// Хранилище вернёт «мусор» вместе с ошибкой — сервис не должен его пропускать.
				return domain.Order{CustomerID: "leak"}, tt.err
			}}

			got, err := New(repo).GetByID(context.Background(), uuid.New())

			if !errors.Is(err, tt.err) {
				t.Errorf("ошибка = %v, ожидали %v", err, tt.err)
			}
			if !reflect.DeepEqual(got, domain.Order{}) {
				t.Errorf("при ошибке должен возвращаться нулевой заказ, получили %+v", got)
			}
		})
	}
}
