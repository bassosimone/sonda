// SPDX-License-Identifier: GPL-3.0-or-later

// Package buildcfg holds build-time configuration values.
package buildcfg

import "runtime/debug"

// Version is the program version string. It is set from Go module build
// info at init time; when that is unavailable (plain `go run`, no module
// version embedded) it falls back to "(devel)".
var Version string

func init() {
	var mainVersion string
	if binfo, ok := debug.ReadBuildInfo(); ok {
		mainVersion = binfo.Main.Version
	}
	Version = resolveVersion(Version, mainVersion)
}

func resolveVersion(current, mainVersion string) string {
	if current != "" {
		return current
	}
	if mainVersion != "" {
		return mainVersion
	}
	return "(devel)"
}
