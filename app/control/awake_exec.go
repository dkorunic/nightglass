// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

//go:build darwin || linux

package control

import (
	"log/slog"
	"os/exec"
)

// startInhibitor runs cmd until the returned release; a missing tool only
// logs (nothing is inhibited).
func startInhibitor(cmd *exec.Cmd) func() {
	if err := cmd.Start(); err != nil {
		slog.Warn("cannot keep the computer awake", "cmd", cmd.Path, "err", err)

		return func() {}
	}
	go func() { _ = cmd.Wait() }()

	return func() { _ = cmd.Process.Kill() }
}
