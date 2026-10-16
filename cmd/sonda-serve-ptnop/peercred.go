// SPDX-License-Identifier: GPL-3.0-or-later

package main

// peerCreds contains the credentials of the process connected to the socket.
//
// The PID refers to the process that called connect(2) and is only a hint, since
// that process may have exited, and its PID may have been reused, since then.
//
// We use int64 so that [unknownPeerCreds] serializes as -1 rather than as the
// uint32 representation of (uid_t)-1 and (gid_t)-1, i.e., 4294967295.
type peerCreds struct {
	GID int64 `json:"gid"`
	PID int64 `json:"pid"`
	UID int64 `json:"uid"`
}

// unknownPeerCreds is the value we use when we cannot obtain the peer credentials
// (e.g., with `--stdio`, on non-Unix sockets, or on unsupported platforms).
//
// We use -1 for every field, mirroring the (uid_t)-1 and (gid_t)-1 Linux returns
// for SO_PEERCRED on a TCP socket. Linux returns pid 0 in that case, but -1 for
// all fields is simpler to check.
var unknownPeerCreds = peerCreds{GID: -1, PID: -1, UID: -1}
