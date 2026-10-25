package order

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Freazecake/orders/internal/controllers/http/response"
	domain "github.com/Freazecake/orders/internal/models/order"
)

// fakeService — ручная заглушка Service.
type fakeService struct {
	createFn func(ctx context.Context, customerID *string, items []domain.ItemRequest) (domain.Order, error)
	getFn    func(ctx context.Context, id uuid.UUID) (domain.Order, error)

	createCalls int
	getCalls    int
}

func (f *fakeService) Create(ctx context.Context, customerID *string, items []domain.ItemRequest) (domain.Order, error) {
	f.createCalls++
	return f.createFn(ctx, customerID, items)
}

func (f *fakeService) GetByID(ctx context.Context, id uuid.UUID) (domain.Order, error) {
	f.getCalls++
	return f.getFn(ctx, id)
}

type errorEnvelope struct {
	Error struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		RequestID string `json:"request_id"`
	} `json:"error"`
}

func sampleOrder() domain.Order {
	return domain.Order{
		ID:         uuid.MustParse("0190a5c0-1111-7222-8333-444455556666"),
		CustomerID: "c-1",
		Status:     domain.StatusCreated,
		Items:      []domain.Item{{SKU: "A", QTY: 2, PriceCents: 150}},
		TotalCents: 300,
		CreatedAt:  time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	}
}

func decodeError(t *testing.T, rec *httptest.ResponseRecorder) errorEnvelope {
	t.Helper()
	var env errorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("тело ошибки не разбирается: %v; тело: %q", err, rec.Body.String())
	}
	return env
}

func assertJSONContentType(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if got, want := rec.Header().Get("Content-Type"), "application/json; charset=utf-8"; got != want {
		t.Errorf("Content-Type = %q, ожидали %q", got, want)
	}
}

func newCreateRequest(body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/orders", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return req
}

func newGetRequest(id string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/orders/"+id, nil)
	req.SetPathValue("id", id)
	return req
}

func TestNewController(t *testing.T) {
	t.Parallel()

	if NewController(&fakeService{}) == nil {
		t.Fatal("NewController вернул nil")
	}
}

func TestCreateOrder_Success(t *testing.T) {
	t.Parallel()

	want := sampleOrder()

	var (
		gotCustomer *string
		gotItems    []domain.ItemRequest
	)
	svc := &fakeService{
		createFn: func(_ context.Context, customerID *string, items []domain.ItemRequest) (domain.Order, error) {
			gotCustomer, gotItems = customerID, items
			return want, nil
		},
	}

	rec := httptest.NewRecorder()
	body := `{"customer_id":"c-1","items":[{"sku":"A","qty":2,"price_cents":150}]}`
	NewController(svc).CreateOrder(rec, newCreateRequest(body))

	if rec.Code != http.StatusCreated {
		t.Fatalf("статус = %d, ожидали %d; тело: %s", rec.Code, http.StatusCreated, rec.Body.String())
	}
	assertJSONContentType(t, rec)

	if got, wantLoc := rec.Header().Get("Location"), "/orders/"+want.ID.String(); got != wantLoc {
		t.Errorf("Location = %q, ожидали %q", got, wantLoc)
	}

	// В сервис ушли разобранные данные запроса.
	if gotCustomer == nil {
		t.Error("customerID в сервисе = nil, ожидали указатель на \"c-1\"")
	} else if *gotCustomer != "c-1" {
		t.Errorf("customerID в сервисе = %q, ожидали %q", *gotCustomer, "c-1")
	}
	if len(gotItems) != 1 ||
		gotItems[0].SKU == nil || *gotItems[0].SKU != "A" ||
		gotItems[0].Qty == nil || *gotItems[0].Qty != 2 ||
		gotItems[0].PriceCents == nil || *gotItems[0].PriceCents != 150 {
		t.Errorf("items в сервисе = %+v", gotItems)
	}

	// В теле — созданный заказ.
	var got domain.Order
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("тело не разбирается: %v; тело: %q", err, rec.Body.String())
	}
	if got.ID != want.ID || got.CustomerID != want.CustomerID || got.Status != want.Status ||
		got.TotalCents != want.TotalCents || !got.CreatedAt.Equal(want.CreatedAt) ||
		len(got.Items) != 1 || got.Items[0] != want.Items[0] {
		t.Errorf("тело = %+v, ожидали %+v", got, want)
	}
}

