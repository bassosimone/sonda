---
author: sbs
status: active
---

# Design: scan scheduler

## Purpose

Run workflows at different cadences (e.g., DNS every few minutes,
large downloads a few times a day) from a single timer.

## How it works

- `sonda-scan.timer` starts the scan five minutes after the
previous one finishes. Each scan runs only the workflows that
are due according to their `cadence`.

- `/var/lib/sonda/scan/sched.json` stores `lastRun` and `nextRun`
for each workflow path. Entries whose `lastRun` is older than a
week are pruned.

- The delay before the next run is an exponential sample whose
mean is the cadence, clamped to [10%, 250%] of it. This spreads
probes in time and approximates the PASTA property of Poisson
sampling, which a fixed cadence lacks. Clamping lowers the mean
to ~92% of the cadence (0.1 + e^-0.1 - e^-2.5).

- `/var/lib/sonda/scan` is `2750`: if a group member ran a scan,
the temporary file plus rename would leave `sched.json` owned
by that user. Manual runs go through `systemctl start sonda-scan`
or use a private `--scheduler-state`.

## Rejected alternatives

- One systemd timer per workflow (e.g., a separate timer for the
bandwidth workflow): more units to ship and keep in sync.

## Known limitations

- Nothing prevents a manual run from overlapping with a timer run
(both could run the same due workflow). A whole-run `flock` in
`sonda-scan` would fix this.

- The scan only checks due workflows at timer ticks (every five
minutes plus the scan duration), so run times are rounded up to
ticks. This truncates much of the distribution: `@frequent` runs
at the next tick ~63% of the time (1 - e^-1), so it is close to
periodic, and slower cadences keep their randomness only at tick
granularity. Avoiding this would need an always-on scheduler.
