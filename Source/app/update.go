package main

import (
	"context"
	"net"
	"net/http"
	"time"

	"fyne.io/fyne/v2"
	"golang.org/x/net/proxy"

	"gitlab.com/vkandreevich/torlocalproxy/core"
	"gitlab.com/vkandreevich/torlocalproxy/core/service"
	"gitlab.com/vkandreevich/torlocalproxy/core/update"
)

// клиентОбновлений отдаёт HTTP-клиент для похода за обновлением. Если
// прокси уже поднят — ходим через него: хостинг выпусков (GitLab,
// GitHub) в цензурируемой сети бывает закрыт, а через Tor доступен. Нет
// прокси — идём напрямую.
func клиентОбновлений(служба *service.Service) *http.Client {
	транспорт := &http.Transport{}
	if socks := служба.SocksAddress(); socks != "" {
		if посредник, err := proxy.SOCKS5("tcp", socks, nil, &net.Dialer{Timeout: 30 * time.Second}); err == nil {
			if контекстный, годится := посредник.(proxy.ContextDialer); годится {
				транспорт.DialContext = контекстный.DialContext
			}
		}
	}
	return &http.Client{Timeout: 60 * time.Second, Transport: транспорт}
}

// запуститьПроверкуОбновлений в фоне спрашивает хостинг о новой версии и,
// если она есть, показывает баннер сверху окна. Молча ничего не делает
// при ошибке или отсутствии обновления: проверка не должна мешать
// работе, а тем более пугать сообщениями, когда сети нет.
func (э *экран) запуститьПроверкуОбновлений(окно fyne.Window, приложение fyne.App, служба *service.Service) {
	ист := источникОбновлений()
	if ист.TagPrefix == "" {
		// Платформа без своего канала выпусков (например, обычный Linux).
		return
	}

	go func() {
		ctx, отмена := context.WithTimeout(context.Background(), 60*time.Second)
		defer отмена()

		доступно, err := update.Check(ctx, клиентОбновлений(служба), ист, core.Version)
		if err != nil || доступно == nil {
			return
		}

		fyne.Do(func() {
			э.меткаОбн.SetText("Доступна версия " + доступно.Version)
			э.кнопкаОбн.OnTapped = func() {
				применитьОбновление(доступно, окно, приложение, служба)
			}
			э.баннерОбн.Show()
		})
	}()
}
