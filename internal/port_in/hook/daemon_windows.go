//go:build windows

package hook

import (
	"os/exec"
	"syscall"
)

// Banderas de creación de proceso de Windows para desacoplar el daemon del hook:
//   - DETACHED_PROCESS (0x08): el hijo no hereda la consola del padre.
//   - CREATE_NEW_PROCESS_GROUP (0x200): grupo propio, no recibe el Ctrl-C del padre.
const (
	detachedProcess       = 0x00000008
	createNewProcessGroup = 0x00000200
)

// detach configura cmd para que el proceso hijo sobreviva a la salida del hook en Windows.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: detachedProcess | createNewProcessGroup,
	}
}
