package requestid

import (
	"context"
	"regexp"
	"sync"
	"testing"
)

func TestFromContext(t *testing.T) {
	t.Parallel()

	type otherKey int

	tests := []struct {
		name string
		ctx  context.Context
		want string
	}{
		{name: "пустой контекст", ctx: context.Background(), want: ""},
		{name: "id положен через NewContext", ctx: NewContext(context.Background(), "abc"), want: "abc"},
		{name: "пустой id", ctx: NewContext(context.Background(), ""), want: ""},
		{name: "чужой ключ с тем же числовым значением", ctx: context.WithValue(context.Background(), otherKey(0), "alien"), want: ""},
		{name: "значение не строка", ctx: context.WithValue(context.Background(), key, 42), want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := FromContext(tt.ctx); got != tt.want {
				t.Errorf("FromContext = %q, ожидали %q", got, tt.want)
			}
		})
	}
}

func TestNewContext_OverridesAndKeepsParent(t *testing.T) {
	t.Parallel()

	type parentKey struct{}
	parent := context.WithValue(context.Background(), parentKey{}, "p")

	first := NewContext(parent, "one")
	second := NewContext(first, "two")

	if got := FromContext(first); got != "one" {
		t.Errorf("первый контекст = %q, ожидали %q", got, "one")
	}
	if got := FromContext(second); got != "two" {
		t.Errorf("вложенный контекст = %q, ожидали %q", got, "two")
	}
	if got := second.Value(parentKey{}); got != "p" {
		t.Errorf("значения родителя потеряны, получили %v", got)
	}
}

func TestNew_Format(t *testing.T) {
	t.Parallel()

	re := regexp.MustCompile(`^[0-9a-f]{16}$`)
	for i := 0; i < 50; i++ {
		if id := New(); !re.MatchString(id) {
			t.Fatalf("id %q не похож на 16 hex-символов", id)
		}
	}
}

func TestNew_Unique(t *testing.T) {
	t.Parallel()

	const n = 1000
	var (
		mu   sync.Mutex
		seen = make(map[string]struct{}, n)
		wg   sync.WaitGroup
	)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id := New()
			mu.Lock()
			seen[id] = struct{}{}
			mu.Unlock()
		}()
	}
	wg.Wait()

	if len(seen) != n {
		t.Errorf("уникальных id %d из %d", len(seen), n)
	}
}

func TestHeader(t *testing.T) {
	t.Parallel()

	// Имя заголовка — часть контракта с клиентами.
	if Header != "X-Request-Id" {
		t.Errorf("Header = %q, ожидали %q", Header, "X-Request-Id")
	}
}
