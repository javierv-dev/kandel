//go:build !windows

package hook

import (
	"os/exec"
	"syscall"
)

// detach configura cmd para que el proceso hijo sobreviva a la salida del hook en sistemas tipo
// Unix: Setsid lo coloca en una sesión nueva, desligado del proceso padre.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
