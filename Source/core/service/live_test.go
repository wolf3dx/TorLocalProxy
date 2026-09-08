//go:build integration

// Живая проверка на настоящей сети: настоящий tor, настоящие мосты
// obfs4, настоящий выход в интернет через поднятый прокси.
//
// За тегом сборки и вдобавок требует переменную окружения с путём к
// файлу мостов — иначе тест пропускается. Строки мостов в репозиторий
// не попадают и в вывод теста не печатаются: файл лежит вне проекта.
//
//	TORPROXY_BRIDGES_FILE="...\мосты.txt" go test -tags integration -run Живая ./core/service/
package service

import (
	"context"
	"os"
	"testing"
	"time"

	"gitlab.com/vkandreevich/torlocalproxy/core/torrun/external"
)

// шумныйНаблюдатель показывает ход подключения в выводе теста — ровно
// то, что увидит пользователь в плашке статуса и в журнале.
type шумныйНаблюдатель struct{ t *testing.T }

func (н шумныйНаблюдатель) OnBootstrap(percent int, phase string) {
	н.t.Logf("  %3d%%  %s", percent, phase)
}
func (н шумныйНаблюдатель) OnLog(line string) { н.t.Logf("  %s", line) }
func (н шумныйНаблюдатель) OnState(state string) {
	н.t.Logf("состояние: %s", state)
}

func TestЖиваяСвязьЧерезМосты(t *testing.T) {
	файл := os.Getenv("TORPROXY_BRIDGES_FILE")
	if файл == "" {
		t.Skip("не задан TORPROXY_BRIDGES_FILE — живая проверка пропущена")
	}
	данные, err := os.ReadFile(файл)
	if err != nil {
		t.Fatalf("файл мостов не прочитался: %v", err)
	}

	// Каталог состояния можно задать снаружи: при разборе отказа нужно
	// заглянуть в torrc и tor.log, а временный каталог теста исчезает.
	//
	// Обычный t.TempDir() тут не годится: он берёт имя из имени теста, а
	// оно по-русски — tor такой путь под Windows не откроет вовсе.
	// Короткое имя 8.3 в torrc помогает, но самого каталога с кириллицей
	// в живой проверке лучше не создавать.
	каталог := os.Getenv("TORPROXY_STATE_DIR")
	if каталог == "" {
		свой, err := os.MkdirTemp("", "torproxy-live-")
		if err != nil {
			t.Fatalf("каталог состояния: %v", err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(свой) })
		каталог = свой
	}
	s, err := New(Options{
		StateDir: каталог,
		Runtime:  external.New(external.Options{}),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = s.Disconnect() })

	ctx, отменить := context.WithTimeout(context.Background(), 4*time.Minute)
	defer отменить()

	начало := time.Now()
	if err := s.Connect(ctx, string(данные), шумныйНаблюдатель{t}); err != nil {
		t.Fatalf("подключиться не удалось: %v", err)
	}
	t.Logf("подключение заняло %s", time.Since(начало).Round(time.Second))

	адрес := s.SocksAddress()
	if адрес == "" {
		t.Fatal("прокси не сообщил адрес")
	}
	t.Logf("прокси SOCKS5: %s — это и вписывается в чужие приложения", адрес)

	// Главная проверка: через прокси действительно ходит трафик, и
	// выходит он из сети Tor. Идёт через тот же CheckExitIP, что стоит за
	// кнопкой «Проверить IP» в приложении, — тест проверяет ровно то, чем
	// пользуется человек.
	ip, черезTor, err := s.CheckExitIP(ctx)
	if err != nil {
		t.Fatalf("через прокси не удалось выйти в сеть: %v", err)
	}
	t.Logf("внешний адрес: %s, сеть Tor: %v", ip, черезTor)
	if !черезTor {
		t.Errorf("трафик пошёл мимо Tor")
	}

	// Смена цепочки — кнопка «новая цепочка» в приложении.
	if err := s.NewIdentity(ctx); err != nil {
		t.Errorf("NewIdentity: %v", err)
	}

	if err := s.Disconnect(); err != nil {
		t.Errorf("Disconnect: %v", err)
	}
	if s.State() != StateIdle {
		t.Errorf("после отключения состояние %q", s.State())
	}
}
