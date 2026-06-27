# Sonda

[![Go](https://github.com/bassosimone/sonda/actions/workflows/go.yml/badge.svg)](https://github.com/bassosimone/sonda/actions) [![Python](https://github.com/bassosimone/sonda/actions/workflows/python.yml/badge.svg)](https://github.com/bassosimone/sonda/actions) [![codecov](https://codecov.io/gh/bassosimone/sonda/branch/main/graph/badge.svg)](https://codecov.io/gh/bassosimone/sonda)

`sonda` is an experimental network probe that periodically measures DNS,
STUN, and other network properties, storing structured results in a local
spool for later analysis.

## Install

You need Go >= 1.26.

### From source

```bash
go install -v github.com/bassosimone/sonda@latest
```

For local development, `go build .` is fine; the resulting
binary will report its version as `(devel)`.

### As a Debian package

On Debian/Ubuntu, you can build a `.deb` from a source checkout and
install it with `dpkg`. There is no public APT repository; the
package is something you produce locally and install once.

```bash
git clone https://github.com/bassosimone/sonda
cd sonda
./scripts/makedeb.bash
sudo dpkg -i sonda_*.deb
```

The package installs a systemd timer (`sonda-scan.timer`) that
periodically runs scans. If `dpkg -i` complains about missing
runtime dependencies, run `sudo apt-get -f install` to pull them in.

## Quick Start

```bash
sonda --help              # interactive help
sonda scan --help         # scan-specific help
sonda measure --help      # single-measurement help
```

## Subcommands

- `measure` — run a single low-level network measurement.

- `metrics` — aggregate and query measurement metrics.

- `scan` — scan specific network endpoints storing results in the spool.

- `spool` — manage the measurement spool directory.

## Metrics Explorer

A [Streamlit](https://streamlit.io/) app for browsing collected metrics
lives in `research/explorer.py`. To run it:

```bash
uv run streamlit run research/explorer.py [/path/to/metrics]
```

It defaults to reading from `/var/lib/sonda/metrics`.

## License

```
SPDX-License-Identifier: GPL-3.0-or-later
```

## Direct Dependencies

### Go

- [github.com/bassosimone/closepool](https://pkg.go.dev/github.com/bassosimone/closepool)
- [github.com/bassosimone/deferexit](https://pkg.go.dev/github.com/bassosimone/deferexit)
- [github.com/bassosimone/dnscodec](https://pkg.go.dev/github.com/bassosimone/dnscodec)
- [github.com/bassosimone/errclass](https://pkg.go.dev/github.com/bassosimone/errclass)
- [github.com/bassosimone/nop](https://pkg.go.dev/github.com/bassosimone/nop)
- [github.com/bassosimone/runtimex](https://pkg.go.dev/github.com/bassosimone/runtimex)
- [github.com/bassosimone/vclip](https://pkg.go.dev/github.com/bassosimone/vclip)
- [github.com/bassosimone/vflag](https://pkg.go.dev/github.com/bassosimone/vflag)
- [github.com/google/uuid](https://pkg.go.dev/github.com/google/uuid)
- [github.com/miekg/dns](https://pkg.go.dev/github.com/miekg/dns)
- [github.com/parquet-go/parquet-go](https://pkg.go.dev/github.com/parquet-go/parquet-go)
- [github.com/pion/stun/v3](https://pkg.go.dev/github.com/pion/stun/v3)

### Python (research)

- [numpy](https://numpy.org/)
- [pandas](https://pandas.pydata.org/)
- [plotly](https://plotly.com/python/)
- [pyarrow](https://arrow.apache.org/docs/python/)
- [streamlit](https://streamlit.io/)
