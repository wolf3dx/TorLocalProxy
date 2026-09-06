//go:build android

package main

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"

	"fyne.io/fyne/v2"

	"gitlab.com/vkandreevich/torlocalproxy/core/torrun"
	"gitlab.com/vkandreevich/torlocalproxy/core/torrun/external"
)

// ИмяБинарникаTor — как tor лежит внутри пакета приложения.
//
// Расширение .so здесь не значит «библиотека»: это обычный исполняемый
// файл, просто названный так, чтобы попасть в каталог нативных
// библиотек. Android распаковывает туда всё из lib/<архитектура>/ и
// ставит бит выполнения — и это единственное место, откуда приложению
// разрешено запускать посторонний код. Из своего каталога данных
// запустить его нельзя начиная с Android 10. Так же поступает Orbot.
const ИмяБинарникаTor = "libtor.so"

// создатьЗапускTor на телефоне запускает tor отдельным процессом — тем
// же кодом, что и на компьютере.
//
// Встроенная сборка (torrun/embedded) для этого не годится: go-libtor
// несёт tor 0.3.5 от 2019 года, а сеть требует протокол FlowCtrl=1.
// Такой tor доходит до 40 % и зовёт exit(1), унося с собой всё
// приложение — он ведь внутри нашего процесса. Отдельный процесс от
// этого защищает заодно: что бы с ним ни случилось, окно останется
// живым и покажет причину.
func создатьЗапускTor(string) torrun.Runtime {
	return external.New(external.Options{Binary: путьКTor()})
}

// путьКTor ищет tor в каталоге нативных библиотек приложения.
//
// Своего пути приложение на Android не знает: os.Executable вернёт
// /system/bin/app_process. Зато в /proc/self/maps видно, откуда
// загружена наша собственная библиотека, а tor лежит рядом с ней.
func путьКTor() string {
	каталог := каталогБиблиотек()
	if каталог == "" {
		// Пусть external поищет сам и, не найдя, объяснит по-человечески.
		return ""
	}
	return filepath.Join(каталог, ИмяБинарникаTor)
}

func каталогБиблиотек() string {
	файл, err := os.Open("/proc/self/maps")
	if err != nil {
		return ""
	}
	defer func() { _ = файл.Close() }()

	чтение := bufio.NewScanner(файл)
	for чтение.Scan() {
		строка := чтение.Text()
		пробел := strings.LastIndex(строка, " ")
		if пробел < 0 {
			continue
		}
		путь := строка[пробел+1:]
		// Нас интересует своя же библиотека: она лежит ровно там, куда
		// установщик распаковал содержимое lib/<архитектура>/.
		if strings.HasSuffix(путь, ".so") && strings.Contains(путь, "/lib/") &&
			strings.Contains(путь, "com.vkandreevich.torlocalproxy") {
			return filepath.Dir(путь)
		}
	}
	return ""
}

// каталогСостояния на Android — личное хранилище приложения. Ничего
// другого нам и не доступно, а tor туда пишет состояние и журнал.
func каталогСостояния(приложение fyne.App) string {
	корень := приложение.Storage().RootURI()
	if корень == nil {
		return "tor-state"
	}
	return filepath.Join(корень.Path(), "tor")
}
