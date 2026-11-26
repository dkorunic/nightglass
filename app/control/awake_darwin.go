// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package control

import (
	"os"
	"os/exec"
	"strconv"
)

// inhibitSleep keeps macOS from idle sleep (and the display awake with
// display) with caffeinate, which also ends with Nightglass (-w).
func inhibitSleep(_ string, display bool) func() {
	flag := "-i"
	if display {
		flag = "-di"
	}
	cmd := exec.Command("caffeinate", flag, "-w", strconv.Itoa(os.Getpid())) //nolint:noctx,gosec // fixed command and our own pid; lives until released

	return startInhibitor(cmd)
}
