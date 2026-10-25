// Package internal собирает маршруты приложения в одном месте.
//
// Контроллеры лежат по пакетам, а карта URL — здесь: по этому файлу видно
// весь публичный интерфейс сервиса, не открывая двадцать пакетов.
package internal

import (
	"log/slog"
	"net/http"

	healthController "github.com/Freazecake/orders/internal/controllers/http/health"
	orderController "github.com/Freazecake/orders/internal/controllers/http/order"
	"github.com/Freazecake/orders/internal/middlewares"
)

// InitRoutes собирает маршруты и оборачивает их общей обвязкой.
//
// Используется стандартный http.ServeMux: с Go 1.22 он умеет метод и
// wildcard в паттерне («POST /orders», «GET /orders/{id}»), поэтому внешний
// роутер на онбординге не нужен. Значение wildcard достаётся из запроса
// через r.PathValue("id").
func InitRoutes(logger *slog.Logger, health *healthController.Controller, order *orderController.Controller) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", health.Health)
	mux.HandleFunc("POST /orders", order.CreateOrder)
	mux.HandleFunc("GET /orders/{id}", order.GetByID)

	// Порядок обёрток важен, и читается он снизу вверх: request id появляется
	// первым, чтобы попасть и в лог, и в ответ; логирование идёт следующим и
	// потому видит любой запрос, включая тот, в котором хендлер паникует;
	// recover — ближе всех к хендлеру, поэтому его ответ 500 успевает
	// записаться в статус и попасть в ту же строку лога.
	var h http.Handler = mux
	h = middlewares.Recover(logger, h)
	h = middlewares.Logging(logger, h)
	h = middlewares.RequestID(h)

	return h
}
