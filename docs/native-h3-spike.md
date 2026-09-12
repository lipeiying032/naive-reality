# Native H3 server spike (stage N0) — measurement report

Status: **the measurement half is complete and decisive; the native server builds,
serves HTTP/3 and terminates CONNECT, but tunnel data forwarding is not yet
working.** The blocker is documented in §6 with its exact cause.

## 0. Headline

A server built from the **pinned QUICHE revision** now runs and answers HTTP/3.
Comparing it against the current Go frontend with a probe that recovers
transport-parameter *encoding order* shows the two are distinguishable on four
independent axes, and that the native server matches QUICHE's shape on all of
them:

| axis | Go `h3frontend` | native QUICHE server | reference origin |
|---|---|---|---|
| `version_information` (0x11) | **absent** | **present** | absent (this origin) |
| parameter order | **fixed**, `bidi_local` first | **shuffled per connection** | fixed |
| GREASE parameter id length | short | **full 8-byte varint** | no GREASE |
| `initial_source_connection_id` length | **4** | **8** | 20 |
| `max_udp_payload_size` | 1452 | **1472** | 1350 |
| `max_datagram_frame_size` | absent | **present (65536)** | absent |

The `version_information` finding is the important one: it settles a claim that
had only been source-derived. **The native QUICHE server always sends 0x11; the
Go server never does.** A single completed handshake separates them, with no
corpus required.

## 1. What the spike set out to test

The client half is already Chrome-identical: naiveproxy's `native-h3` profile
touches only `src/net/tools/naive/*`, leaving `net/quic` (bar
`quic_proxy_client_socket.{cc,h}`), `net/ssl/*` and the whole vendored QUICHE
byte-identical to stock Chromium. The open question was whether replacing the
**server** transport with QUICHE removes the server's implementation
fingerprint.

## 2. Tooling built

`tools/naive-fp/` — an HTTP/3 wire-fingerprint probe, with `probe` and `diff`
subcommands. `diff` exits non-zero when two endpoints differ on any
shape-bearing property, so it works as a CI gate.

The blocking discovery: **stock QUIC clients discard transport-parameter
encoding order**, which is precisely the axis server-library classifiers score
on. quic-go parses parameters into a struct and its qlog `ParametersSet` event
reports the parsed struct. Order is unrecoverable after parsing.

The pinned fork therefore carries a capture hook that records each parameter's
raw bytes in wire order while parsing, re-exported as
`quic.CapturedTransportParameters()`. This is the only fork modification, and it
is read-only instrumentation.

Two calibration results that matter for how much to trust order as a signal:

- A public origin returned the **same order on every probe**, so order is a
  stable property of a deployment, not noise.
- The **native QUICHE server shuffled its order on every connection**. Chrome
  randomises the order per handshake, which is why QUIC Hunter finds only 4 of
  18 server libraries doing so — and why "shuffles" is itself the QUICHE
  signature.

## 3. Measured: the Go frontend is distinguishable

`naive-fp diff` between a reference origin and a locally run `h3frontend`
(origin mode, own certificate) reports **12 observable differences**. Excluding
per-connection random values, the shape-bearing ones are the first five rows of
the table in §0, plus `max_ack_delay` (26 vs 25), `initial_max_data` (20 MiB vs
10 MB) and the three `initial_max_stream_data_*` (8 MiB vs 1 MB).

Two points deserve emphasis:

- The **order and set** differences give a classifier of QUIC Hunter's kind ample
  signal to label the Go server as quic-go. This is measured, not inferred.
- The configured receive windows (8 MiB / 20 MiB) appear directly as parameter
  values. They were chosen to match Hysteria2; they do not match Chrome's
  6 MiB / 15 MiB, so they cost distinguishability without a demonstrated
  throughput benefit.

## 4. Measured: the native server

`bazel build //quiche:naivereal_h3_server` produces a working server:

```
naivereal_h3_server --port=8443 --certificate_file=... --key_file=... \
  --web_root=... --username=... --password=... --upstream_addr=127.0.0.1:18080
```

It serves the website over HTTP/3 (`200`, `HTTP/3.0`) and its transport
parameters are QUICHE's, as tabulated in §0. `version_information` decodes as
chosen v1 with available `{v1, GREASE, v2, draft-29, Q046}`.

The server advertises QUICHE's conservative flow-control defaults
(`initial_max_data` 1 MiB, `initial_max_stream_data_*` 64 KiB, streams 100/103).
Whether those bind on a real path is a throughput question, not a fingerprint
one, and belongs to the production phase.

