//go:build android || ios

package main

import (
	"fyne.io/fyne/v2"

	"gitlab.com/vkandreevich/torlocalproxy/core/service"
)

// настроитьПлатформу на телефоне ничего не делает: окно закрыть нельзя,
// а живучесть в фоне обеспечивает служба переднего плана, объявленная в
// AndroidManifest.xml.
func настроитьПлатформу(fyne.App, fyne.Window, *service.Service) {}
