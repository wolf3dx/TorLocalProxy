package main

import (
	"os"

	"golang.org/x/sys/windows/registry"
)

// Автозапуск в Windows — значение в реестре текущего пользователя.
//
// Ветка HKCU\Software\Microsoft\Windows\CurrentVersion\Run выбрана
// нарочно, а не служба и не Планировщик заданий: запись в своей же
// ветке не требует прав администратора, видна человеку в «Автозагрузке»
// диспетчера задач и выключается оттуда одним переключателем. Служба
// потребовала бы установщика с правами и жила бы отдельно от
// пользователя — для прокси, которым пользуется один человек за своим
// столом, это лишнее.

const веткаЗапуска = `Software\Microsoft\Windows\CurrentVersion\Run`

func автозапускДоступен() bool { return true }

// автозапускВключён — есть ли наше значение в ветке запуска.
//
// Что именно там записано, не сверяем: приложение могли перенести, и
// тогда путь устарел, но запись всё равно есть и человек видит её в
// «Автозагрузке». Старый путь чинится повторным включением — оно
// перезаписывает значение сегодняшним.
func автозапускВключён() bool {
	ключ, err := registry.OpenKey(registry.CURRENT_USER, веткаЗапуска, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer ключ.Close()

	_, _, err = ключ.GetStringValue(ИмяАвтозапуска)
	return err == nil
}

func настроитьАвтозапуск(включить bool) error {
	ключ, err := registry.OpenKey(registry.CURRENT_USER, веткаЗапуска,
		registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer ключ.Close()

	if !включить {
		err := ключ.DeleteValue(ИмяАвтозапуска)
		// Значения и так нет — просили ровно этого.
		if err == registry.ErrNotExist {
			return nil
		}
		return err
	}

	путь, err := os.Executable()
	if err != nil {
		return err
	}
	return ключ.SetStringValue(ИмяАвтозапуска, команднаяСтрока(путь))
}
