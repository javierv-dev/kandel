//go:build !windows

package llm

import "os/exec"

func hideWindow(_ *exec.Cmd) {}
