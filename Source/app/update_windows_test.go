//go:build windows

package main

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestПервоеСлово(t *testing.T) {
	случаи := map[string]string{
		"abc123  TorLocalProxy.zip": "abc123",
		"deadbeef\n":                "deadbeef",
		"  spaced  ":                "spaced",
		"одно":                      "одно",
	}
	for вход, хотим := range случаи {
		if got := первоеСлово(вход); got != хотим {
			t.Errorf("первоеСлово(%q) = %q, хотели %q", вход, got, хотим)
		}
	}
}

func TestФайлSHA256(t *testing.T) {
	данные := []byte("tor local proxy update payload")
	путь := filepath.Join(t.TempDir(), "p.bin")
	if err := os.WriteFile(путь, данные, 0o644); err != nil {
		t.Fatal(err)
	}
	хотим := sha256.Sum256(данные)
	got, err := файлSHA256(путь)
	if err != nil {
		t.Fatal(err)
	}
	if got != hex.EncodeToString(хотим[:]) {
		t.Errorf("sha256 = %s, хотели %s", got, hex.EncodeToString(хотим[:]))
	}
}

func TestРаспаковкаСВерхнимКаталогом(t *testing.T) {
	// Собираем zip как build-windows.sh: единственный верхний каталог
	// поставки, внутри файлы.
	временный := t.TempDir()
	zipПуть := filepath.Join(временный, "u.zip")
	файл, err := os.Create(zipПуть)
	if err != nil {
		t.Fatal(err)
	}
	z := zip.NewWriter(файл)
	файлыВАрхиве := map[string]string{
		"TorLocalProxy-0.0.6-windows-x86_64/TorLocalProxy.exe": "новый бинарник",
		"TorLocalProxy-0.0.6-windows-x86_64/tor.exe":           "тор",
		"TorLocalProxy-0.0.6-windows-x86_64/ЧИТАТЬ.txt":        "как запускать",
	}
	for имя, содержимое := range файлыВАрхиве {
		w, err := z.Create(имя)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(содержимое)); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	_ = файл.Close()

	расп := filepath.Join(временный, "new")
	if err := распаковатьZip(zipПуть, расп); err != nil {
		t.Fatalf("распаковатьZip: %v", err)
	}

	внутр := внутреннийКаталог(расп)
	if filepath.Base(внутр) != "TorLocalProxy-0.0.6-windows-x86_64" {
		t.Errorf("внутреннийКаталог = %q, ждали папку поставки", внутр)
	}
	exe := filepath.Join(внутр, "TorLocalProxy.exe")
	данные, err := os.ReadFile(exe)
	if err != nil {
		t.Fatalf("нет распакованного exe: %v", err)
	}
	if string(данные) != "новый бинарник" {
		t.Errorf("содержимое exe = %q", string(данные))
	}
}

func TestРаспаковкаОтсекаетZipSlip(t *testing.T) {
	временный := t.TempDir()
	zipПуть := filepath.Join(временный, "evil.zip")
	файл, err := os.Create(zipПуть)
	if err != nil {
		t.Fatal(err)
	}
	z := zip.NewWriter(файл)
	w, _ := z.Create(`..\сбежал.txt`)
	_, _ = w.Write([]byte("нельзя"))
	_ = z.Close()
	_ = файл.Close()

	if err := распаковатьZip(zipПуть, filepath.Join(временный, "out")); err == nil {
		t.Fatal("ожидали отказ на выход за пределы каталога, его нет")
	}
}

func TestТекстБатника(t *testing.T) {
	бат := текстБатника(4242, `C:\tmp\new\bundle`, `C:\App`, "TorLocalProxy.exe",
		`C:\tmp\upd`, `C:\Windows\Temp\apply.cmd`)

	должноБыть := []string{
		`PID eq 4242`, // ждём именно наш процесс
		`find "4242"`, // и проверяем его же
		`goto`,        // цикл ожидания
		`xcopy /E /Y /I "C:\tmp\new\bundle\*" "C:\App\"`, // копируем новые файлы
		`start "" "C:\App\TorLocalProxy.exe"`,            // запускаем новую версию
		`rmdir /S /Q "C:\tmp\upd"`,                       // чистим временное
		`del "C:\Windows\Temp\apply.cmd"`,                // и сам батник
	}
	for _, кусок := range должноБыть {
		if !strings.Contains(бат, кусок) {
			t.Errorf("в батнике нет %q\n---\n%s", кусок, бат)
		}
	}
}

// на всякий случай — что fmt-числа в батнике совпадают с pid.
func TestТекстБатникаPID(t *testing.T) {
	бат := текстБатника(777, "s", "d", "e.exe", "t", "b.cmd")
	if strings.Count(бат, fmt.Sprintf("%d", 777)) < 2 {
		t.Errorf("pid должен встречаться дважды (ждать и проверять):\n%s", бат)
	}
}
