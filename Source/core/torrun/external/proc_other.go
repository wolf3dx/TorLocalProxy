//go:build !windows

package external

import "os/exec"

// скрытьОкно нужно только на Windows: на остальных системах у процесса
// нет своего окна консоли.
func скрытьОкно(*exec.Cmd) {}
