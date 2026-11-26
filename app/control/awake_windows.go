// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package control

import (
	"runtime"
	"syscall"
)

var setThreadExecutionState = syscall.NewLazyDLL("kernel32.dll").NewProc("SetThreadExecutionState")

const (
	esContinuous      = 0x80000000
	esSystemRequired  = 0x00000001
	esDisplayRequired = 0x00000002
)

// inhibitSleep keeps Windows from idle sleep with SetThreadExecutionState,
// which holds for the calling thread, so one locked thread holds it.
func inhibitSleep(_ string, display bool) func() {
	flags := uintptr(esContinuous | esSystemRequired)
	if display {
		flags |= esDisplayRequired
	}
	done := make(chan struct{})
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		_, _, _ = setThreadExecutionState.Call(flags)
		<-done
		_, _, _ = setThreadExecutionState.Call(esContinuous)
	}()

	return func() { close(done) }
}
