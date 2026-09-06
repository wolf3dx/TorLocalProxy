//go:build integration

// Приёмка этапа 3: external.Runtime поднимает заглушку tor из прототипа
// и доводит подключение до 100 % через control-порт.
//
// За тегом сборки: нужен Python, а `go test` без флагов не должен
// требовать ничего (PLAN.md, раздел 5.1).
//
//	go test -tags integration ./core/torrun/external/
package external

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"gitlab.com/vkandreevich/torlocalproxy/core/bridges"
	"gitlab.com/vkandreevich/torlocalproxy/core/control"
	"gitlab.com/vkandreevich/torlocalproxy/core/torrun"
)

const отпечаток = "0123456789ABCDEF0123456789ABCDEF01234567"

// среда собирает Runtime, которому вместо tor подставлена заглушка.
// Ради этого случая в Options и заведено поле Args: заглушка — скрипт,
// запускать её приходится через интерпретатор.
func поднять(t *testing.T, окружение ...string) *Runtime {
	t.Helper()

	заглушка, err := filepath.Abs(filepath.Join("..", "..", "..",
		"Prototype-Python", "tests", "fake_tor.py"))
	if err != nil {
		t.Fatalf("путь к заглушке: %v", err)
	}
	if _, err := os.Stat(заглушка); err != nil {
		t.Skipf("заглушка tor не найдена: %v", err)
	}

	// Заглушка пишет по-русски, а Python на Windows по умолчанию отдаёт
	// stderr в кодировке консоли — вывод превратился бы в кракозябру.
	// Настоящий tor пишет на английском, так что это забота только теста.
	t.Setenv("PYTHONIOENCODING", "utf-8")

	for _, пара := range окружение {
		ключ, значение, _ := strings.Cut(пара, "=")
		t.Setenv(ключ, значение)
	}

	запуск := New(Options{Binary: питон(t), Args: []string{заглушка}})
	t.Cleanup(func() { _ = запуск.Stop() })
	return запуск
}

func питон(t *testing.T) string {
	t.Helper()
	кандидаты := []string{"python3", "python"}
	if runtime.GOOS == "windows" {
		кандидаты = []string{"python", "python3"}
	}
	for _, имя := range кандидаты {
		if путь, err := exec.LookPath(имя); err == nil {
			return путь
		}
	}
	t.Skip("Python не найден — интеграционный тест пропущен")
	return ""
}

func мосты(t *testing.T) []bridges.Bridge {
	t.Helper()
	разобранные, проблемы := bridges.Parse("obfs4 192.0.2.1:9443 " + отпечаток + " cert=AAA iat-mode=0")
	if len(проблемы) != 0 {
		t.Fatalf("тестовые мосты не разобрались: %v", проблемы)
	}
	return разобранные
}

// TestПодключениеЧерезRuntime — критерий приёмки этапа: запуск процесса,
// control-порт, bootstrap до 100 %.
func TestПодключениеЧерезRuntime(t *testing.T) {
	среда := поднять(t, "FAKE_TOR_DELAY=0.15")

	ctx, отменить := context.WithTimeout(context.Background(), 60*time.Second)
	defer отменить()

	конец, err := среда.Start(ctx, torrun.Config{
		DataDir: t.TempDir(),
		Bridges: мосты(t),
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if конец.ControlAddress == "" {
		t.Fatal("Start не вернул адрес control-порта")
	}
	// Порт выбирал tor, значит адрес SOCKS знает только он сам.
	if конец.SocksAddress != "" {
		t.Errorf("SocksAddress = %q, при автоматическом порту он неизвестен", конец.SocksAddress)
	}

	клиент, err := control.Dial(ctx, конец.ControlAddress, control.Options{})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer клиент.Close()
	if err := клиент.Authenticate(ctx); err != nil {
		t.Fatalf("Authenticate: %v", err)
	}

	события, err := клиент.WatchBootstrap(ctx)
	if err != nil {
		t.Fatalf("WatchBootstrap: %v", err)
	}
	итог, err := control.Wait(ctx, события, control.WaitOptions{
		Timeout:      45 * time.Second,
		StallTimeout: 15 * time.Second,
	})
	if err != nil {
		t.Fatalf("Wait: %v (дошли до %d%%)", err, итог.Percent)
	}
	if итог.Percent != 100 {
		t.Errorf("итог = %d%%, ожидалось 100", итог.Percent)
	}

	адресSocks, err := клиент.SocksAddress(ctx)
	if err != nil {
		t.Fatalf("SocksAddress: %v", err)
	}
	if !strings.Contains(адресSocks, ":") {
		t.Errorf("адрес SOCKS = %q", адресSocks)
	}
}

// Stop обязан не просто убить процесс, но и освободить порты: иначе
// повторное подключение упрётся в собственный хвост (PLAN.md, 5.3).
func TestStopОсвобождаетПорты(t *testing.T) {
	среда := поднять(t, "FAKE_TOR_DELAY=0.05")

	ctx, отменить := context.WithTimeout(context.Background(), 60*time.Second)
	defer отменить()

	конец, err := среда.Start(ctx, torrun.Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Пока процесс жив, control-порт отвечает.
	соединение, err := net.DialTimeout("tcp", конец.ControlAddress, 3*time.Second)
	if err != nil {
		t.Fatalf("control-порт не отвечает при живом процессе: %v", err)
	}
	_ = соединение.Close()

	if err := среда.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	// После Stop — уже нет. Порт освобождён, а не «завис» в ожидании.
	if соединение, err := net.DialTimeout("tcp", конец.ControlAddress, 2*time.Second); err == nil {
		_ = соединение.Close()
		t.Errorf("control-порт %s всё ещё принимает соединения после Stop", конец.ControlAddress)
	}

	if err := среда.Stop(); err != nil {
		t.Errorf("повторный Stop вернул ошибку: %v", err)
	}
}

// Заглушка умеет изображать провал запуска. Ошибка обязана объяснять,
// что произошло, и приносить с собой то, что процесс успел сказать.
func TestПровалЗапускаОбъясняется(t *testing.T) {
	среда := поднять(t, "FAKE_TOR_FAIL=1")

	ctx, отменить := context.WithTimeout(context.Background(), 30*time.Second)
	defer отменить()

	_, err := среда.Start(ctx, torrun.Config{DataDir: t.TempDir()})
	if err == nil {
		t.Fatal("ожидалась ошибка запуска")
	}
	if !strings.Contains(err.Error(), "control-порт") {
		t.Errorf("ошибка = %v, ожидалось упоминание control-порта", err)
	}
	if !strings.Contains(err.Error(), "имитирую сбой") {
		t.Errorf("ошибка не принесла вывод процесса: %v", err)
	}
}

// Порт, заданный явно, попадает в Endpoint без обращения к control.
func TestЯвныйПортПопадаетВEndpoint(t *testing.T) {
	среда := поднять(t, "FAKE_TOR_DELAY=0.05")

	слушатель, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	порт := слушатель.Addr().(*net.TCPAddr).Port
	_ = слушатель.Close() // порт свободен, но заведомо не занят никем другим

	ctx, отменить := context.WithTimeout(context.Background(), 60*time.Second)
	defer отменить()

	конец, err := среда.Start(ctx, torrun.Config{DataDir: t.TempDir(), SocksPort: порт})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if конец.SocksAddress == "" {
		t.Error("при явном порту адрес SOCKS должен быть известен сразу")
	}
	if !strings.HasSuffix(конец.SocksAddress, ":"+strconv.Itoa(порт)) {
		t.Errorf("SocksAddress = %q, ожидался порт %d", конец.SocksAddress, порт)
	}
}
