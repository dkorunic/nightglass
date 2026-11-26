// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

//go:build catalogbootstrap

package catalog

import "embed"

// files is empty: this build only exists to run
// go test -tags catalogbootstrap ./catalog -run TestEmbeddedLists -args -update
// when no targets/*.gob exists yet (see embed.go).
var files embed.FS
