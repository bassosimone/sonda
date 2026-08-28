---
author: sbs
status: active
---

# Design: Debian packaging

## Purpose

Ship sonda as a `.deb` package so that installation handles
everything the binary alone cannot: creating a dedicated system
user, setting up the spool directory with correct ownership and
permissions, and installing systemd units for periodic execution.

## Directory layout

Packaging artifacts live under `dist/`, not `scripts/`:

- `dist/debian/` — Debian control file (templated), copyright,
  lintian overrides, and maintainer scripts (`postinst`,
  `postrm`, `prerm`).
- `dist/unix/` — static files (systemd units, manpage, default
  scan config) laid out mirroring their install paths on a
  modern Unix (e.g. `dist/unix/usr/share/man/man1/sonda.1`).
- `scripts/makedeb.bash` — builds the Go binary, substitutes
  templates, assembles the staging tree, calls `dpkg-deb`, and
  runs `lintian` on the result (errors fail the build).
  Contains no heredocs; all metadata lives in `dist/`.

The binary installs to `/usr/bin/sonda`, not `/usr/sbin/`,
because it is not an administration tool.

## Scheduling

The timer uses two triggers:

- `OnActiveSec=10s` — fires 10 seconds after the timer unit
  is started, providing the initial run. `OnBootSec` was
  rejected because it measures from system boot, not from
  timer activation — on a long-running system the trigger
  time is already past and systemd skips it silently.

- `OnUnitInactiveSec=60s` — fires 60 seconds after the
  service finishes. This spaces runs relative to completion,
  not relative to start, avoiding pile-up when a scan takes
  longer than the interval.

Overlap is impossible: systemd will not start a service that
is already running. `AccuracySec=1s` prevents coalescing
delays. `Persistent=true` fires a missed run on next boot.

## Security

The service runs as `User=_sonda`, `Group=_sonda` — a
dedicated system account with no home directory, no login
shell, and no capabilities.

Systemd hardening directives are applied in three tiers:

1. Filesystem isolation: `ProtectSystem=strict` (read-only
   root), `ReadWritePaths=/var/spool/sonda` (the one
   exception), `ProtectHome=yes`, `PrivateTmp=yes`.

2. Kernel isolation: `PrivateDevices=yes`,
   `ProtectKernelTunables=yes`, `ProtectKernelModules=yes`,
   `ProtectKernelLogs=yes`, `ProtectControlGroups=yes`.

3. Privilege restriction: `NoNewPrivileges=yes`,
   `CapabilityBoundingSet=` (empty), `RestrictSUIDSGID=yes`,
   `RestrictNamespaces=yes`, `LockPersonality=yes`,
   `MemoryDenyWriteExecute=yes`, `RestrictRealtime=yes`,
   `SystemCallFilter=@system-service`.

All three tiers are safe for a Go binary that only needs
outbound network access (UDP for DNS/STUN, HTTPS for DoH)
and write access to the spool.

## Spool permissions

The spool directory is owned by `_sonda:adm` with mode `2750`
(setgid). The `adm` group is the Debian convention for users
who can read monitoring and log data.

The setgid bit causes new files and subdirectories to inherit
the `adm` group from the parent, even though the process runs
as `_sonda:_sonda`. This avoids granting the sonda process
`adm` group membership, which would give it read access to
syslog and other sensitive files.

Files are created with mode `0640` and directories with `0750`.
The kernel propagates the setgid bit to subdirectories
automatically. The resulting permission model:

- `_sonda` can read and write everything.
- Members of `adm` can read everything.
- Other users have no access.

## Package lifecycle

The systemd interactions follow the maintainer-script snippets
that `dh_installsystemd` generates for real Debian packages
(see the comments in the scripts themselves): enabling goes
through `deb-systemd-helper`, so upgrades honour the sysadmin's
enable/disable choice; starting and stopping go through
`deb-systemd-invoke`, which respects `policy-rc.d`; and every
interaction is skipped when systemd is not running (chroots).

`postinst` (runs on install and upgrade):

1. Creates the `_sonda` system user and group if absent.
2. Creates `/var/spool/sonda` and `/var/lib/sonda/metrics`
   with `2750 _sonda:adm`.
3. Reloads systemd and enables/starts the timer as above.

`prerm` (runs on remove): stops the timer.

`postrm` (runs on remove and purge):

- On `remove`: reloads systemd to forget the removed units.
- On `purge`: removes the spool and metrics directories, the
  enable symlinks with their helper state, and the `_sonda`
  user and group.

The split means `apt remove` preserves collected data and
the system user; `apt purge` cleans up completely.

## Versioning

The package version derives from `git describe --tags` plus
a UTC timestamp (e.g. `0.3.0~20260828105453-1`). The `~`
sorts lower than the bare tag version in dpkg, so a future
tagged release is seen as an upgrade over the snapshots
built before it.
