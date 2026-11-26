// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

//go:build !darwin && !linux && !windows

package control

func inhibitSleep(string, bool) func() { return func() {} }
