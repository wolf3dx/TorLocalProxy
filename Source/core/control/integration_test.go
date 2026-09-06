//go:build integration

// Тесты против заглушки tor из прототипа (Prototype-Python/tests/fake_tor.py).
// За тегом сборки, потому что требуют Python и поднимают сокеты, а
// `go test` без флагов не должен требовать ничего (PLAN.md, раздел 5.1).
//
// Запуск:
//
//	go test -tags integration ./core/control/
package control

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"gitlab.com/vkandreevich/torlocalproxy/core/bridges"
	"gitlab.com/vkandreevich/torlocalproxy/core/torconf"
)

const отпечаток = "0123456789ABCDEF0123456789ABCDEF01234567"

// поднятьЗаглушку запускает fake_tor.py по сгенерированному нами torrc и
// возвращает адрес control-порта. Заодно это проверка torconf: заглушка
// разбирает конфиг тем же кодом, что и в прототипе, и на кривой торрк
// просто не поднимется.
func поднятьЗаглушку(t *testing.T, окружение ...string) string {
	t.Helper()

	каталог := t.TempDir()
	файлПорта := filepath.Join(каталог, "control_port")
	торрк := filepath.Join(каталог, "torrc")

	мосты, проблемы := bridges.Parse("obfs4 192.0.2.1:9443 " + отпечаток + " cert=AAA iat-mode=0")
	if len(проблемы) != 0 {
		t.Fatalf("тестовые мосты не разобрались: %v", проблемы)
	}

	содержимое := torconf.Build(torconf.Config{
		DataDir:     каталог,
		LogFile:     filepath.Join(каталог, "tor.log"),
		ControlFile: файлПорта,
		Bridges:     мосты,
		Plugins: []torconf.Plugin{
			{Transports: []string{"obfs4"}, Address: "127.0.0.1:41000"},
		},
	})
	if err := os.WriteFile(торрк, []byte(содержимое), 0o600); err != nil {
		t.Fatalf("не записался torrc: %v", err)
	}

	заглушка := filepath.Join("..", "..", "Prototype-Python", "tests", "fake_tor.py")
	if _, err := os.Stat(заглушка); err != nil {
		t.Skipf("заглушка tor не найдена: %v", err)
	}

	процесс := exec.Command(питон(t), заглушка, "-f", торрк)
	процесс.Env = append(os.Environ(), окружение...)
	процесс.Stderr = os.Stderr
	if err := процесс.Start(); err != nil {
		t.Fatalf("заглушка не запустилась: %v", err)
	}
	t.Cleanup(func() {
		_ = процесс.Process.Kill()
		_, _ = процесс.Process.Wait()
	})

	return ждатьАдресControl(t, файлПорта)
}

