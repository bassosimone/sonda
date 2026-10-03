// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !linux

package main

import (
	"errors"
	"syscall"
)

// peerCred always returns [unknownPeerCreds] and [errors.ErrUnsupported] on this platform.
//
// TODO(bassosimone): OpenBSD has SO_PEERCRED with a different struct, while
// FreeBSD and macOS have LOCAL_PEERCRED and getpeereid(3).
func peerCred(conn syscall.Conn) (peerCreds, error) {
	return unknownPeerCreds, errors.ErrUnsupported
}