func TestCreateOrder_PassesRequestContext(t *testing.T) {
	t.Parallel()

	type ctxKey struct{}
	var seen any
	svc := &fakeService{
		createFn: func(ctx context.Context, _ *string, _ []domain.ItemRequest) (domain.Order, error) {
			seen = ctx.Value(ctxKey{})
			return sampleOrder(), nil
		},
	}

	req := newCreateRequest(`{"customer_id":"c-1","items":[]}`)
	req = req.WithContext(context.WithValue(req.Context(), ctxKey{}, "marker"))

	NewController(svc).CreateOrder(httptest.NewRecorder(), req)

	if seen != "marker" {
		t.Errorf("контекст запроса не дошёл до сервиса, получили %v", seen)
	}
}

func TestCreateOrder_InvalidJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
	}{
		{name: "пустое тело", body: ""},
		{name: "не json", body: "not json"},
		{name: "оборванный json", body: `{"customer_id":`},
		{name: "неверный тип customer_id", body: `{"customer_id": 5, "items": []}`},
		{name: "неверный тип items", body: `{"customer_id": "c", "items": "x"}`},
		{name: "неверный тип qty", body: `{"customer_id": "c", "items": [{"sku":"A","qty":"два","price_cents":1}]}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			svc := &fakeService{}
			rec := httptest.NewRecorder()
			NewController(svc).CreateOrder(rec, newCreateRequest(tt.body))

			if rec.Code != http.StatusBadRequest {
				t.Errorf("статус = %d, ожидали %d", rec.Code, http.StatusBadRequest)
			}
			assertJSONContentType(t, rec)
			if env := decodeError(t, rec); env.Error.Code != response.CodeBadRequest || env.Error.Message == "" {
				t.Errorf("ошибка = %+v, ожидали code=%q и непустое сообщение", env.Error, response.CodeBadRequest)
			}
			if svc.createCalls != 0 {
				t.Errorf("сервис не должен вызываться при битом запросе, вызовов: %d", svc.createCalls)
			}
			if loc := rec.Header().Get("Location"); loc != "" {
				t.Errorf("Location не должен выставляться при ошибке, получили %q", loc)
			}
		})
	}
}

// customer_id — указатель: если поля нет в JSON (или оно null), он nil.
// Контроллер сам его не разыменовывает: nil уходит в сервис как есть, а решение
// «это невалидный заказ» принимает сервис (ErrInvalidOrder → 422 в едином формате).
// Главное — хендлер не должен падать с паникой.
func TestCreateOrder_MissingCustomerID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
	}{
		{name: "пустой объект", body: `{}`},
		{name: "только items", body: `{"items":[{"sku":"A","qty":1,"price_cents":1}]}`},
		{name: "customer_id null", body: `{"customer_id":null,"items":[]}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var gotCustomer *string
			svc := &fakeService{
				createFn: func(_ context.Context, customerID *string, _ []domain.ItemRequest) (domain.Order, error) {
					gotCustomer = customerID
					return domain.Order{}, domain.ErrInvalidOrder
				},
			}
			rec := httptest.NewRecorder()

			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Errorf("хендлер упал с паникой на запросе без customer_id: %v", r)
					}
				}()
				NewController(svc).CreateOrder(rec, newCreateRequest(tt.body))
			}()

			if svc.createCalls != 1 {
				t.Fatalf("сервис вызван %d раз, ожидали 1: проверку customer_id делает сервис", svc.createCalls)
			}
			if gotCustomer != nil {
				t.Errorf("в сервис ушёл customerID = %q, ожидали nil", *gotCustomer)
			}
			if rec.Code != http.StatusUnprocessableEntity {
				t.Errorf("статус = %d, ожидали %d", rec.Code, http.StatusUnprocessableEntity)
			}
			assertJSONContentType(t, rec)
			if env := decodeError(t, rec); env.Error.Code != response.CodeValidation {
				t.Errorf("code = %q, ожидали %q", env.Error.Code, response.CodeValidation)
			}
		})
	}
}

