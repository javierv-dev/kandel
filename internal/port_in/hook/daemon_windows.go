//go:build windows

package hook

import (
	"os/exec"
	"syscall"
)

// Banderas de creación de proceso de Windows para desacoplar el daemon del hook:
//   - DETACHED_PROCESS (0x08): el hijo no hereda la consola del padre.
//   - CREATE_NEW_PROCESS_GROUP (0x200): grupo propio, no recibe el Ctrl-C del padre.
//   - CREATE_NO_WINDOW (0x08000000): impide que Windows abra una consola visible para el daemon.
const (
	detachedProcess       = 0x00000008
	createNewProcessGroup = 0x00000200
	createNoWindow        = 0x08000000
)

// detach configura cmd para que el proceso hijo sobreviva a la salida del hook en Windows sin ventana.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: detachedProcess | createNewProcessGroup | createNoWindow,
	}
}
