//go:build !android && !ios

package main

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"

	"gitlab.com/vkandreevich/torlocalproxy/core/service"
)

// настроитьПлатформу учит приложение вести себя на компьютере так же,
// как на телефоне: закрытие окна не должно ронять прокси.
//
// На Android за это отвечает служба переднего плана. Здесь роль та же у
// значка в трее: окно скрывается, процесс живёт, прокси отвечает. Выйти
// по-настоящему можно из меню значка — тогда tor корректно
// останавливается, а порты освобождаются.
func настроитьПлатформу(приложение fyne.App, окно fyne.Window, служба *service.Service) {
	выйти := func() {
		_ = служба.Disconnect()
		приложение.Quit()
	}

	// Трей есть не во всякой оболочке: на некоторых Linux-окружениях его
	// может не быть вовсе. Тогда закрытие окна означает выход — иначе
	// приложение стало бы невозможно ни увидеть, ни закрыть.
	рабочийСтол, естьТрей := приложение.(desktop.App)
	if !естьТрей {
		окно.SetCloseIntercept(выйти)
		return
	}

	рабочийСтол.SetSystemTrayMenu(fyne.NewMenu("TorLocalProxy",
		fyne.NewMenuItem("Показать окно", func() {
			окно.Show()
			окно.RequestFocus()
		}),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Выйти и отключить", выйти),
	))
	рабочийСтол.SetSystemTrayIcon(theme.VisibilityOffIcon())

	окно.SetCloseIntercept(func() {
		окно.Hide()
	})
}
