//go:build !windows

package main

import (
	"net/url"
	"runtime"

	"fyne.io/fyne/v2"

	"gitlab.com/vkandreevich/torlocalproxy/core/service"
	"gitlab.com/vkandreevich/torlocalproxy/core/update"
)

// источникОбновлений выбирает канал по системе сборки. Apple-платформы
// (iOS, macOS) собирает CI на Mac и кладёт на GitHub; Android — на
// основной GitLab. Обычный Linux своего канала пока не имеет.
func источникОбновлений() update.Source {
	switch runtime.GOOS {
	case "android":
		// APK у каждой архитектуры свой: телефону нужен arm64-v8a,
		// эмулятору — x86_64. Скачать чужой нельзя, он не поставится.
		// Поэтому ассет отбираем по концу имени под свою архитектуру.
		суффикс := "x86_64.apk"
		switch runtime.GOARCH {
		case "arm64":
			суффикс = "arm64-v8a.apk"
		case "arm":
			суффикс = "armeabi-v7a.apk"
		}
		return update.Source{
			Kind: update.GitLab, Owner: "vkandreevich", Repo: "TorLocalProxy",
			TagPrefix: "android-v", AssetSuffix: суффикс,
		}
	case "ios":
		return update.Source{
			Kind: update.GitHub, Owner: "wolf3dx", Repo: "TorLocalProxy",
			TagPrefix: "ios-v", AssetSuffix: ".ipa",
		}
	case "darwin":
		return update.Source{
			Kind: update.GitHub, Owner: "wolf3dx", Repo: "TorLocalProxy",
			TagPrefix: "macos-v", AssetSuffix: ".dmg",
		}
	default:
		return update.Source{}
	}
}

// применитьОбновление вне Windows приложение само себя не заменяет:
// песочница не даёт. Что делаем по платформам:
//
//   - Android: открываем прямую ссылку на APK. Система скачивает файл, и
//     дальше его ставит штатный установщик пакетов — пользователь
//     подтверждает. Тихо подменить APK нельзя.
//   - iOS: открываем страницу выпуска. Поставить .ipa можно только
//     сторонним средством (Sideloadly), прямая ссылка тут не поможет.
//   - macOS: открываем ссылку на .dmg — она просто скачается. Полная
//     авто-замена .app — отдельным шагом позже.
//
// Возвращаемся к списку действий по системе: направление одно —
// «открыть нужный адрес во внешнем приложении», отличается лишь адрес и
// подсказка человеку.
func применитьОбновление(доступно *update.Available, окно fyne.Window, приложение fyne.App, _ *service.Service) {
	адрес, подсказка := кудаЗаОбновлением(доступно)
	if адрес == "" {
		сообщить(окно, "Доступна версия "+доступно.Version+" — обновите из репозитория")
		return
	}
	разобранный, err := url.Parse(адрес)
	if err != nil {
		сообщить(окно, "Адрес обновления: "+адрес)
		return
	}
	if подсказка != "" {
		сообщить(окно, подсказка)
	}
	_ = приложение.OpenURL(разобранный)
}

// кудаЗаОбновлением выбирает адрес и подсказку по системе сборки.
func кудаЗаОбновлением(доступно *update.Available) (адрес, подсказка string) {
	switch runtime.GOOS {
	case "android":
		if доступно.AssetURL != "" {
			return доступно.AssetURL, "Скачивается APK версии " + доступно.Version +
				". Когда загрузка закончится, откройте файл и подтвердите установку."
		}
	case "darwin":
		if доступно.AssetURL != "" {
			return доступно.AssetURL, ""
		}
	}
	// iOS и всё остальное — страница выпуска.
	return доступно.Page, ""
}
