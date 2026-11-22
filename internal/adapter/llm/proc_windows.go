//go:build windows

package llm

import (
	"os/exec"
	"syscall"
)

// createNoWindow (0x08000000) evita que Windows cree una ventana de consola visible cuando un
// proceso sin consola (como el daemon `kandel serve` lanzado con DETACHED_PROCESS) ejecuta una CLI
// hija (claude, pi, etc.).
const createNoWindow = 0x08000000

func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNoWindow,
	}
}
