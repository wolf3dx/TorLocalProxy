// Команда torproxy — мини-прокси для телефона и компьютера.
//
// Одно окно с четырьмя плашками: состояние подключения, поле мостов,
// журнал и адрес прокси, который вписывают в чужие приложения. Браузера
// нет и не предполагается.
//
// Один и тот же код собирается и в окно на компьютере, и в APK для
// Android. Разница ровно одна и заперта в файлах runtime_*: чем поднимать
// tor. На компьютере это отдельный процесс, на телефоне — библиотека
// внутри нашего же процесса, потому что запускать посторонние
// исполняемые файлы Android не даёт.
package main

import (
	"context"
	"fmt"
	"log"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"gitlab.com/vkandreevich/torlocalproxy/core/service"
)

// ИдПриложения нужен Fyne, чтобы выделить приложению своё хранилище.
// На Android от него зависит каталог, куда лягут состояние tor и мосты.
const ИдПриложения = "com.vkandreevich.torlocalproxy"

func main() {
	приложение := app.NewWithID(ИдПриложения)
	приложение.Settings().SetTheme(theme.DarkTheme())

	окно := приложение.NewWindow("TorLocalProxy")
	окно.Resize(fyne.NewSize(420, 640))

	каталог := каталогСостояния(приложение)
	служба, err := service.New(service.Options{
		StateDir: каталог,
		Runtime:  создатьЗапускTor(каталог),
	})
	if err != nil {
		окно.SetContent(widget.NewLabel("Не удалось подготовить приложение:\n" + err.Error()))
		окно.ShowAndRun()
		return
	}

	экран := собратьЭкран(окно, служба)
	окно.SetContent(экран.корень)

	// Мосты из прошлого запуска — чтобы не вставлять их каждый раз.
	if сохранённые, err := служба.LoadBridges(); err == nil && сохранённые != "" {
		экран.мосты.SetText(сохранённые)
	}

	окно.SetCloseIntercept(func() {
		_ = служба.Disconnect()
		окно.Close()
	})
	окно.ShowAndRun()
}

// экран держит виджеты, которые обновляются по ходу подключения.
type экран struct {
	корень      fyne.CanvasObject
	состояние   *widget.Label
	фаза        *widget.Label
	полоса      *widget.ProgressBar
	мосты       *widget.Entry
	адрес       *widget.Label
	подключение *widget.Button
	цепочка     *widget.Button
}

func собратьЭкран(окно fyne.Window, служба *service.Service) *экран {
	э := &экран{}

	// ---- Плашка 1: состояние подключения ----------------------------
	э.состояние = widget.NewLabelWithStyle("ВЫКЛЮЧЕНО", fyne.TextAlignCenter,
		fyne.TextStyle{Bold: true})
	э.фаза = widget.NewLabelWithStyle("Не подключено", fyne.TextAlignCenter, fyne.TextStyle{})
	э.полоса = widget.NewProgressBar()
	э.полоса.Hide()

	плашкаСостояния := widget.NewCard("Состояние", "", container.NewVBox(
		э.состояние, э.полоса, э.фаза,
	))

	// ---- Плашка 4: адрес прокси -------------------------------------
	// Идёт второй сверху не случайно: это то, ради чего приложение и
	// нужно. Пользователь возвращается сюда каждый раз, когда настраивает
	// очередную программу.
	э.адрес = widget.NewLabelWithStyle("—", fyne.TextAlignCenter, fyne.TextStyle{Monospace: true})
	копировать := widget.NewButtonWithIcon("Копировать", theme.ContentCopyIcon(), func() {
		текст := э.адрес.Text
		if текст == "—" {
			сообщить(окно, "Прокси ещё не поднят")
			return
		}
		окно.Clipboard().SetContent(текст)
		сообщить(окно, "Адрес скопирован")
	})
	плашкаАдреса := widget.NewCard("Прокси SOCKS5", "вписать в настройки приложения",
		container.NewVBox(э.адрес, копировать))

	// ---- Плашка 2: мосты --------------------------------------------
	э.мосты = widget.NewMultiLineEntry()
	э.мосты.SetPlaceHolder("Вставьте строки мостов целиком, как их прислал\n" +
		"bridges@torproject.org. Лишний текст письма можно не убирать.")
	э.мосты.SetMinRowsVisible(5)
	сохранить := widget.NewButtonWithIcon("Сохранить мосты", theme.DocumentSaveIcon(), func() {
		if err := служба.SaveBridges(э.мосты.Text); err != nil {
			сообщить(окно, "Не сохранилось: "+err.Error())
			return
		}
		сообщить(окно, "Мосты сохранены")
	})
	плашкаМостов := widget.NewCard("Мосты", "пусто — подключение напрямую",
		container.NewVBox(э.мосты, сохранить))

	// ---- Плашка 3: журнал -------------------------------------------
	журнал := widget.NewButtonWithIcon("Посмотреть журнал", theme.DocumentIcon(), func() {
		показатьЖурнал(окно, служба)
	})

	// ---- Управление --------------------------------------------------
	э.подключение = widget.NewButtonWithIcon("Подключить", theme.MediaPlayIcon(), nil)
	э.подключение.Importance = widget.HighImportance
	э.подключение.OnTapped = func() { э.переключить(окно, служба) }

	э.цепочка = widget.NewButtonWithIcon("Новая цепочка", theme.ViewRefreshIcon(), func() {
		if err := служба.NewIdentity(context.Background()); err != nil {
			сообщить(окно, err.Error())
			return
		}
		сообщить(окно, "Запрошена новая цепочка")
	})
	э.цепочка.Disable()

	содержимое := container.NewVBox(
		плашкаСостояния,
		плашкаАдреса,
		плашкаМостов,
		журнал,
		layout.NewSpacer(),
		container.NewGridWithColumns(2, э.подключение, э.цепочка),
	)
	э.корень = container.NewPadded(container.NewVScroll(содержимое))
	return э
}

