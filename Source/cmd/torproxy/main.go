// Команда torproxy — десктопный клиент луковой сети с локальным прокси.
//
// Этап 0: каркас. Разбор мостов, запуск tor и сами прокси появляются на
// этапах 1–6, см. PLAN.md. Сейчас команда умеет только назвать себя —
// этого хватает, чтобы проверить сборку под все целевые платформы.
package main

import (
	"flag"
	"fmt"
	"os"

	"gitlab.com/vkandreevich/torlocalproxy/core"
)

func main() {
	показатьВерсию := flag.Bool("version", false, "показать версию и выйти")
	flag.Parse()

	if *показатьВерсию {
		fmt.Println(core.Banner())
		return
	}

	fmt.Fprintln(os.Stderr, core.Banner())
	fmt.Fprintln(os.Stderr, "Каркас без функционала: подключение появится на этапе 6 (см. PLAN.md).")
	fmt.Fprintln(os.Stderr, "Доступно: -version")
	os.Exit(2)
}
