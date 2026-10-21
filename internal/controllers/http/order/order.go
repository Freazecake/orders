// Package order — HTTP-контроллер заказов: разбор запроса, вызов сервиса,
// перевод доменных ошибок в коды ответа.
//
// Единый формат ответа и коды ошибок — в соседнем пакете response.
package order

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/google/uuid"

	"github.com/Freazecake/orders/internal/controllers/http/response"
	domain "github.com/Freazecake/orders/internal/models/order"
)

type Service interface {
	Create(ctx context.Context, customerID string, items []domain.ItemRequest) (domain.Order, error)
	GetByID(ctx context.Context, id uuid.UUID) (domain.Order, error)
}

type Controller struct {
	svc Service
}

func NewController(svc Service) *Controller {
	return &Controller{svc: svc}
}

func (c *Controller) CreateOrder(w http.ResponseWriter, r *http.Request) {
	var req domain.CreateOrderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, r, http.StatusBadRequest, response.CodeBadRequest, err.Error())
		return
	}

	ctx := r.Context()
	o, err := c.svc.Create(ctx, *req.CustomerID, req.Items)
	if err != nil {
		response.Error(w, r, http.StatusUnprocessableEntity, response.CodeValidation, err.Error())
		return
	}

	w.Header().Set("Location", "/orders/"+o.ID.String())
	response.JSON(w, r, http.StatusCreated, o)
}

func (c *Controller) GetByID(w http.ResponseWriter, r *http.Request) {
	id_s := r.PathValue("id")
	id, err := uuid.Parse(id_s)
	if err != nil {
		response.Error(w, r, http.StatusBadRequest, response.CodeBadRequest, err.Error())
		return
	}

	ctx := r.Context()
	o, err := c.svc.GetByID(ctx, id)
	if err != nil {
		response.Error(w, r, http.StatusNotFound, response.CodeNotFound, err.Error())
		return
	}

	response.JSON(w, r, http.StatusOK, o)
}
