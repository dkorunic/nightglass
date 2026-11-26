// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package control

import (
	"os/exec"
	"syscall"
)

// inhibitSleep keeps Linux from going idle (suspend and, where the desktop
// honours it, blanking) with systemd-inhibit, which ends with Nightglass
// (Pdeathsig); display asks no more than idle can.
func inhibitSleep(why string, _ bool) func() {
	cmd := exec.Command("systemd-inhibit", "--what=idle", "--who=Nightglass", "--why="+why, "--mode=block", "sleep", "infinity") //nolint:noctx // lives until released
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGTERM}

	return startInhibitor(cmd)
}
