package main

import (
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Freazecake/orders/internal/requestid"
)

// Тесты не параллельные: они меняют окружение, slog.Default и шлют сигнал процессу.

// setEnv снимает все переменные сервиса и выставляет только нужные кейсу.
func setEnv(t *testing.T, env map[string]string) {
	t.Helper()

	for _, key := range []string{"ORDERS_HTTP_ADDR", "ORDERS_SHUTDOWN_TIMEOUT", "ORDERS_LOG_LEVEL"} {
		t.Setenv(key, "")
		if err := os.Unsetenv(key); err != nil {
			t.Fatalf("не удалось снять %s: %v", key, err)
		}
	}
	for k, v := range env {
		t.Setenv(k, v)
	}
}

// keepDefaultLogger возвращает slog.Default на место: run() его подменяет.
func keepDefaultLogger(t *testing.T) {
	t.Helper()
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })
}

func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("не удалось занять порт: %v", err)
	}
	defer ln.Close()
	return ln.Addr().String()
}

func waitUp(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", addr, 50*time.Millisecond)
		if err == nil {
			_ = c.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("сервис на %s не поднялся", addr)
}

func startRun(t *testing.T) <-chan error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- run() }()
	return done
}

func waitRun(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("run() не завершился вовремя")
		return nil
	}
}

func TestRun_InvalidConfig(t *testing.T) {
	keepDefaultLogger(t)

	tests := []struct {
		name    string
		env     map[string]string
		wantMsg string
	}{
		{
			name:    "некорректный таймаут остановки",
			env:     map[string]string{"ORDERS_SHUTDOWN_TIMEOUT": "быстро"},
			wantMsg: "ORDERS_SHUTDOWN_TIMEOUT",
		},
		{
			name:    "неизвестный уровень логирования",
			env:     map[string]string{"ORDERS_LOG_LEVEL": "trace"},
			wantMsg: "ORDERS_LOG_LEVEL",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setEnv(t, tt.env)

			done := startRun(t)
			err := waitRun(t, done)

			if err == nil {
				t.Fatal("ожидали ошибку конфигурации, получили nil")
			}
			if !strings.Contains(err.Error(), tt.wantMsg) {
				t.Errorf("ошибка = %q, ожидали упоминание %q", err, tt.wantMsg)
			}
		})
	}
}

// Адрес уже занят: run() должен вернуть ошибку, а не зависнуть.
func TestRun_AddressInUse(t *testing.T) {
	keepDefaultLogger(t)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()

	setEnv(t, map[string]string{"ORDERS_HTTP_ADDR": ln.Addr().String()})

	if err := waitRun(t, startRun(t)); err == nil {
		t.Error("ожидали ошибку из-за занятого адреса, получили nil")
	}
}