// ждатьАдресControl ждёт файл, который tor пишет в самом конце запуска:
// раньше него control-порт слушать некому.
func ждатьАдресControl(t *testing.T, файлПорта string) string {
	t.Helper()
	срок := time.Now().Add(20 * time.Second)
	for time.Now().Before(срок) {
		данные, err := os.ReadFile(файлПорта)
		if err == nil {
			строка := strings.TrimSpace(string(данные))
			if адрес, найдено := strings.CutPrefix(строка, "PORT="); найдено {
				return адрес
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("заглушка не написала %s за 20 с", файлПорта)
	return ""
}

func питон(t *testing.T) string {
	t.Helper()
	кандидаты := []string{"python3", "python"}
	if runtime.GOOS == "windows" {
		кандидаты = []string{"python", "python3", "py"}
	}
	for _, имя := range кандидаты {
		if путь, err := exec.LookPath(имя); err == nil {
			return путь
		}
	}
	t.Skip("Python не найден — интеграционный тест пропущен")
	return ""
}

// TestПолныйЦиклПротивЗаглушки — приёмка этапа 2: SAFECOOKIE, события
// 0 → 100, NEWNYM и GETINFO net/listeners/socks на живом соединении.
func TestПолныйЦиклПротивЗаглушки(t *testing.T) {
	адрес := поднятьЗаглушку(t, "FAKE_TOR_DELAY=0.15")

	ctx, отменить := context.WithTimeout(context.Background(), 60*time.Second)
	defer отменить()

	клиент, err := Dial(ctx, адрес, Options{})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer клиент.Close()

	// Заглушка объявляет METHODS=COOKIE,SAFECOOKIE — значит идёт
	// челлендж-ответ, а не голая передача cookie.
	if err := клиент.Authenticate(ctx); err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if err := клиент.TakeOwnership(ctx); err != nil {
		t.Fatalf("TakeOwnership: %v", err)
	}

	события, err := клиент.WatchBootstrap(ctx)
	if err != nil {
		t.Fatalf("WatchBootstrap: %v", err)
	}

	// Сохраняем то, что проходит мимо, чтобы проверить не только итог,
	// но и что фазы приходили переведёнными.
	собранные := make(chan Bootstrap, 64)
	видели := make([]Bootstrap, 0, 16)
	готово := make(chan struct{})
	go func() {
		defer close(готово)
		defer close(собранные)
		for шаг := range события {
			видели = append(видели, шаг)
			собранные <- шаг
		}
	}()

	итог, err := Wait(ctx, собранные, WaitOptions{Timeout: 45 * time.Second, StallTimeout: 15 * time.Second})
	if err != nil {
		t.Fatalf("Wait: %v (дошли до %d%%)", err, итог.Percent)
	}
	if итог.Percent != 100 {
		t.Errorf("итог = %d%%, ожидалось 100", итог.Percent)
	}
	if итог.Phase != "Готово — Tor подключён" {
		t.Errorf("итоговая фаза = %q, ожидался перевод тега done", итог.Phase)
	}

	отменить()
	<-готово

	if len(видели) < 3 {
		t.Errorf("получено %d событий, ожидался ход подключения", len(видели))
	}
	переведённые := 0
	for _, шаг := range видели {
		if _, есть := PhaseNames[шаг.Tag]; есть && шаг.Phase != шаг.Tag {
			переведённые++
		}
	}
	if переведённые == 0 {
		t.Errorf("ни одна фаза не переведена: %+v", видели)
	}
}

// Команды после подписки на события должны проходить сразу: ради этого
// события слушаются вторым соединением (см. WatchBootstrap).
func TestКомандыРаботаютПослеПодписки(t *testing.T) {
	адрес := поднятьЗаглушку(t, "FAKE_TOR_DELAY=0.15")

	ctx, отменить := context.WithTimeout(context.Background(), 60*time.Second)
	defer отменить()

	клиент, err := Dial(ctx, адрес, Options{})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer клиент.Close()
	if err := клиент.Authenticate(ctx); err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if _, err := клиент.WatchBootstrap(ctx); err != nil {
		t.Fatalf("WatchBootstrap: %v", err)
	}

	// Заглушка шлёт события раз в 150 мс; команда обязана вернуться
	// быстрее, а не ждать ближайшего события.
	начало := time.Now()
	if err := клиент.NewIdentity(ctx); err != nil {
		t.Fatalf("NewIdentity: %v", err)
	}
	адресSocks, err := клиент.SocksAddress(ctx)
	if err != nil {
		t.Fatalf("SocksAddress: %v", err)
	}
	if прошло := time.Since(начало); прошло > 3*time.Second {
		t.Errorf("команды выполнялись %s — похоже, встали в очередь за событиями", прошло)
	}

	if !strings.Contains(адресSocks, ":") {
		t.Errorf("адрес SOCKS = %q, ожидался host:port", адресSocks)
	}
	if strings.Contains(адресSocks, `"`) {
		t.Errorf("адрес SOCKS = %q, кавычки должны быть сняты", адресSocks)
	}
}

// Заглушка умеет изображать падение при запуске. Проверяем, что до
// control-порта дело не доходит и ошибка внятная.
func TestНедоступныйControlПорт(t *testing.T) {
	ctx, отменить := context.WithTimeout(context.Background(), 5*time.Second)
	defer отменить()

	// Занятый, но не говорящий по протоколу порт: слушателя нет вовсе.
	_, err := Dial(ctx, "127.0.0.1:1", Options{DialTimeout: time.Second})
	if err == nil {
		t.Fatal("ожидалась ошибка соединения")
	}
	if !strings.Contains(err.Error(), "control-порт") {
		t.Errorf("ошибка = %v, ожидалось упоминание control-порта", err)
	}
}
