// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

//go:build !catalogbootstrap

package catalog

import "embed"

// files holds the generated lists and their NOTICE. The catalogbootstrap tag
// (bootstrap.go) compiles the package without them, for generating the very
// first *.gob files: go:embed fails on a pattern that matches nothing.
//
//go:embed targets/*.gob targets/NOTICE.md
var files embed.FS
