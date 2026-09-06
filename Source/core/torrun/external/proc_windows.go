//go:build windows

package external

import (
	"os/exec"
	"syscall"
)

// скрытьОкно не даёт tor мигнуть чёрным окном консоли поверх интерфейса.
func скрытьОкно(команда *exec.Cmd) {
	команда.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}
