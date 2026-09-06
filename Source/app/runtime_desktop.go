//go:build !android && !ios

package main

import (
	"path/filepath"

	"fyne.io/fyne/v2"

	"gitlab.com/vkandreevich/torlocalproxy/core/torrun"
	"gitlab.com/vkandreevich/torlocalproxy/core/torrun/external"
)

// создатьЗапускTor на компьютере поднимает tor отдельным процессом.
// Сам исполняемый файл ищется по известным местам, включая Tor Browser:
// чаще всего tor у пользователя есть именно оттуда.
func создатьЗапускTor(string) torrun.Runtime {
	return external.New(external.Options{})
}

// каталогСостояния на компьютере кладём рядом с настройками приложения.
func каталогСостояния(приложение fyne.App) string {
	корень := приложение.Storage().RootURI()
	if корень == nil {
		return "tor-state"
	}
	return filepath.Join(корень.Path(), "tor")
}
