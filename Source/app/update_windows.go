//go:build windows

package main

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"gitlab.com/vkandreevich/torlocalproxy/core/service"
	"gitlab.com/vkandreevich/torlocalproxy/core/update"
)

// имяИсполняемого — под этим именем приложение лежит в поставке (его
// делает fyne package). Новую версию запускаем именно по нему.
const имяИсполняемого = "TorLocalProxy.exe"

// источникОбновлений на Windows — основной репозиторий GitLab, выпуски с
// тегом windows-v и пакетом .zip.
func источникОбновлений() update.Source {
	return update.Source{
		Kind: update.GitLab, Owner: "vkandreevich", Repo: "TorLocalProxy",
		TagPrefix: "windows-v", AssetSuffix: ".zip",
	}
}

// применитьОбновление скачивает новую версию, проверяет её и заменяет
// работающую, после чего перезапускает приложение.
//
// Само себя заменить работающий exe не может — файл занят. Поэтому
// подмену делает отдельный батник: он дожидается, пока наш процесс
// выйдет (иначе и файл занят, и замок 19050 не отпущен), копирует новые
// файлы поверх старых и запускает свежую версию.
func применитьОбновление(доступно *update.Available, окно fyne.Window, _ fyne.App, служба *service.Service) {
	if доступно.AssetURL == "" {
		сообщить(окно, "У этого выпуска нет файла для Windows")
		return
	}

	полоса := widget.NewProgressBar()
	метка := widget.NewLabel("Скачиваю обновление…")
	окноПрогресса := dialog.NewCustomWithoutButtons("Обновление до "+доступно.Version,
		container.NewVBox(метка, полоса), окно)
	окноПрогресса.Show()

	go func() {
		клиент := клиентОбновлений(служба)

		провал := func(текст string, err error) {
			fyne.Do(func() {
				окноПрогресса.Hide()
				сообщить(окно, текст+": "+err.Error())
			})
		}

		врем, err := os.MkdirTemp("", "torproxy-upd-")
		if err != nil {
			провал("Не удалось подготовить обновление", err)
			return
		}

		// 1. Скачиваем пакет с показом процентов.
		zipПуть := filepath.Join(врем, "update.zip")
		if err := скачатьФайл(клиент, доступно.AssetURL, zipПуть, func(доля float64) {
			fyne.Do(func() { полоса.SetValue(доля) })
		}); err != nil {
			провал("Не удалось скачать", err)
			_ = os.RemoveAll(врем)
			return
		}

		// 2. Проверяем контрольную сумму: заменять бинарник без этого у
		// инструмента приватности нельзя.
		if доступно.SHA256URL != "" {
			fyne.Do(func() { метка.SetText("Проверяю контрольную сумму…") })
			сумма, err := скачатьТекст(клиент, доступно.SHA256URL)
			if err != nil {
				провал("Не удалось получить контрольную сумму", err)
				_ = os.RemoveAll(врем)
				return
			}
			факт, err := файлSHA256(zipПуть)
			if err != nil {
				провал("Не удалось посчитать сумму", err)
				_ = os.RemoveAll(врем)
				return
			}
			ожид := первоеСлово(сумма)
			if !strings.EqualFold(факт, ожид) {
				провал("Контрольная сумма не совпала — файл повреждён или подменён",
					fmt.Errorf("ожидали %s, получили %s", ожид, факт))
				_ = os.RemoveAll(врем)
				return
			}
		}

		// 3. Распаковываем. В архиве один верхний каталог поставки —
		// берём его содержимое.
		fyne.Do(func() { метка.SetText("Распаковываю…") })
		расп := filepath.Join(врем, "new")
		if err := распаковатьZip(zipПуть, расп); err != nil {
			провал("Не удалось распаковать", err)
			_ = os.RemoveAll(врем)
			return
		}
		источникФайлов := внутреннийКаталог(расп)

		exe, err := os.Executable()
		if err != nil {
			провал("Не найден путь приложения", err)
			_ = os.RemoveAll(врем)
			return
		}
		каталогПрил := filepath.Dir(exe)

		// 4. Батник кладём в системный temp, а не в удаляемый каталог:
		// он же этот каталог и удалит в конце, а себя сотрёт последней
		// строкой.
		батПуть := filepath.Join(os.TempDir(), fmt.Sprintf("torproxy-apply-%d.cmd", os.Getpid()))
		if err := os.WriteFile(батПуть,
			[]byte(текстБатника(os.Getpid(), источникФайлов, каталогПрил, имяИсполняемого, врем, батПуть)),
			0o644); err != nil {
			провал("Не удалось подготовить замену", err)
			_ = os.RemoveAll(врем)
			return
		}

		// 5. Останавливаем прокси (освобождаем tor и порты) и запускаем
		// отдельный процесс замены, после чего выходим.
		_ = служба.Disconnect()
		замена := exec.Command("cmd", "/C", "start", "", "/min", "cmd", "/C", батПуть)
		if err := замена.Start(); err != nil {
			провал("Не удалось запустить обновление", err)
			return
		}
		os.Exit(0)
	}()
}

