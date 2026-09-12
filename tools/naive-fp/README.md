# naive-fp

A wire-fingerprint probe for HTTP/3 endpoints, built for the native-H3 spike.

## Why it exists

Server-library classifiers identify a QUIC server from the **shape of its
transport-parameter block** — which parameters are present, and in what order.
QUIC Hunter (PAM 2024) scans millions of addresses this way and deliberately
ignores parameter *values* because those are easy to configure. tlsfingerprint.io
publishes `qtp` fingerprints that do include values, at roughly 8.7e9 observations.

Stock QUIC clients make that comparison impossible: they parse the peer's
transport parameters into a struct and **discard the encoding order**. quic-go's
qlog `ParametersSet` event reports the parsed struct, not the wire order.

So this tool uses a small addition to the pinned `apernet/quic-go` fork that
captures each parameter's raw bytes in wire order while parsing. See
`quic.CapturedTransportParameters` in the fork, added in
`internal/wire/transport_parameters.go` and re-exported from the module root.

## Commands

```
naive-fp probe [flags] <url>      probe one endpoint, write JSON to stdout
naive-fp diff  <a.json> <b.json>  compare two reports; exit 1 if distinguishable
```

`probe` flags: `-authority`, `-path`, `-proto`, `-insecure`.

`diff` exits non-zero when the two endpoints differ on any shape-bearing
property, so it works as a CI gate.

## What is compared

Shape-bearing (a difference here is a real fingerprint):

- the set of transport parameters
- their **encoding order**
- their values

Excluded by construction, because they carry no signal:

- GREASE parameters (random id by definition)
- per-connection random values: `original_destination_connection_id`,
  `stateless_reset_token`, `initial_source_connection_id`,
  `retry_source_connection_id`

`HTTP status` is reported but is usually a probe artifact (a `/` that redirects),
not a server property — do not read it as a tell.

## Example

```
$ naive-fp probe https://cloudflare.com/ > cf.json
$ naive-fp probe -insecure https://127.0.0.1:8443/ > go-frontend.json
$ naive-fp diff cf.json go-frontend.json
```

## Build

```
go build -o naive-fp .
```

The `go.mod` has a `replace` directive pointing at the local fork; adjust it if
the fork lives elsewhere.

## Limits

- Reports only what a completed handshake and one request expose. The first-flight
  packet-length histogram, packet-number length and ECN bits need the packet
  capture path, which is not part of this tool yet.
- The probe does not attempt to reproduce a browser's full header set, so
  application-layer differences it reports are informative, not authoritative.