## 5. Build feasibility (this was a real risk, now resolved)

- The pinned revision `997d654308b6a1a17435e472ef5190aecb12e3eb`
  (`naiveproxy src/DEPS:438`) fetched cleanly from `quiche.googlesource.com`.
- The standalone QUICHE repository is self-contained for Bazel
  (`.bazelversion = 8.2.1`, `MODULE.bazel` pulling abseil, protobuf, boringssl,
  re2, zlib from the Bazel Central Registry). **No `gclient` checkout and no
  change to naiveproxy's build files were needed.**
- QUICHE's `cc_library` targets are `//visibility:private`, so the naive server
  lives in QUICHE's own package and five targets carry an explicit public
  visibility, all marked `NAIVEREAL_VISIBILITY_PATCH`.

Three compatibility fixes were required, each reproducible and documented in
`MODULE.bazel` / the source:

1. `com_google_googleurl` ships three `[[noreturn]]` literal-conversion traps as
   `static_assert(false, ...)`, which a conforming pre-C++23 compiler rejects at
   definition time. GCC 11 here; Chromium builds with a C++23-era clang. Fixed
   with a `patch_cmds` that makes the assertion depend on the template
   parameter.
2. The same dependency declares a `constexpr` `operator<=>` over `std::string`,
   which pre-C++23 libstdc++ cannot constant-evaluate. `constexpr` removed.
3. QUICHE's `QuicConfig::ClientRequestedIndependentOptions` holds a
   `static constexpr QuicTagVector`; `std::vector` is not a literal type before
   libstdc++ 12. `constexpr` removed. The value is unchanged.

Installing `libicu-dev` is also required (gurl builds against system ICU on
Linux).

**These are the true cost of the native path on an older toolchain.** On a
modern clang they would not arise; the build host here has only GCC 11.

## 6. Blocker: tunnel data forwarding

CONNECT reaches the backend and authenticates, but no data flows. Traced
step by step to a single call:

```
[naive] CONNECT headers: :authority :method padding padding-type-request
        proxy-authorization accept-encoding user-agent authorized=1
[naive] Start: entered
[naive] Start: resolving
[naive] Start: spawning connect thread
[naive] thread: ConnectBlocking entered
   <never returns>
```

`quic::ConnectingClientSocket::ConnectBlocking()` never returns for the socket
that `EventLoopSocketFactory` produces, so `ConnectComplete` is never called, no
`200` is sent, and the client times out.

Three approaches were tried and are worth recording so the next attempt does not
repeat them:

- **`ConnectBlocking()` on the event-loop thread** — deadlocks the server.
  QUICHE runs every session on one thread, so blocking there also blocks the
  stream waiting for the response.
- **`ConnectBlocking()` on a helper thread** — the current state. It hangs
  instead of returning, and if it were to return while still `kConnecting`, a
  later `Disconnect()` trips
  `QUICHE_DCHECK(connect_status_ != kNotConnected)` in
  `event_loop_connecting_client_socket.cc:124`.
- **`ConnectAsync()`** — the intended API, but it only makes progress while the
  server's event loop runs, and the loop is not reachable from a
  `QuicSimpleServerBackend`. Exposing it needs a `SetEventLoop` hook, which
  requires `quic_event_loop.h` to be declared by `quiche_tool_support` — it is
  not, so a sibling include fails Bazel's inclusion check.

- **A plain POSIX socket** (`naive_posix_client_socket.{h,cc}`, a minimal
  `ConnectingClientSocket`) — implemented and wired in, to remove the event loop
  from the picture entirely. It builds cleanly, but `ConnectBlocking()` on it
  also does not return from the helper thread, so this did **not** unblock the
  tunnel either. That result is itself informative: the stall is not specific to
  `EventLoopConnectingClientSocket`, which points at the calling pattern — a
  detached thread invoking a callback that writes to the H3 stream — rather than
  at QUICHE's socket implementation.

**The next things to try**, in order of likelihood:

1. Compare against `ConnectServerBackend` + `ConnectTunnel`, which do work
   upstream. They are the reference implementation for this exact integration,
   and the difference between them and this backend is the shortest path to the
   fix.
2. Move the tunnel onto its own thread with its own blocking socket and hand
   work to the QUIC thread through the stream's own API, instead of calling the
   backend's `RequestHandler` from a foreign thread.
3. Give the backend the event loop and use `ConnectAsync` (add the hook the same
   way `SetSocketFactory` is wired, declaring `quic_event_loop.h` in the target's
   `hdrs`).

