package service

import (
	"context"
	"strings"
	"testing"
)

// TestПроверкаБезПодключения: пока прокси не поднят, проверять нечего.
// CheckExitIP обязан вернуть ошибку сразу, не открывая ни одного
// соединения, — иначе кнопка «Проверить IP» на неподключённом
// приложении молча висла бы до таймаута.
func TestПроверкаБезПодключения(t *testing.T) {
	s, err := New(Options{
		StateDir: t.TempDir(),
		Runtime:  &поддельныйЗапуск{},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if адрес := s.SocksAddress(); адрес != "" {
		t.Fatalf("до подключения адрес прокси должен быть пуст, получено %q", адрес)
	}

	ip, черезTor, err := s.CheckExitIP(context.Background())
	if err == nil {
		t.Fatal("ожидалась ошибка «прокси не поднят», её нет")
	}
	if ip != "" || черезTor {
		t.Fatalf("при ошибке результат должен быть пустым, получено ip=%q tor=%v", ip, черезTor)
	}
	if !strings.Contains(err.Error(), "подключитесь") {
		t.Errorf("ошибка должна подсказывать подключиться, получено: %v", err)
	}
}
