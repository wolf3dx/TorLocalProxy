//go:build windows

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestБатникЗаменяетИЗапускает прогоняет настоящий скрипт замены на живом
// Windows: отдельный процесс изображает работающее приложение, батник
// дожидается его выхода, копирует новые файлы поверх старых и запускает
// «новую версию». Проверяем, что подмена и перезапуск действительно
// случились, а временное убрано.
func TestБатникЗаменяетИЗапускает(t *testing.T) {
	if testing.Short() {
		t.Skip("-short: пропускаем тест с реальными процессами")
	}

	// Каталог с ASCII-именем (не t.TempDir — тот берёт имя из имени
	// теста, а оно кириллическое). Кириллические пути проверяет
	// отдельный тест ниже.
	корень, err := os.MkdirTemp("", "torupd-e2e-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(корень) })
	каталогПрил := filepath.Join(корень, "App")
	расп := filepath.Join(корень, "new")
	источник := filepath.Join(расп, "bundle")
	for _, д := range []string{каталогПрил, источник} {
		if err := os.MkdirAll(д, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	// Старое содержимое приложения.
	if err := os.WriteFile(filepath.Join(каталогПрил, "старый.txt"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Новая «поставка»: добавочный файл и скрипт-«приложение», который при
	// запуске оставляет отметку — так проверяем, что перезапуск сработал.
	if err := os.WriteFile(filepath.Join(источник, "добавлено.txt"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	отметка := filepath.Join(каталогПрил, "restarted.marker")
	скриптПрил := "@echo off\r\n>\"" + отметка + "\" echo restarted\r\n"
	if err := os.WriteFile(filepath.Join(источник, "restart.cmd"), []byte(скриптПрил), 0o644); err != nil {
		t.Fatal(err)
	}

	// Процесс, который батник должен дождаться. Живёт ~2 c и завершается
	// сам — так проверяется и ожидание, и выход из него, без гонки с
	// принудительным убийством.
	ожидаемый := exec.Command("ping", "-n", "3", "127.0.0.1")
	if err := ожидаемый.Start(); err != nil {
		t.Fatal(err)
	}
	pid := ожидаемый.Process.Pid
	go func() { _ = ожидаемый.Wait() }()

	батПуть := filepath.Join(корень, "apply.cmd")
	текст := текстБатника(pid, источник, каталогПрил, "restart.cmd", расп, батПуть)
	if err := os.WriteFile(батПуть, []byte(текст), 0o644); err != nil {
		t.Fatal(err)
	}

	// Запуск через .Run() (без перехвата вывода — иначе .Run ждал бы
	// закрытия пайпа start-процессом) и с тайм-аутом, чтобы тест не завис
	// при ошибке.
	ctx, отмена := context.WithTimeout(context.Background(), 30*time.Second)
	defer отмена()
	if err := exec.CommandContext(ctx, "cmd", "/C", батПуть).Run(); err != nil {
		t.Logf("батник вернул: %v (код не проверяем — важен результат)", err)
	}

	// Подмена файлов.
	if _, err := os.Stat(filepath.Join(каталогПрил, "добавлено.txt")); err != nil {
		t.Errorf("новый файл не скопирован: %v", err)
	}
	// Перезапуск («приложение» запустилось и оставило отметку). Запуск
	// идёт через start — асинхронно, поэтому ждём отметку немного.
	if !ждатьФайл(отметка, 5*time.Second) {
		t.Errorf("перезапуск не оставил отметку %s", отметка)
	}
	// Уборка.
	if _, err := os.Stat(расп); !os.IsNotExist(err) {
		t.Errorf("временный каталог распаковки не удалён")
	}
	// Самоудаление батника — best-effort: если не вышло, не беда (в бою
	// это временный файл в %TEMP%). Только отметим.
	if _, err := os.Stat(батПуть); err == nil {
		t.Logf("батник не удалил сам себя (не критично)")
	}
}

func ждатьФайл(путь string, срок time.Duration) bool {
	крайний := time.Now().Add(срок)
	for time.Now().Before(крайний) {
		if _, err := os.Stat(путь); err == nil {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}
