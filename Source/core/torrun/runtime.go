// Package torrun описывает жизненный цикл tor.
//
// Это интерфейс с двумя реализациями, а не функция, и так задумано с
// самого начала. На iOS дочерние процессы запрещены совсем, поэтому там
// tor может быть только слинкованной библиотекой; на десктопе проще и
// надёжнее отдельный процесс. Выбор делается на этапе сборки
// (ЛОГИКА.md, раздел 2.1):
//
//	external — отдельный процесс tor (десктоп, этап 3)
//	embedded — go-libtor в этом же процессе (мобильные, этап 8)
//
// Пакет не знает ни о UI, ни о control-протоколе: он поднимает tor и
// сообщает, куда стучаться дальше.
package torrun

import (
	"context"

	"gitlab.com/vkandreevich/torlocalproxy/core/bridges"
	"gitlab.com/vkandreevich/torlocalproxy/core/torconf"
)

// PTPlugin — запущенный pluggable transport: какие транспорты он
// обслуживает и на каком адресе слушает. Тип общий с torconf: описание
// одно и то же, а два одинаковых типа пришлось бы перекладывать
// туда-сюда без всякой пользы. Заполняется на этапе 4.
type PTPlugin = torconf.Plugin

// Config — что нужно знать, чтобы поднять tor.
type Config struct {
	// DataDir — каталог состояния tor. Создаётся, если его нет.
	DataDir string
	// Bridges — мосты; пусто означает подключение напрямую.
	Bridges []bridges.Bridge
	// SocksPort — 0 означает «пусть tor выберет свободный сам».
	SocksPort int
	// PTPlugins — уже поднятые транспорты (этап 4).
	PTPlugins []PTPlugin
	// ExtraTorrc — дополнительные строки конфигурации от пользователя.
	ExtraTorrc string
}

// Endpoint — куда стучаться после запуска.
type Endpoint struct {
	// ControlAddress — host:port control-порта. Есть всегда.
	ControlAddress string
	// CookiePath — файл cookie-аутентификации. Обычно его сообщает сам
	// tor в PROTOCOLINFO, но знать путь заранее полезно для диагностики.
	CookiePath string
	// SocksAddress — host:port SOCKS5, если порт был задан явно. При
	// SocksPort = 0 остаётся пустым: какой порт tor выбрал, знает только
	// он сам, и спрашивать надо через control (GETINFO
	// net/listeners/socks). Ядро не лезет в control из torrun, чтобы не
	// смешивать две роли.
	SocksAddress string
}

// Runtime поднимает и останавливает tor.
//
// Stop обязан быть безопасным при повторном вызове и при вызове до
// Start: остановка вызывается и из обработчика ошибок, где состояние
// неизвестно.
type Runtime interface {
	Start(ctx context.Context, cfg Config) (Endpoint, error)
	Stop() error
}
