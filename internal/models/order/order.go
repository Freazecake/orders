// Package order — доменная модель заказа.
//
// Здесь живут типы предметной области и правила, которые к ним относятся.
// Пакет ничего не знает про HTTP, JSON и базу данных: он не импортирует
// net/http и не содержит тегов сериализации. Это проверяется на приёмке.
package order

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

type Order struct {
	ID         uuid.UUID `json:"id"`
	CustomerID string    `json:"customer_id"`
	Status     Status    `json:"status"`
	Items      []Item    `json:"items"`
	TotalCents int       `json:"total_cents"`
	CreatedAt  time.Time `json:"created_at"`
}

type Item struct {
	SKU        string `json:"sku"`
	QTY        int    `json:"qty"`
	PriceCents int    `json:"price_cents"`
}

type Status string

var (
	ErrNotFound     = errors.New("order not found")
	ErrInvalidOrder = errors.New("invalid order")
)

var (
	StatusCreated Status = "created"
)

type CreateOrderRequest struct {
	CustomerID *string       `json:"customer_id"`
	Items      []ItemRequest `json:"items"`
}

type ItemRequest struct {
	SKU        *string `json:"sku"`
	Qty        *int    `json:"qty"`
	PriceCents *int    `json:"price_cents"`
}
