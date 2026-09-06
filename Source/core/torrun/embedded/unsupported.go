//go:build !(cgo && (android || linux || darwin))

// Заглушка для сборок, где встроенного tor нет: под Windows go-libtor
// не собирается, а без cgo его нет вовсе. Файл существует, чтобы пакет
// импортировался везде, а отказ приходил внятным текстом, а не ошибкой
// компоновки.
package embedded

import (
	"context"
	"errors"

	"gitlab.com/vkandreevich/torlocalproxy/core/torrun"
)

// Available говорит, собрана ли в это приложение встроенная реализация.
const Available = false

// Options настраивает запуск. В этой сборке ни на что не влияет.
type Options struct {
	Log func(string)
}

// Runtime — реализация torrun.Runtime, которой в этой сборке нет.
type Runtime struct{}

var _ torrun.Runtime = (*Runtime)(nil)

// New создаёт заглушку.
func New(Options) *Runtime { return &Runtime{} }

var ошибка = errors.New(
	"встроенный tor в эту сборку не включён: соберите с CGO_ENABLED=1 и тегами " +
		"staticOpenssl,staticZlib,staticLibevent под Android, Linux или macOS")

func (*Runtime) Start(context.Context, torrun.Config) (torrun.Endpoint, error) {
	return torrun.Endpoint{}, ошибка
}

func (*Runtime) Stop() error { return nil }

// LastLog возвращает пустой журнал: запускать было нечего.
func (*Runtime) LastLog() []string { return nil }
