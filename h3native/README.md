# h3native — the native naive H3 server

A QUICHE backend that serves an operator-owned website and terminates naive
CONNECT tunnels on one HTTP/3 endpoint, so the **server** half of the connection
uses Chrome's QUIC implementation instead of Go's.

## Why this exists

The client half is already indistinguishable from Chrome: naiveproxy's
`native-h3` profile patches only `src/net/tools/naive/*`, leaving `net/quic`
(except `quic_proxy_client_socket.{cc,h}`), `net/ssl/*` and the vendored QUICHE
byte-identical to stock Chromium. There is nothing to fix there.

The server half was the problem. `h3frontend` terminates QUIC with quic-go, and a
QUIC server's transport-parameter block identifies its implementation. Measured
with `tools/naive-fp`, the two differ on ten properties, including one that a
single handshake reveals:

| property | `h3frontend` (quic-go) | this server (QUICHE) |
|---|---|---|
| `version_information` (0x11) | never sent | always sent |
| parameter encoding order | fixed | shuffled per connection |
| GREASE parameter id | short varint | full 8-byte varint |
| `initial_source_connection_id` length | 4 | 8 |
| `max_udp_payload_size` | 1452 | 1472 |

QUIC Hunter (PAM 2024) identifies 18 server libraries from exactly this
information, and only 4 of them shuffle the parameter order -- QUICHE is one.

**This buys indistinguishability, not speed.** On a long path the client's 15 MiB
receive window, not the server implementation, caps throughput.

## Build

```sh
h3native/build.sh --dir /path/to/quiche-checkout
```

The QUICHE revision is read from upstream naiveproxy's `DEPS` at the commit
`.github/workflows/build-kernel.yml` pins, so it cannot drift from the kernel
build. The script clones (or reuses) a checkout, copies these sources into the
QUICHE package, applies `patches/`, and builds
`//quiche:naivereal_h3_server`.

Prerequisites: `bazelisk`, `libicu-dev`, and a C++20 compiler. On GCC 11 three
compatibility patches are required; they are in `patches/` and are the reason the
script does not build from a completely untouched tree.

`h3native/build.sh --print-revision` prints the pinned QUICHE revision without
building, which is what the workflow uses to check the patches still apply.

## Run

```sh
naivereal_h3_server --port=8443 \
  --certificate_file=/etc/letsencrypt/live/example/fullchain.pem \
  --key_file=/etc/letsencrypt/live/example/privkey.pem \
  --web_root=/var/www/example \
  --username=user --password=pass \
  --upstream_addr=127.0.0.1:18080
```

`--upstream_addr` is the official naive HTTP server, which owns the forward-proxy
and padding logic. Non-CONNECT requests are served from `--web_root`, so an
unauthenticated probe sees an ordinary website.

## Protocol contract

The backend implements the same contract as `h3frontend` and the Go reference in
klzgrad/forwardproxy. Two details are easy to get wrong and cost real debugging:

- **An unauthenticated CONNECT must be answered `407` with `Proxy-Authenticate`.**
  The real naive client issues CONNECT *without* credentials first and only sends
  `Proxy-Authorization` after being challenged. Serving it as a website request
  instead -- the intuitive reading of "do not reveal a proxy" -- makes the real
  client fail with `ERR_QUIC_PROTOCOL_ERROR`. The client sent the CONNECT, so it
  already knows a proxy is there; the challenge reveals nothing.
- **The padding frame's payload length is a `uint16`.** A read of exactly 65536
  bytes encodes as length 0, and the peer then discards the whole chunk as
  padding while still reporting the right total size. Reads are capped below
  65536 and the encoded length is clamped.

## Congestion control

`--bbr_cwnd_gain`, `--max_congestion_window`, `--lumpy_pacing_size` and
`--lumpy_pacing_cwnd_fraction` map onto QUICHE's protocol flags, which QUICHE
defines as plain globals and does not register with the command-line parser.

Tuning these is **not** a fingerprint: they are sender-local dynamics applied
after the handshake, so they change no transport parameter, no frame and no
packet layout. What they change is the timing profile, which matters only to
statistical size/timing analysis. Choose values by measuring on the real path.

## Status

Spike quality, proven end to end: the real naive kernel negotiates
`padding type: Variant1` with this server and moves a 50 MB payload
byte-exact. Not production-ready -- see `docs/native-h3-spike.md` for what
remains, in particular that an unreachable upstream currently blocks the single
event-loop thread and stalls the whole server.
