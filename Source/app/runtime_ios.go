//go:build ios

package main

import (
	"path/filepath"

	"fyne.io/fyne/v2"

	"gitlab.com/vkandreevich/torlocalproxy/core/torrun"
	"gitlab.com/vkandreevich/torlocalproxy/core/torrun/embedded"
	"gitlab.com/vkandreevich/torlocalproxy/core/torrun/torlib"
)

// создатьЗапускTor на iPhone поднимает tor библиотекой внутри нашего
// процесса.
//
// Выбора здесь нет: на iOS запуск дочерних процессов запрещён совсем.
// Ни положить tor рядом с приложением, ни запустить его, как мы делаем
// на Windows и Android, нельзя — остаётся линковать его в себя. Так же
// поступают Onion Browser и Orbot для iOS.
//
// Плата за это известна и описана в torrun/torlib: остановить такой tor
// снаружи нечем, а запустить его в одном процессе дважды нельзя.
func создатьЗапускTor(string) torrun.Runtime {
	return embedded.New(embedded.Options{Creator: torlib.Creator})
}