// Пустая строка — не то же самое, что отсутствие поля: в сервис уходит
// указатель на "", а не nil (отличать их — задача сервиса).
func TestCreateOrder_EmptyCustomerIDIsForwarded(t *testing.T) {
	t.Parallel()

	var gotCustomer *string
	svc := &fakeService{
		createFn: func(_ context.Context, customerID *string, _ []domain.ItemRequest) (domain.Order, error) {
			gotCustomer = customerID
			return domain.Order{}, domain.ErrInvalidOrder
		},
	}

	rec := httptest.NewRecorder()
	NewController(svc).CreateOrder(rec, newCreateRequest(`{"customer_id":"","items":[]}`))

	if gotCustomer == nil {
		t.Fatal("customerID в сервисе = nil, ожидали указатель на пустую строку")
	}
	if *gotCustomer != "" {
		t.Errorf("customerID в сервисе = %q, ожидали пустую строку", *gotCustomer)
	}
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("статус = %d, ожидали %d", rec.Code, http.StatusUnprocessableEntity)
	}
}

func TestCreateOrder_ServiceValidationError(t *testing.T) {
	t.Parallel()

	svc := &fakeService{
		createFn: func(context.Context, *string, []domain.ItemRequest) (domain.Order, error) {
			return domain.Order{}, domain.ErrInvalidOrder
		},
	}

	rec := httptest.NewRecorder()
	NewController(svc).CreateOrder(rec, newCreateRequest(`{"customer_id":"c-1","items":[]}`))

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("статус = %d, ожидали %d", rec.Code, http.StatusUnprocessableEntity)
	}
	assertJSONContentType(t, rec)

	env := decodeError(t, rec)
	if env.Error.Code != response.CodeValidation {
		t.Errorf("code = %q, ожидали %q", env.Error.Code, response.CodeValidation)
	}
	if env.Error.Message != domain.ErrInvalidOrder.Error() {
		t.Errorf("message = %q, ожидали %q", env.Error.Message, domain.ErrInvalidOrder.Error())
	}
	if loc := rec.Header().Get("Location"); loc != "" {
		t.Errorf("Location не должен выставляться при ошибке, получили %q", loc)
	}
}

// Непредвиденная ошибка (например, отказ хранилища) — это 500 internal_error,
// а не 422, и подробности наружу уходить не должны (см. комментарий в response.Error).
func TestCreateOrder_UnexpectedErrorIsInternal(t *testing.T) {
	t.Parallel()

	secret := "connection to db-host:5432 refused"
	svc := &fakeService{
		createFn: func(context.Context, *string, []domain.ItemRequest) (domain.Order, error) {
			return domain.Order{}, errors.New(secret)
		},
	}

	rec := httptest.NewRecorder()
	NewController(svc).CreateOrder(rec, newCreateRequest(`{"customer_id":"c-1","items":[]}`))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("статус = %d, ожидали %d", rec.Code, http.StatusInternalServerError)
	}
	env := decodeError(t, rec)
	if env.Error.Code != response.CodeInternal {
		t.Errorf("code = %q, ожидали %q", env.Error.Code, response.CodeInternal)
	}
	if strings.Contains(env.Error.Message, secret) {
		t.Errorf("внутренние подробности утекли клиенту: %q", env.Error.Message)
	}
}

