package main

import (
	"os"
	"path/filepath"
)

// Автозапуск в macOS — агент входа в ~/Library/LaunchAgents.
//
// launchd загружает агенты из этого каталога при входе пользователя и
// сам запускает то, у чего стоит RunAtLoad. Прав администратора это не
// требует (в отличие от /Library/LaunchDaemons), а человек видит
// приложение в «Объектах входа» системных настроек и может выключить
// его оттуда.
//
// launchctl не зовём: при следующем входе агент подхватится сам, а до
// того приложение и так уже запущено — оно же и ставило галочку.

func автозапускДоступен() bool { return true }

// путьАгента — файл агента входа. Имя — идентификатор приложения, как
// принято у Apple: так он не столкнётся с чужими агентами.
func путьАгента() (string, error) {
	дом, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(дом, "Library", "LaunchAgents", ИдПриложения+".plist"), nil
}

func автозапускВключён() bool {
	путь, err := путьАгента()
	if err != nil {
		return false
	}
	_, err = os.Stat(путь)
	return err == nil
}

func настроитьАвтозапуск(включить bool) error {
	файл, err := путьАгента()
	if err != nil {
		return err
	}

	if !включить {
		err := os.Remove(файл)
		// Файла и так нет — просили ровно этого.
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	путь, err := os.Executable()
	if err != nil {
		return err
	}
	// Каталога может не быть вовсе: у человека, который ни одного агента
	// не заводил, LaunchAgents не создан.
	if err := os.MkdirAll(filepath.Dir(файл), 0o755); err != nil {
		return err
	}
	return os.WriteFile(файл, []byte(текстPlist(путь)), 0o644)
}