// текстБатника собирает скрипт замены. Ждёт выхода процесса pid, копирует
// новые файлы поверх приложения, запускает свежую версию, подчищает за
// собой.
func текстБатника(pid int, источник, назначение, имяExe, врем, свойПуть string) string {
	// chcp 65001 — иначе cmd читает файл в OEM-кодировке (cp866 на
	// русской системе) и кириллица в путях ломается: приложение,
	// поставленное в папку с русским именем, не обновилось бы. Файл
	// пишется в UTF-8 без BOM, с этой кодовой страницей пути читаются
	// верно.
	// Метка цикла — ASCII (:wait), а не кириллица: с chcp 65001 cmd ищет
	// метку по байтовым смещениям в UTF-8 и на кириллице промахивается,
	// после чего goto не находит её и батник обрывается, не дойдя до
	// замены. Пути при этом могут быть кириллическими — их читает та же
	// кодовая страница, это данные, а не метка.
	return fmt.Sprintf(`@echo off
chcp 65001 >nul
:wait
tasklist /FI "PID eq %d" 2>nul | find "%d" >nul
if not errorlevel 1 (
  ping -n 2 127.0.0.1 >nul
  goto wait
)
xcopy /E /Y /I "%s\*" "%s\" >nul
start "" "%s\%s"
rmdir /S /Q "%s" >nul 2>&1
del "%s" >nul 2>&1
`, pid, pid, источник, назначение, назначение, имяExe, врем, свойПуть)
}

// скачатьФайл сохраняет ответ по адресу в файл, сообщая долю по мере
// чтения. Если сервер не назвал размер, доля остаётся на нуле — не беда.
func скачатьФайл(клиент *http.Client, адрес, куда string, прогресс func(float64)) error {
	ответ, err := клиент.Get(адрес)
	if err != nil {
		return err
	}
	defer func() { _ = ответ.Body.Close() }()
	if ответ.StatusCode != http.StatusOK {
		return fmt.Errorf("сервер ответил %s", ответ.Status)
	}

	файл, err := os.Create(куда)
	if err != nil {
		return err
	}
	defer func() { _ = файл.Close() }()

	всего := ответ.ContentLength
	var прочитано int64
	буфер := make([]byte, 64*1024)
	for {
		n, err := ответ.Body.Read(буфер)
		if n > 0 {
			if _, e := файл.Write(буфер[:n]); e != nil {
				return e
			}
			прочитано += int64(n)
			if всего > 0 && прогресс != nil {
				прогресс(float64(прочитано) / float64(всего))
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func скачатьТекст(клиент *http.Client, адрес string) (string, error) {
	ответ, err := клиент.Get(адрес)
	if err != nil {
		return "", err
	}
	defer func() { _ = ответ.Body.Close() }()
	if ответ.StatusCode != http.StatusOK {
		return "", fmt.Errorf("сервер ответил %s", ответ.Status)
	}
	тело, err := io.ReadAll(io.LimitReader(ответ.Body, 4096))
	return string(тело), err
}

func файлSHA256(путь string) (string, error) {
	файл, err := os.Open(путь)
	if err != nil {
		return "", err
	}
	defer func() { _ = файл.Close() }()
	хеш := sha256.New()
	if _, err := io.Copy(хеш, файл); err != nil {
		return "", err
	}
	return hex.EncodeToString(хеш.Sum(nil)), nil
}

// первоеСлово берёт хеш из строки вида "abc123  файл.zip" — файлы .sha256
// часто содержат и имя файла после суммы.
func первоеСлово(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, " \t\r\n"); i >= 0 {
		return s[:i]
	}
	return s
}

func распаковатьZip(архив, куда string) error {
	r, err := zip.OpenReader(архив)
	if err != nil {
		return err
	}
	defer func() { _ = r.Close() }()

	for _, ф := range r.File {
		цель := filepath.Join(куда, ф.Name)
		// Защита от выхода за пределы каталога (zip slip).
		if !strings.HasPrefix(цель, filepath.Clean(куда)+string(os.PathSeparator)) {
			return fmt.Errorf("подозрительный путь в архиве: %s", ф.Name)
		}
		if ф.FileInfo().IsDir() {
			if err := os.MkdirAll(цель, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(цель), 0o755); err != nil {
			return err
		}
		вход, err := ф.Open()
		if err != nil {
			return err
		}
		выход, err := os.OpenFile(цель, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
		if err != nil {
			_ = вход.Close()
			return err
		}
		_, err = io.Copy(выход, вход)
		_ = выход.Close()
		_ = вход.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

// внутреннийКаталог возвращает единственный вложенный каталог, если
// распакованное — это одна папка поставки (TorLocalProxy-<ver>-...).
// Иначе возвращает сам каталог.
func внутреннийКаталог(каталог string) string {
	записи, err := os.ReadDir(каталог)
	if err != nil {
		return каталог
	}
	if len(записи) == 1 && записи[0].IsDir() {
		return filepath.Join(каталог, записи[0].Name())
	}
	return каталог
}
