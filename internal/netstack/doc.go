// SPDX-License-Identifier: GPL-3.0-or-later

// Package netstack provides high-level network operations (DNS resolution,
// IP address lookup, etc.) built on top of sonda's CLI measurement primitives. Each
// operation runs `sonda measure` through the spool and parses the results.
package netstack
