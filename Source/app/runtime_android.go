//go:build android

package main

import (
	"path/filepath"

	"fyne.io/fyne/v2"

	"gitlab.com/vkandreevich/torlocalproxy/core/torrun"
	"gitlab.com/vkandreevich/torlocalproxy/core/torrun/embedded"
)

// создатьЗапускTor на телефоне поднимает tor библиотекой внутри нашего
// же процесса. Положить рядом с приложением посторонний исполняемый файл
// и запустить его Android не даёт, поэтому другого пути здесь нет.
func создатьЗапускTor(string) torrun.Runtime {
	return embedded.New(embedded.Options{})
}

// каталогСостояния на Android — личное хранилище приложения. Ничего
// другого нам и не доступно, а tor туда пишет своё состояние и журнал.
func каталогСостояния(приложение fyne.App) string {
	корень := приложение.Storage().RootURI()
	if корень == nil {
		return "tor-state"
	}
	return filepath.Join(корень.Path(), "tor")
}
