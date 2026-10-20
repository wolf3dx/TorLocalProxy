package main

import (
	"os"
	"path/filepath"
)

// Автозапуск в Linux — файл .desktop в каталоге автозапуска XDG.
//
// Это общий для оболочек способ: GNOME, KDE, XFCE и прочие при входе
// пользователя проходят по ~/.config/autostart и запускают всё, что там
// описано. Прав администратора не нужно, запись видна в настройках
// оболочки («Автоматически запускаемые приложения») и выключается
// оттуда.
//
// Каталог берём через os.UserConfigDir: он уважает XDG_CONFIG_HOME,
// который у части людей уведён в сторону от ~/.config.

func автозапускДоступен() bool { return true }

func путьЗаписиАвтозапуска() (string, error) {
	настройки, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(настройки, "autostart", "torlocalproxy.desktop"), nil
}

func автозапускВключён() bool {
	путь, err := путьЗаписиАвтозапуска()
	if err != nil {
		return false
	}
	_, err = os.Stat(путь)
	return err == nil
}

func настроитьАвтозапуск(включить bool) error {
	файл, err := путьЗаписиАвтозапуска()
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
	if err := os.MkdirAll(filepath.Dir(файл), 0o755); err != nil {
		return err
	}
	return os.WriteFile(файл, []byte(текстDesktopEntry(путь)), 0o644)
}
