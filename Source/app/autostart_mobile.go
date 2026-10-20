//go:build android || ios

package main

import "fyne.io/fyne/v2"

// На телефоне приложение запускают касанием значка, и прятать окно при
// запуске незачем: кроме него, показывать нечего.
func стартоватьСкрытно(fyne.App) bool { return false }
