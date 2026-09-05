// Фикстура для проверки самого сторожа границы: файл под testdata
// невидим для go build и go vet, но парсер сторожа его читает.
package badimport

import (
	"fmt"

	"gitlab.com/vkandreevich/torlocalproxy/desktop/ui"
)

func Плохо() { fmt.Println(ui.Заголовок) }
