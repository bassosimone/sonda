// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"github.com/bassosimone/sonda/cmd/internal/subcommand"
	"github.com/bassosimone/sonda/internal/cli/metrics"
	"github.com/bassosimone/sonda/internal/cli/scan"
	"github.com/bassosimone/vclip"
)

func main() {
	subcommand.Main(
		"sonda-qoe",
		func(disp *vclip.DispatcherCommand) {
			disp.AddCommand("metrics", vclip.CommandFunc(metrics.Main), metrics.ShortDescr)
			disp.AddCommand("scan", vclip.CommandFunc(scan.Main), scan.ShortDescr)
		},
	)
}
