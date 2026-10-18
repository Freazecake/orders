// Команда orders — HTTP-сервис заказов, сквозной проект курса.
//
// main держит только composition root: читает конфигурацию, собирает
// зависимости, запускает сервер и корректно его останавливает. Логики здесь
// нет и быть не должно — её невозможно протестировать.
package main

import (
	"log/slog"
	"os"

	"github.com/Freazecake/orders/internal"
	"github.com/Freazecake/orders/internal/config"
	healthController "github.com/Freazecake/orders/internal/controllers/http/health"
	orderController "github.com/Freazecake/orders/internal/controllers/http/order"
	repo "github.com/Freazecake/orders/internal/repositories/order"
	httpServer "github.com/Freazecake/orders/internal/servers/http"
	service "github.com/Freazecake/orders/internal/services/order"
)

func main() {
	if err := run(); err != nil {
		slog.Error("сервис остановлен с ошибкой", slog.Any("error", err))
		os.Exit(1)
	}
}

// run возвращает ошибку вместо того, чтобы звать os.Exit по всему коду:
// так путь завершения один и его видно целиком.
func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	slog.SetDefault(logger)

	// Composition root: зависимости создаются здесь и передаются явно, снизу
	// вверх — хранилище в сервис, сервис в контроллер, контроллер в маршруты.
	// Никаких глобальных переменных и пакетов-синглтонов.
	repo := repo.New()

	svc := service.New(repo)

	health := healthController.NewController()
	order := orderController.NewController(svc)

	srv := httpServer.NewServer(cfg.HTTPAddr, internal.InitRoutes(logger, health, order), logger, cfg.ShutdownTimeout)

	return srv.Run()
}
