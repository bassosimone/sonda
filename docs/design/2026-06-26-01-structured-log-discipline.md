---
author: sbs
status: active
---

# Design: structured log discipline

## Why this matters

Sonda's measurement commands emit structured JSON logs to stdout.
The spool stores these as `stdout.txt` files, and every consumer
— Go parsers and extractors, future Python extractors (sondax), any
downstream pipeline — parses the same JSON lines. The schema must
be well-defined and stable because it is the interface between
producers and consumers.

## Event taxonomy

Following ptnop's documentation, events fall into three categories:

- **Span events**: `*Start`/`*Done` pairs bracketing an operation.
  `*Start` carries `t` (start timestamp). `*Done` carries `t0`
  (start) and `t` (end), plus `err` and `errClass` on failure.

- **Wire observations**: single events recording protocol-level
  data observed during a span (e.g., `dnsQuery`, `dnsResponse`).

- **Notifications**: single events reporting a derived result or
  status. These are sonda-specific: command arguments, parsed
  results, failures.

## Naming rules

### Event names (`msg` field)

- Sonda command-layer events use a `sonda` prefix to avoid
  collision with ptnop pipeline events: `sondaFailure`,
  `sondaDnsRecordsA` etc.

- STUN events use a `stun` prefix because STUN binding is a
  protocol-level operation that may migrate to ptnop in the future:
  `stunBindingResult`.

- Ptnop pipeline events (`connectStart`, `tlsHandshakeDone`, etc.)
  are not prefixed. They are defined and tested by ptnop.

### Field names (JSON keys)

- JavaScript casing: lowercase first letter for acronyms.
  `httpRequestUrl`, `httpResponseBodySize`, `tlsServerName`. This
  matches common JSON conventions.

- Qualified names: field names must be unambiguous without the
  event name. `cliArgs` not `args`, `httpResponseBodySize` not
  `size`, `stunReflexiveAddr` not `ip`. A field named `size`
  meaning different things in different events is a schema
  collision waiting to happen.

- No redundancy between event name and field name. The event
  `stunBindingResult` carries `stunReflexiveAddr` and
  `stunReflexivePort` — the field names describe the values,
  not the event.

## Consolidated error handling

All sonda command-layer errors use a single event type:
`sondaFailure` with an `operation` field identifying what failed
and `err` carrying the error description. This replaces 14 ad-hoc
`*Failed` event names with one parameterized type.

For errors without a Go `error` value, `err` carries a synthetic
description: `"missing colon"` for header parsing, `"unknown
query type"` for DNS query type parsing.

## When to split vs. parameterize

- **Parameterize** when the shape is identical and consumers
  handle all variants the same way. `sondaFailure` with
  `operation`: every failure has the same fields, and a consumer
  filtering for failures wants all of them.

- **Split** when consumers care about one variant and would
  need a switch to tell them apart. DNS records use three
  distinct msg names (`sondaDnsRecordsA`, `sondaDnsRecordsAAAA`,
  `sondaDnsRecordsCNAME`) each carrying a `dnsRecordsList`
  field. A parser looking for A records matches on msg directly.

## The union struct

All events are parsed into a single flat Go struct
`ptnopdata.Event` in `internal/ptnopdata`. The `Msg` field
identifies which subset of fields is meaningful.

This lives in sonda, not ptnop, because ptnop does not produce all
fields (sonda adds `spanId`, `operation`, etc.). Ptnop has its own
tests verifying its field names.

### Type choices

- `*Failure` (`type Failure string`) for nullable errors. Named
  type avoids the semantic emptiness of `*string`.

- `[]byte` for `dnsRawQuery` and `dnsRawResponse`. Ptnop emits
  base64-encoded DNS wire bytes, which `encoding/json` decodes
  into `[]byte`. `json.RawMessage` would expect embedded JSON.

- `http.Header` for HTTP request/response headers. Same
  underlying type as `map[string][]string` but more intentional.

- `[]string` for `dnsRecordsList`. Always string slices (IP
  addresses or CNAMEs).

- Minimal pointer usage. Only `*Failure` uses a pointer.

## Contextual tags

Measurement commands accept a repeatable `--tag KEY=VALUE` flag.
Each tag is added to the logger via `logger.With(key, value)`,
so it appears on every structured log line emitted by that span.

Tags are the mechanism for injecting session-level context into
individual measurements. The orchestrator (e.g., `sonda scan`)
discovers context — such as the reflexive IPv4/IPv6 addresses
from a STUN lookup — and propagates it to subsequent measurements
by appending `--tag` flags to the inner command.

Current tag keys:

- `reflexiveAddrV4`: the public IPv4 address discovered by STUN.
- `reflexiveAddrV6`: the public IPv6 address discovered by STUN.

Tag keys follow the same naming rules as other field names
(JavaScript casing, qualified). New tags can be added without
changing the measurement commands as they are generic key-value
pairs.

## Parsing contract

All consumers use `ptnopdata.ParseEvent(line []byte)` instead
of `map[string]any` with type assertions. The schema is explicit
in the struct definition, not implicit in scattered assertions.

Parsers scan line-by-line, skip entries they do not recognize,
and return an error when no matching result is found. This
contract is unchanged from the original design — only the
parsing mechanism is now typed.

## Event inventory

### Sonda command layer

|                         Event |     Category |                                    Fields |
|-------------------------------|--------------|-------------------------------------------|
|              `sondaFailure`   | Notification |            `operation`, `err`, `exitCode` |
|           `sondaDnsRecordsA`  | Notification |                          `dnsRecordsList` |
|        `sondaDnsRecordsAAAA`  | Notification |                          `dnsRecordsList` |
|       `sondaDnsRecordsCNAME`  | Notification |                          `dnsRecordsList` |
|           `stunBindingResult` | Notification | `stunReflexiveAddr`, `stunReflexivePort`  |

### Contextual tags (injected via `--tag`, present on all events)

|             Field |                                      Source |
|-------------------|---------------------------------------------|
| `reflexiveAddrV4` |  STUN lookup, injected by `sonda scan`      |
| `reflexiveAddrV6` |  STUN lookup, injected by `sonda scan`      |

### Ptnop pipeline (see ptnop docs for full list)

All span events carry `t`, `t0`, `localAddr`, `remoteAddr`,
`protocol`. All `*Done` events carry `err` and `errClass`.

|                                    Event | Category |                                                       Key fields |
|------------------------------------------|----------|------------------------------------------------------------------|
|                    `connectStart`/`Done` |   Span   |                                         `remoteAddr`, `protocol` |
|               `tlsHandshakeStart`/`Done` |   Span   |                                `tlsServerName`, `tlsCipherSuite` |
|              `httpRoundTripStart`/`Done` |   Span   |  `httpRequestMethod`, `httpRequestUrl`, `httpResponseStatusCode` |
|  `readStart`/`Done`, `writeStart`/`Done` |   Span   |                                   `ioBufferSize`, `ioBytesCount` |
|                `dnsExchangeStart`/`Done` |   Span   |                                              `dnsServerProtocol` |
|                `dnsQuery`, `dnsResponse` |   Wire   |                                  `dnsRawQuery`, `dnsRawResponse` |