// Сквозной сценарий: run() собирает репозиторий, сервис, контроллеры и роуты,
// поднимает сервер, обслуживает запросы и штатно останавливается по SIGTERM.
func TestRun_EndToEnd(t *testing.T) {
	keepDefaultLogger(t)

	addr := freeAddr(t)
	setEnv(t, map[string]string{
		"ORDERS_HTTP_ADDR":        addr,
		"ORDERS_SHUTDOWN_TIMEOUT": "2s",
		"ORDERS_LOG_LEVEL":        "error", // чтобы не шуметь в выводе тестов
	})

	done := startRun(t)
	waitUp(t, addr)
	base := "http://" + addr

	t.Run("healthz", func(t *testing.T) {
		resp, err := http.Get(base + "/healthz")
		if err != nil {
			t.Fatalf("GET /healthz: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("статус = %d, ожидали %d", resp.StatusCode, http.StatusOK)
		}
		if resp.Header.Get(requestid.Header) == "" {
			t.Errorf("заголовок %s пустой — обвязка не подключена", requestid.Header)
		}
	})

	var location string
	var createdID string

	t.Run("создание заказа", func(t *testing.T) {
		body := `{"customer_id":"c-1","items":[{"sku":"A","qty":2,"price_cents":150},{"sku":"B","qty":1,"price_cents":99}]}`
		resp, err := http.Post(base+"/orders", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatalf("POST /orders: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusCreated {
			b, _ := io.ReadAll(resp.Body)
			t.Fatalf("статус = %d, ожидали %d; тело: %s", resp.StatusCode, http.StatusCreated, b)
		}
		location = resp.Header.Get("Location")
		if !strings.HasPrefix(location, "/orders/") {
			t.Fatalf("Location = %q, ожидали /orders/<id>", location)
		}

		var got struct {
			ID         string `json:"id"`
			CustomerID string `json:"customer_id"`
			Status     string `json:"status"`
			TotalCents int    `json:"total_cents"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
			t.Fatalf("тело не разбирается: %v", err)
		}
		createdID = got.ID
		if got.ID == "" || location != "/orders/"+got.ID {
			t.Errorf("Location %q не соответствует id %q", location, got.ID)
		}
		if got.CustomerID != "c-1" || got.Status != "created" || got.TotalCents != 2*150+99 {
			t.Errorf("заказ = %+v", got)
		}
	})

	t.Run("получение созданного заказа", func(t *testing.T) {
		if location == "" {
			t.Skip("заказ не был создан")
		}
		resp, err := http.Get(base + location)
		if err != nil {
			t.Fatalf("GET %s: %v", location, err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("статус = %d, ожидали %d", resp.StatusCode, http.StatusOK)
		}
		var got struct {
			ID         string `json:"id"`
			TotalCents int    `json:"total_cents"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
			t.Fatalf("тело не разбирается: %v", err)
		}
		if got.ID != createdID || got.TotalCents != 399 {
			t.Errorf("получили %+v, ожидали id=%s total=399", got, createdID)
		}
	})

	t.Run("несуществующий заказ — 404", func(t *testing.T) {
		resp, err := http.Get(base + "/orders/0190a5c0-1111-7222-8333-444455556666")
		if err != nil {
			t.Fatalf("GET: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("статус = %d, ожидали %d", resp.StatusCode, http.StatusNotFound)
		}
	})

	t.Run("невалидный заказ — 422", func(t *testing.T) {
		body := `{"customer_id":"c-1","items":[{"sku":"A","qty":0,"price_cents":1}]}`
		resp, err := http.Post(base+"/orders", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatalf("POST: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("статус = %d, ожидали %d", resp.StatusCode, http.StatusUnprocessableEntity)
		}
	})

	t.Run("неизвестный маршрут — 404", func(t *testing.T) {
		resp, err := http.Get(base + "/nope")
		if err != nil {
			t.Fatalf("GET: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("статус = %d, ожидали %d", resp.StatusCode, http.StatusNotFound)
		}
	})

	t.Run("неподдерживаемый метод — 405", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodDelete, base+"/orders", nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("DELETE: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("статус = %d, ожидали %d", resp.StatusCode, http.StatusMethodNotAllowed)
		}
	})

	// Штатная остановка.
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatalf("не удалось послать SIGTERM: %v", err)
	}
	if err := waitRun(t, done); err != nil {
		t.Errorf("run() вернул ошибку при штатной остановке: %v", err)
	}
	if c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond); err == nil {
		_ = c.Close()
		t.Error("сервис продолжает принимать соединения после остановки")
	}
}

// run() устанавливает логгер по умолчанию с уровнем из конфигурации.
func TestRun_SetsDefaultLoggerLevel(t *testing.T) {
	keepDefaultLogger(t)

	addr := freeAddr(t)
	setEnv(t, map[string]string{
		"ORDERS_HTTP_ADDR":        addr,
		"ORDERS_SHUTDOWN_TIMEOUT": "1s",
		"ORDERS_LOG_LEVEL":        "error",
	})

	done := startRun(t)
	waitUp(t, addr)

	if slog.Default().Enabled(t.Context(), slog.LevelInfo) {
		t.Error("при ORDERS_LOG_LEVEL=error уровень info должен быть выключен")
	}
	if !slog.Default().Enabled(t.Context(), slog.LevelError) {
		t.Error("при ORDERS_LOG_LEVEL=error уровень error должен быть включён")
	}

	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatalf("не удалось послать SIGTERM: %v", err)
	}
	if err := waitRun(t, done); err != nil {
		t.Errorf("run() вернул ошибку: %v", err)
	}
}
