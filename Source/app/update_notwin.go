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
		return update.Source{
			Kind: update.GitLab, Owner: "vkandreevich", Repo: "TorLocalProxy",
			TagPrefix: "android-v", AssetSuffix: ".apk",
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

// применитьОбновление вне Windows пока не заменяет приложение само:
// на Android APK ставит системный установщик, на iOS/macOS файл берут
// со страницы выпуска. Поэтому открываем эту страницу, а дальше человек
// подтверждает установку сам. Полная замена на этих платформах — следом.
func применитьОбновление(доступно *update.Available, окно fyne.Window, приложение fyne.App, _ *service.Service) {
	if доступно.Page == "" {
		сообщить(окно, "Доступна версия "+доступно.Version+" — обновите из репозитория")
		return
	}
	адрес, err := url.Parse(доступно.Page)
	if err != nil {
		сообщить(окно, "Страница выпуска: "+доступно.Page)
		return
	}
	_ = приложение.OpenURL(адрес)
}
