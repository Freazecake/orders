package health

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewController(t *testing.T) {
	t.Parallel()

	if NewController() == nil {
		t.Fatal("NewController вернул nil")
	}
}

func TestController_Health(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)

	NewController().Health(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("статус = %d, ожидали %d", rec.Code, http.StatusOK)
	}
	if got, want := rec.Header().Get("Content-Type"), "application/json; charset=utf-8"; got != want {
		t.Errorf("Content-Type = %q, ожидали %q", got, want)
	}

	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("тело не разбирается: %v; тело: %q", err, rec.Body.String())
	}
	if len(body) != 1 || body["status"] != "ok" {
		t.Errorf("тело = %v, ожидали {status: ok}", body)
	}
}
