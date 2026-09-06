//go:build android

package main

import (
	"fyne.io/fyne/v2"

	"gitlab.com/vkandreevich/torlocalproxy/core/service"
)

// настроитьПлатформу на Android ничего не делает: окно закрыть нельзя,
// а живучесть в фоне обеспечивает служба переднего плана, объявленная в
// AndroidManifest.xml.
func настроитьПлатформу(fyne.App, fyne.Window, *service.Service) {}

// удержатьВФоне на Android не нужно: этим занимается та же служба
// переднего плана. Пока висит её уведомление, система процесс не
// трогает — ни при переключении на другие программы, ни при смахивании
// из списка задач.
func удержатьВФоне(bool) string { return "" }
