package http

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// syncBuffer — буфер для логов, безопасный для параллельного доступа.
type syncBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func newLogger(w io.Writer) *slog.Logger {
	return slog.New(slog.NewTextHandler(w, nil))
}

// freeAddr возвращает свободный адрес 127.0.0.1:порт.
func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("не удалось занять порт: %v", err)
	}
	defer ln.Close()
	return ln.Addr().String()
}

// waitUp ждёт, пока сервер начнёт принимать соединения.
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
	t.Fatalf("сервер на %s не поднялся", addr)
}

func TestNewServer_Configuration(t *testing.T) {
	t.Parallel()

	handler := http.NewServeMux()
	logger := newLogger(io.Discard)

	s := NewServer(":8080", handler, logger, 7*time.Second)

	if s == nil || s.server == nil {
		t.Fatal("NewServer вернул пустой сервер")
	}
	if s.server.Addr != ":8080" {
		t.Errorf("Addr = %q, ожидали %q", s.server.Addr, ":8080")
	}
	if s.server.Handler != http.Handler(handler) {
		t.Error("Handler не тот, что передали")
	}
	if s.logger != logger {
		t.Error("logger не тот, что передали")
	}
	if s.shutdownTimeout != 7*time.Second {
		t.Errorf("shutdownTimeout = %v, ожидали %v", s.shutdownTimeout, 7*time.Second)
	}
}

// Таймауты обязательны: без них медленный клиент держит соединение вечно.
func TestNewServer_TimeoutsAreSet(t *testing.T) {
	t.Parallel()

	srv := NewServer(":0", http.NewServeMux(), newLogger(io.Discard), time.Second).server

	tests := []struct {
		name string
		got  time.Duration
		want time.Duration
	}{
		{"ReadHeaderTimeout", srv.ReadHeaderTimeout, 5 * time.Second},
		{"ReadTimeout", srv.ReadTimeout, 15 * time.Second},
		{"WriteTimeout", srv.WriteTimeout, 15 * time.Second},
		{"IdleTimeout", srv.IdleTimeout, 60 * time.Second},
	}
	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("%s = %v, ожидали %v", tt.name, tt.got, tt.want)
		}
	}
}

// Адрес уже занят: Run должен вернуть ошибку, а не зависнуть.
func TestRun_ListenError(t *testing.T) {
	t.Parallel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()

	s := NewServer(ln.Addr().String(), http.NewServeMux(), newLogger(io.Discard), time.Second)

	done := make(chan error, 1)
	go func() { done <- s.Run() }()

	select {
	case err := <-done:
		if err == nil {
			t.Error("ожидали ошибку из-за занятого адреса")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run завис при занятом адресе")
	}
}

// Не параллельный: шлёт SIGTERM самому процессу. Run перехватывает его через
// signal.NotifyContext, поэтому тестовый процесс не умирает.
func TestRun_GracefulShutdownOnSignal(t *testing.T) {
	addr := freeAddr(t)

	logs := &syncBuffer{}
	mux := http.NewServeMux()
	mux.HandleFunc("/ping", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "pong")
	})
	s := NewServer(addr, mux, newLogger(logs), 2*time.Second)

	done := make(chan error, 1)
	go func() { done <- s.Run() }()
	waitUp(t, addr)

	// Пока сервер работает — отвечает.
	resp, err := http.Get("http://" + addr + "/ping")
	if err != nil {
		t.Fatalf("GET /ping: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != "pong" {
		t.Errorf("ответ = %d %q, ожидали 200 \"pong\"", resp.StatusCode, body)
	}

	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatalf("не удалось послать SIGTERM: %v", err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run вернул ошибку при штатной остановке: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run не завершился после SIGTERM")
	}

	out := logs.String()
	for _, want := range []string{"сервис запущен", "получен сигнал остановки", "сервис остановлен"} {
		if !strings.Contains(out, want) {
			t.Errorf("в логе нет %q; лог:\n%s", want, out)
		}
	}

	// После остановки порт больше не слушается.
	if c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond); err == nil {
		_ = c.Close()
		t.Error("сервер продолжает принимать соединения после остановки")
	}
}

// Активный запрос должен доработать во время graceful shutdown.
func TestRun_ShutdownWaitsForActiveRequest(t *testing.T) {
	addr := freeAddr(t)

	started := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("/slow", func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		time.Sleep(300 * time.Millisecond)
		_, _ = io.WriteString(w, "done")
	})
	s := NewServer(addr, mux, newLogger(io.Discard), 3*time.Second)

	done := make(chan error, 1)
	go func() { done <- s.Run() }()
	waitUp(t, addr)

	type result struct {
		body string
		err  error
	}
	resCh := make(chan result, 1)
	go func() {
		resp, err := http.Get("http://" + addr + "/slow")
		if err != nil {
			resCh <- result{err: err}
			return
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		resCh <- result{body: string(b)}
	}()

	<-started
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatalf("не удалось послать SIGTERM: %v", err)
	}

	select {
	case r := <-resCh:
		if r.err != nil {
			t.Errorf("активный запрос оборвался: %v", r.err)
		} else if r.body != "done" {
			t.Errorf("тело = %q, ожидали %q", r.body, "done")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("активный запрос не завершился")
	}

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run вернул ошибку: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run не завершился")
	}
}

// Запрос дольше shutdownTimeout: Shutdown возвращает ошибку контекста, Run её пробрасывает.
func TestRun_ShutdownTimeoutExceeded(t *testing.T) {
	addr := freeAddr(t)

	started := make(chan struct{})
	release := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("/hang", func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
	})
	s := NewServer(addr, mux, newLogger(io.Discard), 100*time.Millisecond)

	done := make(chan error, 1)
	go func() { done <- s.Run() }()
	waitUp(t, addr)

	go func() {
		resp, err := http.Get("http://" + addr + "/hang")
		if err == nil {
			_ = resp.Body.Close()
		}
	}()

	<-started
	t.Cleanup(func() { close(release) })

	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatalf("не удалось послать SIGTERM: %v", err)
	}

	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("ошибка = %v, ожидали %v", err, context.DeadlineExceeded)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run не завершился по таймауту shutdown")
	}
}