This is an integration problem in a convenience API, not a protocol or
architecture problem: the same backend shape already works upstream in
`ConnectServerBackend`, which is what `ConnectTunnel` was written against.

## 7. What the spike establishes

| question | answer |
|---|---|
| Can the pinned QUICHE be built standalone? | **Yes.** Bazel, no gclient, no naiveproxy changes; three compiler-compatibility fixes on GCC 11. |
| Does a native QUICHE server remove the server-library fingerprint? | **Yes, on every axis measured** — `version_information`, order shuffling, GREASE id length, connection-ID length, payload size, datagram frame size. |
| Does it fix the client half? | Nothing to fix: the client is already byte-identical to Chrome. |
| Does it fix throughput? | **No, and it cannot.** See §8. |
| Is it production-ready? | **No.** Tunnel forwarding is not working (§6), and the toy server architecture is explicitly not performance-oriented. |

## 8. What this spike does not settle

- **The client-side window ceiling.** A Chromium/naive client advertises
  `initial_max_data = 15 MiB` and never grows it: receive-window auto-tuning is
  server-only (`quic_session.cc:178-185`). No server implementation can raise
  that. Whether it binds depends on RTT.
- **Throughput.** `quic_server.h` states it is "in no way expected to be
  performant"; the generator-based `quic::QuicServer` + `Http3ServerBackend`
  architecture is a production-phase choice. Any number measured here is a lower
  bound.
- **Statistical traffic analysis.** Xue et al. (USENIX Security 2024) measured
  naiveproxy's padded, multiplexed configuration at TPR 0.32772 at FPR
  0.0544%, using only packet size, timing and direction. Nothing here addresses
  that class of attack, and the forward-proxy request headers (`padding`, no
  `User-Agent`) are identical on the native path by design.
- **The forward-proxy application layer.** The naive CONNECT request carries
  `padding` and `padding-type-request` headers and no `User-Agent`; only a
  naive-aware server echoes `Padding`. That is visible to the server and to
  anyone who can decrypt, on either implementation.

## 9. Gate recommendation

**Conditional go, with the tunnel blocker resolved first.**

The fingerprint case for the native server is now measured rather than argued,
and it is as strong as expected: `version_information` alone separates it from
the Go server in one handshake. The build path is proven end to end at the
pinned revision.

But the spike also clarifies what the native path does *not* buy, and the honest
balance is:

- **What native buys:** server-role transport indistinguishability, which the Go
  server cannot have by construction.
- **What it costs:** a second language and build system in the deployment, three
  toolchain-compatibility patches in a vendored tree, a proxy backend that must
  be written and maintained in C++, and the loss of `h3frontend`'s working test
  suite and status tooling.
- **What it does not buy:** throughput, and any reduction in the size/timing
  signal that the published measurement actually detects.

So the recommendation is:

1. **First**, finish the tunnel with a plain POSIX socket (est. small), and run
   the end-to-end test against the real patched kernel. Without this the native
   server is not a server.
2. **Then** measure throughput against `h3frontend` on the same VPS. If the
   native server is not at least comparable, the fingerprint gain has to be
   weighed against a real performance regression, and that trade must be made
   explicitly.
3. **In parallel and independently**, apply the four cheap corrections to the Go
   frontend that the probe already identified: advertise Chrome's window values,
   send `ack_delay_exponent`, stop sending `active_connection_id_limit`, and fix
   the 404 fallback. These cost nothing and remove the differences that do not
   require an architecture change.

The strongest argument against a full native rewrite is §8's first item: the
client's 15 MiB window, not the server implementation, is what caps throughput
on a long path, and no server-side work changes it.

## 10. Reproducing

```sh
# toolchain
apt-get install -y libicu-dev
curl -sSL -o /opt/bin/bazelisk https://github.com/bazelbuild/bazelisk/releases/latest/download/bazelisk-linux-amd64
chmod +x /opt/bin/bazelisk

# pinned tree
git clone --filter=blob:none --bare https://quiche.googlesource.com/quiche q
cd q && git archive 997d654308b6a1a17435e472ef5190aecb12e3eb | tar -x -C ../quiche
cd ../quiche && /opt/bin/bazelisk build //quiche:quic_server --jobs=1
/opt/bin/bazelisk build //quiche:naivereal_h3_server --jobs=1

# measurement
cd tools/naive-fp && go build -o naive-fp .
naive-fp probe https://origin.example/ > reference.json
naive-fp probe -insecure https://127.0.0.1:8443/ > native.json
naive-fp diff reference.json native.json
```