// переключить подключает или отключает — в зависимости от того, где мы
// сейчас.
func (э *экран) переключить(окно fyne.Window, служба *service.Service) {
	if служба.State() == service.StateConnected {
		_ = служба.Disconnect()
		return
	}
	if служба.State() == service.StateConnecting {
		_ = служба.Disconnect()
		return
	}

	текстМостов := э.мосты.Text
	// Сохраняем сразу: пользователь вставил мосты и нажал «Подключить»,
	// нажимать ещё и «Сохранить» он не обязан.
	if err := служба.SaveBridges(текстМостов); err != nil {
		log.Println("мосты не сохранились:", err)
	}

	// Connect блокирующий, поэтому уходим в отдельную горутину: иначе
	// окно замрёт на всё время подключения.
	go func() {
		err := служба.Connect(context.Background(), текстМостов, наблюдатель{экран: э, окно: окно})
		if err != nil {
			fyne.Do(func() { сообщить(окно, err.Error()) })
		}
	}()
}

// наблюдатель переносит события службы в виджеты.
//
// Все обращения к интерфейсу завёрнуты в fyne.Do: служба зовёт нас из
// своей горутины, а трогать виджеты можно только из потока интерфейса.
type наблюдатель struct {
	экран *экран
	окно  fyne.Window
}

func (н наблюдатель) OnBootstrap(percent int, phase string) {
	fyne.Do(func() {
		н.экран.полоса.Show()
		н.экран.полоса.SetValue(float64(percent) / 100)
		н.экран.фаза.SetText(fmt.Sprintf("%d%% — %s", percent, phase))
	})
}

func (н наблюдатель) OnLog(string) {
	// Строки уже копятся в службе; кнопка журнала читает их оттуда.
}

func (н наблюдатель) OnState(state string) {
	fyne.Do(func() {
		switch state {
		case service.StateConnecting:
			н.экран.состояние.SetText("ПОДКЛЮЧЕНИЕ")
			н.экран.подключение.SetText("Прервать")
			н.экран.подключение.SetIcon(theme.MediaStopIcon())
			н.экран.цепочка.Disable()
			н.экран.полоса.Show()

		case service.StateConnected:
			н.экран.состояние.SetText("ВКЛЮЧЕНО")
			н.экран.фаза.SetText("Готово — трафик идёт через Tor")
			н.экран.подключение.SetText("Отключить")
			н.экран.подключение.SetIcon(theme.MediaStopIcon())
			н.экран.цепочка.Enable()
			н.экран.полоса.Hide()

		case service.StateFailed:
			н.экран.состояние.SetText("ОШИБКА")
			н.экран.фаза.SetText("Подробности — в журнале")
			н.экран.подключение.SetText("Подключить")
			н.экран.подключение.SetIcon(theme.MediaPlayIcon())
			н.экран.цепочка.Disable()
			н.экран.полоса.Hide()
			н.экран.адрес.SetText("—")

		default:
			н.экран.состояние.SetText("ВЫКЛЮЧЕНО")
			н.экран.фаза.SetText("Не подключено")
			н.экран.подключение.SetText("Подключить")
			н.экран.подключение.SetIcon(theme.MediaPlayIcon())
			н.экран.цепочка.Disable()
			н.экран.полоса.Hide()
			н.экран.адрес.SetText("—")
		}
	})
}

// показатьЖурнал открывает окно с накопленными строками — та самая
// кнопка «понять, что не так».
func показатьЖурнал(окно fyne.Window, служба *service.Service) {
	строки := служба.Log()
	if len(строки) == 0 {
		сообщить(окно, "Журнал пуст — подключение ещё не начиналось")
		return
	}

	список := widget.NewList(
		func() int { return len(строки) },
		func() fyne.CanvasObject {
			метка := widget.NewLabel("")
			метка.TextStyle = fyne.TextStyle{Monospace: true}
			метка.Wrapping = fyne.TextWrapWord
			return метка
		},
		func(i widget.ListItemID, объект fyne.CanvasObject) {
			объект.(*widget.Label).SetText(строки[i])
		},
	)
	// Показываем конец: причина отказа всегда там.
	список.ScrollToBottom()

	окноЖурнала := dialog.NewCustom("Журнал подключения", "Закрыть", список, окно)
	окноЖурнала.Resize(fyne.NewSize(400, 560))
	окноЖурнала.Show()
}

func сообщить(окно fyne.Window, текст string) {
	dialog.ShowInformation("TorLocalProxy", текст, окно)
}
