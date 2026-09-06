package external

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// ErrTorNotFound — исполняемый файл tor не найден ни по одному из
// известных путей.
var ErrTorNotFound = errors.New("исполняемый файл tor не найден")

// имяTor учитывает расширение Windows.
func имяTor() string {
	if runtime.GOOS == "windows" {
		return "tor.exe"
	}
	return "tor"
}

// SearchPaths перечисляет места, где может лежать tor, в порядке
// предпочтения. Список перенесён из прототипа (tormgr.py,
// _tor_search_paths) и уже учитывает Tor Browser на трёх ОС: чаще всего
// tor у пользователя есть именно оттуда, а не отдельным пакетом.
//
// Первым идёт TOR_BINARY из окружения — это способ указать свой путь,
// не трогая настройки.
func SearchPaths() []string {
	var пути []string

	if изОкружения := os.Getenv("TOR_BINARY"); изОкружения != "" {
		пути = append(пути, изОкружения)
	}

	// Рядом с самим приложением: так выглядит поставка, где tor лежит
	// в комплекте.
	if рядом, err := os.Executable(); err == nil {
		каталог := filepath.Dir(рядом)
		for _, под := range []string{"", "tor", "bin", filepath.Join("tor", "tor")} {
			пути = append(пути, filepath.Join(каталог, под, имяTor()))
		}
	}

	if вПути, err := exec.LookPath("tor"); err == nil {
		пути = append(пути, вПути)
	}

	дом, _ := os.UserHomeDir()

	switch runtime.GOOS {
	case "windows":
		основания := []string{
			os.Getenv("LOCALAPPDATA"),
			os.Getenv("APPDATA"),
			os.Getenv("ProgramFiles"),
			os.Getenv("ProgramFiles(x86)"),
			`C:\`,
			`C:\Tor`,
		}
		if дом != "" {
			основания = append(основания, filepath.Join(дом, "Desktop"), filepath.Join(дом, "Downloads"))
		}
		for _, основание := range основания {
			if основание == "" {
				continue
			}
			пути = append(пути,
				filepath.Join(основание, "Tor Browser", "Browser", "TorBrowser", "Tor", "tor.exe"),
				filepath.Join(основание, "Tor", "tor.exe"),
				filepath.Join(основание, "tor", "tor.exe"),
			)
		}

	case "darwin":
		пути = append(пути,
			"/Applications/Tor Browser.app/Contents/MacOS/Tor/tor",
			"/opt/homebrew/bin/tor",
			"/usr/local/bin/tor",
			"/opt/local/bin/tor",
		)
		if дом != "" {
			пути = append(пути,
				filepath.Join(дом, "Applications", "Tor Browser.app", "Contents", "MacOS", "Tor", "tor"))
		}

	default:
		пути = append(пути,
			"/usr/bin/tor",
			"/usr/local/bin/tor",
			"/usr/sbin/tor",
			"/snap/bin/tor",
		)
		if дом != "" {
			for _, каталог := range []string{"tor-browser", "tor-browser_en-US", ".local/share/torbrowser"} {
				пути = append(пути,
					filepath.Join(дом, filepath.FromSlash(каталог), "Browser", "TorBrowser", "Tor", "tor"))
			}
		}
	}

	return пути
}

// FindTor возвращает путь к исполняемому tor. Подсказка, если она не
// пуста, проверяется первой.
func FindTor(hint string) (string, error) {
	return найтиСреди(hint, SearchPaths())
}

// найтиСреди — сам поиск, отделённый от списка путей. Так проверяется
// отказ: на машине разработчика tor обычно установлен, и FindTor с
// настоящим списком просто не может не найти его.
func найтиСреди(hint string, кандидаты []string) (string, error) {
	if hint != "" {
		кандидаты = append([]string{hint}, кандидаты...)
	}

	for _, путь := range кандидаты {
		if путь == "" {
			continue
		}
		if исполняемый(путь) {
			абсолютный, err := filepath.Abs(путь)
			if err != nil {
				return путь, nil
			}
			return абсолютный, nil
		}
	}

	if hint != "" {
		return "", fmt.Errorf("%w: указанный путь %q не подошёл, остальные %d тоже",
			ErrTorNotFound, hint, len(кандидаты)-1)
	}
	return "", fmt.Errorf("%w: проверено %d путей. Укажите его в TOR_BINARY "+
		"или установите Tor Browser", ErrTorNotFound, len(кандидаты))
}

// исполняемый проверяет, что по пути лежит файл, который можно
// запустить. На Windows бит выполнения не хранится, там достаточно того,
// что файл существует.
func исполняемый(путь string) bool {
	сведения, err := os.Stat(путь)
	if err != nil || сведения.IsDir() {
		return false
	}
	if runtime.GOOS == "windows" {
		return strings.EqualFold(filepath.Ext(путь), ".exe")
	}
	return сведения.Mode().Perm()&0o111 != 0
}
