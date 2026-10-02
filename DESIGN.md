# Sonda Network Observation Backend

- *status*: temporary

This document lays out a proposal for replacing the
current `measure+spool` subcommands with the `sonda-nob`
(network observation backend) accepting network measurement
requests from the rest of `sonda`.

## Motivation

- (honest) curiosity about making the architecture

- starting to separate backends that do different things
  such as network observation and speed tests and also
  differentiating subsystems

- still wanting to try to preserve the original Gall's Law
  semantics of invoking a process and getting results

- and yet allowing for more efficient operations (with in
  mind the growth trajectory of different kind of
  systems with different needs, e.g. Windows, Android)

## The daemon

The daemon will be an hidden sonda plugin:

    /usr/libexec/sonda/sonda-nob

It will have a minimal manpage explaining that this is
a private component used for measuring and deferring
to `/usr/libexec/sonda/sonda-nob --help` for additional
information about the purpose and usage.

The daemon will be started by `systemd` with very
strict confinement. It should only be able to perform
file I/O inside its own subset of the spool:

    /var/spool/sonda/nob

The daemon should listen on the following socket:

    /var/run/sonda/nob.sock

Generally speaking, the API to communicate with the
daemon is more similar to the current `spool` API
than it is similar to the `measure` API.

## Functionality

We divide functionality by tier. The first tier is
what would allow us to replace the existing code with
minimal changes. Then we also include another tier
that adds nice-to-have functionality.

### Tier 0

1. Ability to programmatically `gc` the spool as
   we can currently do with `sonda spool gc`

2. Ability to programmatically `run` a measurement
   like we currently do with `sonda spool run`

### Tier 1

Main change to be careful about: the spool directory
gains a specific subdirectory for `sonda-nob` and
`sonda-nob` only owns that subdirectory. The chief
reason for this is that I want to also introduce
the ability to run periodic MSAK network performance
tests and this would be a different backend using
a different spool subdirectory.

We might want to introduce the spool directory change as
a subsequent patch, TBH.

### Tier 2

Ability to automatically assign `spanID`s to each
measurement inside `sonda-nob`. This is probably
a better design than having it assigned externally
since the daemon *owns* the spool.

We could keep reading the files directly but we
probably eventually want an API to actually access
the files programmatically so that the daemon
fully mediates the spool.

### Tier 3

1. Ability to stream relevant files belonging to a
   specific measurement `spanID`

2. Ability to delete an existing measurement by `spanID`
   ahead of its scheduled delete time via `gc` [I am
   not super sure about this though].

## API/protocol

### Garbage Collection

Request:

```
POST /sonda-nob/api/v1/gc
```

Response:

```
202 Accepted
```

### Synchronous Command Execution

```
POST /sonda-nob/api/v1/run
Content-Type: application/json

{
    "pipeline": "dns-over-udp|...|tls",
	// arguments we would send to the command line
	// using a flat model, example below
	"tlsAlpn": ["h2", "http/1.1"]
}
```

Response on overload:

```
429 Too Many Requests
```

Response on usage error:

```
400 Bad Request
```

Otherwise:

```
200 Ok
Content-Type: application/json

{"spanId": "..."}
```

Command execution is synchronous.

## Old Notes

The daemon should not allow too many concurrent connections
to the socket. Additionally, I think a relatively simple
model of communication is possible, where each request creates
a new execution context and then the socket is closed.

Actually, IIRC docker uses HTTP to communicate to its backend
and probably also `podman` does the same. If so, then we
could potentially consider a model in which we wrap the
work to do inside HTTP requests.

My main point is that, anyway, the daemon owns the spool
directory and can create files inside it. So, probably
the interface we need to implement is more similar to the
`spool` command interface than it is similar to the
network measurement interface we currently have.

If we accept a model in which the following is true:

1. every command creates the files that the spool
currently creates or similar (so the body file, the
stdout file, the exit code, the commands that were
used, etc)

2. the caller has the ability to fetch files that
make sense within the context of a given measurement

3. the daemon assigns the span IDs unilaterally
and then the caller uses them to decide what
it should actually do

4. we could have an in-memory ring for logs in case
a client wants to stream logs (with a filter like
`journalctl` to only get specific logs)

Then we are probably good to go.

An additional extension could be to decouple the
ability of creating a connection from the ability
to use the connection. This may possibly be YAGNI
at this stage but maybe instead it is something
that we could consider.

That is, this would allow one to say: "create me
a connection for this purpose with those
parameters", which then triggers the proper
pipeline.

Once the pipeline completes, the connection is
there and then we can use it for sending specific
requests or we can close it.

This latter design aspect might bring the design
more in line with a browser network process in
the sense that I suspect, especially when dealing
with JavaScript, objects outlive requests, which
is not a stupid thing to do IMHO, if the cost
of doing this is not immensely high.


