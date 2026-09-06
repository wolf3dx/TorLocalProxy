// Package core — корень модуля ядра. Логика живёт в подпакетах (bridges,
// torconf, control, ptrun, torrun, socksproxy, service, mobileapi);
// здесь только идентификация сборки и проверка границы модуля.
//
// Правило границы: core не импортирует ничего из cmd, desktop, android
// и ios (ЛОГИКА.md, раздел 3). Обратное направление — единственно
// допустимое. Проверяется тестом в arch_test.go.
package core

// Name — имя продукта, одинаковое на всех платформах.
const Name = "TorLocalProxy"

// Значения подставляются линковщиком при сборке выпуска:
//
//	go build -ldflags "-X gitlab.com/vkandreevich/torlocalproxy/core.Version=1.2.3 -X gitlab.com/vkandreevich/torlocalproxy/core.Commit=abc1234"
var (
	// Version — версия выпуска. Значение по умолчанию соответствует
	// текущей ветке разработки; в сборке выпуска подставляется
	// линковщиком из тега.
	Version = "0.0.4"
	// Commit — короткий хеш коммита, из которого собрано.
	Commit = "unknown"
)

// Banner возвращает строку вида "TorLocalProxy 1.2.3 (abc1234)".
func Banner() string {
	return Name + " " + Version + " (" + Commit + ")"
}
