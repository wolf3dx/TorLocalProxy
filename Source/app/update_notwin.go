//go:build !windows

package main

import (
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"fyne.io/fyne/v2"

	"gitlab.com/vkandreevich/torlocalproxy/core/service"
	"gitlab.com/vkandreevich/torlocalproxy/core/update"
)

// источникОбновлений выбирает канал по системе сборки. Apple-платформы
// (iOS, macOS) и Linux собирает CI на GitHub, туда же кладёт пакеты;
// Android — на основной GitLab.
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
	case "linux":
		return update.Source{
			Kind: update.GitHub, Owner: "wolf3dx", Repo: "TorLocalProxy",
			TagPrefix: "linux-v", AssetSuffix: суффиксLinux(),
		}
	default:
		return update.Source{}
	}
}

// суффиксLinux выбирает, какой из двух пакетов предлагать: тот же, из
// какого приложение и поставлено.
//
// Пакетов на архитектуру два — .deb и портативный .tar.gz, — и подсунуть
// человеку чужой значило бы сломать ему установку: поставивший .deb
// получил бы папку, которую надо распаковывать руками, а распаковавший
// архив — пакет, который ставится совсем в другое место.
//
// Отличить одно от другого можно по тому, где лежим мы сами: .deb
// кладёт приложение в /opt (см. build-linux.sh), архив распаковывают
// куда угодно.
func суффиксLinux() string {
	арх := "x86_64"
	if runtime.GOARCH == "arm64" {
		арх = "arm64"
	}
	вид := ".tar.gz"
	if путь, err := os.Executable(); err == nil &&
		strings.HasPrefix(filepath.ToSlash(путь), "/opt/TorLocalProxy/") {
		вид = ".deb"
	}
	return "-linux-" + арх + вид
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
	case "linux":
		if доступно.AssetURL != "" {
			if strings.HasSuffix(доступно.AssetURL, ".deb") {
				return доступно.AssetURL, "Скачивается пакет версии " +
					доступно.Version + ". Когда загрузка закончится, откройте " +
					"файл — установка поверх нынешней версии."
			}
			return доступно.AssetURL, "Скачивается архив версии " +
				доступно.Version + ". Распакуйте его на место нынешней папки."
		}
	}
	// iOS и всё остальное — страница выпуска.
	return доступно.Page, ""
}
