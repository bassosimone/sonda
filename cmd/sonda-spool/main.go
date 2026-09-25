// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"github.com/bassosimone/sonda/cmd/internal/subcommand"
	"github.com/bassosimone/sonda/internal/cli/spool"
)

func main() {
	subcommand.Main(spool.Main)
}