func TestGetByID_Success(t *testing.T) {
	t.Parallel()

	want := sampleOrder()

	var gotID uuid.UUID
	svc := &fakeService{
		getFn: func(_ context.Context, id uuid.UUID) (domain.Order, error) {
			gotID = id
			return want, nil
		},
	}

	rec := httptest.NewRecorder()
	NewController(svc).GetByID(rec, newGetRequest(want.ID.String()))

	if rec.Code != http.StatusOK {
		t.Fatalf("статус = %d, ожидали %d; тело: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	assertJSONContentType(t, rec)

	if gotID != want.ID {
		t.Errorf("в сервис передан id %s, ожидали %s", gotID, want.ID)
	}

	var got domain.Order
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("тело не разбирается: %v", err)
	}
	if got.ID != want.ID || got.CustomerID != want.CustomerID || got.TotalCents != want.TotalCents ||
		len(got.Items) != 1 || got.Items[0] != want.Items[0] {
		t.Errorf("тело = %+v, ожидали %+v", got, want)
	}
}

func TestGetByID_InvalidID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		id   string
	}{
		{name: "не uuid", id: "not-a-uuid"},
		{name: "пустой", id: ""},
		{name: "число", id: "123"},
		{name: "обрезанный uuid", id: "0190a5c0-1111-7222-8333"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			svc := &fakeService{}
			rec := httptest.NewRecorder()
			NewController(svc).GetByID(rec, newGetRequest(tt.id))

			if rec.Code != http.StatusBadRequest {
				t.Errorf("статус = %d, ожидали %d", rec.Code, http.StatusBadRequest)
			}
			assertJSONContentType(t, rec)
			if env := decodeError(t, rec); env.Error.Code != response.CodeBadRequest {
				t.Errorf("code = %q, ожидали %q", env.Error.Code, response.CodeBadRequest)
			}
			if svc.getCalls != 0 {
				t.Errorf("сервис не должен вызываться при неверном id, вызовов: %d", svc.getCalls)
			}
		})
	}
}

func TestGetByID_NotFound(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
	}{
		{name: "прямая ошибка", err: domain.ErrNotFound},
		{name: "обёрнутая ошибка", err: fmt.Errorf("repo: %w", domain.ErrNotFound)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			svc := &fakeService{
				getFn: func(context.Context, uuid.UUID) (domain.Order, error) {
					return domain.Order{}, tt.err
				},
			}
			rec := httptest.NewRecorder()
			NewController(svc).GetByID(rec, newGetRequest(uuid.New().String()))

			if rec.Code != http.StatusNotFound {
				t.Fatalf("статус = %d, ожидали %d", rec.Code, http.StatusNotFound)
			}
			assertJSONContentType(t, rec)
			if env := decodeError(t, rec); env.Error.Code != response.CodeNotFound {
				t.Errorf("code = %q, ожидали %q", env.Error.Code, response.CodeNotFound)
			}
		})
	}
}

// Любая ошибка, кроме «не найдено», не должна превращаться в 404.
func TestGetByID_UnexpectedErrorIsInternal(t *testing.T) {
	t.Parallel()

	secret := "connection to db-host:5432 refused"
	svc := &fakeService{
		getFn: func(context.Context, uuid.UUID) (domain.Order, error) {
			return domain.Order{}, errors.New(secret)
		},
	}

	rec := httptest.NewRecorder()
	NewController(svc).GetByID(rec, newGetRequest(uuid.New().String()))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("статус = %d, ожидали %d", rec.Code, http.StatusInternalServerError)
	}
	env := decodeError(t, rec)
	if env.Error.Code != response.CodeInternal {
		t.Errorf("code = %q, ожидали %q", env.Error.Code, response.CodeInternal)
	}
	if strings.Contains(env.Error.Message, secret) {
		t.Errorf("внутренние подробности утекли клиенту: %q", env.Error.Message)
	}
}

// Роутинг через настоящий ServeMux проверяет, что PathValue("id") читается из шаблона маршрута.
func TestGetByID_ThroughServeMux(t *testing.T) {
	t.Parallel()

	want := sampleOrder()
	svc := &fakeService{
		getFn: func(_ context.Context, id uuid.UUID) (domain.Order, error) {
			if id != want.ID {
				return domain.Order{}, domain.ErrNotFound
			}
			return want, nil
		},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /orders/{id}", NewController(svc).GetByID)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/orders/"+want.ID.String(), nil))

	if rec.Code != http.StatusOK {
		t.Errorf("статус = %d, ожидали %d; тело: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
}
