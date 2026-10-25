package response

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Freazecake/orders/internal/requestid"
)

func newRequest() *http.Request {
	return httptest.NewRequest(http.MethodGet, "/", nil)
}

func TestJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		status  int
		payload any
		want    string
	}{
		{name: "объект и 200", status: http.StatusOK, payload: map[string]string{"status": "ok"}, want: `{"status":"ok"}`},
		{name: "объект и 201", status: http.StatusCreated, payload: map[string]int{"n": 1}, want: `{"n":1}`},
		{name: "срез", status: http.StatusOK, payload: []string{"a", "b"}, want: `["a","b"]`},
		{name: "строка", status: http.StatusOK, payload: "hello", want: `"hello"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			JSON(rec, newRequest(), tt.status, tt.payload)

			if rec.Code != tt.status {
				t.Errorf("статус = %d, ожидали %d", rec.Code, tt.status)
			}
			if got, want := rec.Header().Get("Content-Type"), "application/json; charset=utf-8"; got != want {
				t.Errorf("Content-Type = %q, ожидали %q", got, want)
			}
			if got := strings.TrimSpace(rec.Body.String()); got != tt.want {
				t.Errorf("тело = %q, ожидали %q", got, tt.want)
			}
		})
	}
}

func TestJSON_NilPayload(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	JSON(rec, newRequest(), http.StatusNoContent, nil)

	if rec.Code != http.StatusNoContent {
		t.Errorf("статус = %d, ожидали %d", rec.Code, http.StatusNoContent)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("тело должно быть пустым, получили %q", rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got == "" {
		t.Error("Content-Type должен выставляться и для пустого ответа")
	}
}

// Не параллельный: подменяет slog.Default.
func TestJSON_EncodeErrorIsLoggedAndStatusKept(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	rec := httptest.NewRecorder()
	// chan не кодируется в JSON.
	JSON(rec, newRequest(), http.StatusOK, make(chan int))

	if rec.Code != http.StatusOK {
		t.Errorf("заголовки уже отправлены, статус должен остаться %d, получили %d", http.StatusOK, rec.Code)
	}
	if !strings.Contains(buf.String(), "не удалось закодировать ответ") {
		t.Errorf("ожидали запись об ошибке в логе, лог: %q", buf.String())
	}
}

type testEnvelope struct {
	Error map[string]any `json:"error"`
}

func TestError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		status  int
		code    string
		message string
	}{
		{name: "bad_request", status: http.StatusBadRequest, code: CodeBadRequest, message: "битый json"},
		{name: "validation", status: http.StatusUnprocessableEntity, code: CodeValidation, message: "invalid order"},
		{name: "not_found", status: http.StatusNotFound, code: CodeNotFound, message: "заказ не найден"},
		{name: "internal", status: http.StatusInternalServerError, code: CodeInternal, message: "внутренняя ошибка сервиса"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			Error(rec, newRequest(), tt.status, tt.code, tt.message)

			if rec.Code != tt.status {
				t.Errorf("статус = %d, ожидали %d", rec.Code, tt.status)
			}
			if got, want := rec.Header().Get("Content-Type"), "application/json; charset=utf-8"; got != want {
				t.Errorf("Content-Type = %q, ожидали %q", got, want)
			}

			var env testEnvelope
			if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
				t.Fatalf("тело не разбирается как JSON: %v; тело: %q", err, rec.Body.String())
			}
			if env.Error == nil {
				t.Fatalf("в теле нет ключа error: %q", rec.Body.String())
			}
			if got := env.Error["code"]; got != tt.code {
				t.Errorf("code = %v, ожидали %q", got, tt.code)
			}
			if got := env.Error["message"]; got != tt.message {
				t.Errorf("message = %v, ожидали %q", got, tt.message)
			}
		})
	}
}

// Без request id в контексте поле request_id не должно попадать в ответ (omitempty).
func TestError_NoRequestIDOmitted(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	Error(rec, newRequest(), http.StatusBadRequest, CodeBadRequest, "x")

	var env testEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("тело не разбирается: %v", err)
	}
	if _, ok := env.Error["request_id"]; ok {
		t.Errorf("request_id не должен присутствовать без id в контексте: %v", env.Error)
	}
}

// Конверт ошибки — только error.{code,message[,request_id]}: лишних ключей на верхнем уровне нет.
func TestError_EnvelopeShape(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	Error(rec, newRequest(), http.StatusNotFound, CodeNotFound, "нет")

	var top map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &top); err != nil {
		t.Fatalf("тело не разбирается: %v", err)
	}
	if len(top) != 1 {
		t.Errorf("на верхнем уровне должен быть один ключ error, получили %d: %v", len(top), top)
	}
	if _, ok := top["error"]; !ok {
		t.Error("ключ error отсутствует")
	}
}

// Коды ошибок — публичный контракт: менять их можно только через депрекацию.
func TestErrorCodesAreStable(t *testing.T) {
	t.Parallel()

	tests := []struct{ name, got, want string }{
		{"CodeBadRequest", CodeBadRequest, "bad_request"},
		{"CodeValidation", CodeValidation, "validation_error"},
		{"CodeNotFound", CodeNotFound, "not_found"},
		{"CodeInternal", CodeInternal, "internal_error"},
	}
	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("%s = %q, ожидали %q", tt.name, tt.got, tt.want)
		}
	}
}

// Если в контексте запроса есть request id, он попадает в ответ.
func TestError_IncludesRequestID(t *testing.T) {
	t.Parallel()

	req := newRequest()
	req = req.WithContext(requestid.NewContext(req.Context(), "req-123"))

	rec := httptest.NewRecorder()
	Error(rec, req, http.StatusBadRequest, CodeBadRequest, "x")

	var env testEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("тело не разбирается: %v", err)
	}
	if got := env.Error["request_id"]; got != "req-123" {
		t.Errorf("request_id = %v, ожидали %q", got, "req-123")
	}
}
