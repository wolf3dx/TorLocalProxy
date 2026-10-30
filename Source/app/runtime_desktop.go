//go:build !android && !ios

package main

import (
	"gitlab.com/vkandreevich/torlocalproxy/core/torrun"
	"gitlab.com/vkandreevich/torlocalproxy/core/torrun/external"
)

// создатьЗапускTor на компьютере поднимает tor отдельным процессом.
// Сам исполняемый файл ищется по известным местам, включая Tor Browser:
// чаще всего tor у пользователя есть именно оттуда.
func создатьЗапускTor(string) torrun.Runtime {
	return external.New(external.Options{})
}
