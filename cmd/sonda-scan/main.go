// SPDX-License-Identifier: GPL-3.0-or-later

package main

import "github.com/bassosimone/sonda/cmd/internal/plugincommand"

func main() {
	plugincommand.Main(0, scanMain)
}
