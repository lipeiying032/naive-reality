# Native H3 server spike (stage N0) — measurement report

Status: **complete, including the real client.** The native server builds from
the pinned QUICHE revision, serves the website over HTTP/3, terminates naive
CONNECT tunnels, and moves a 50 MB payload byte-exact through the **real naive
kernel** (a `native-h3` build). Its transport-parameter fingerprint is measurably
QUICHE's rather than quic-go's.

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

## 6. Tunnel: working, and the two bugs that were in the way

The tunnel now passes end to end:

```
fronting ok: GET / -> 200, 14 bytes served as a website
fronting ok: bad-password CONNECT refused without a response (H3_CONNECT_ERROR)
CONNECT established: status=200 padding-type-reply=1 padding-header=37 bytes
round 1: 65 bytes echoed identically
round 2: 66 bytes echoed identically
...
round 12: 76 bytes echoed identically
bulk: 262144 bytes echoed identically
PASS: tunnel payload round-tripped byte-for-byte
```

That exercises the whole naive contract: classic CONNECT with `Proxy-Authorization`,
per-request authorization, `padding` / `padding-type-request` negotiation, the
`padding-type-reply` echo with a `[30, 62)`-length padding header, padding removed
on client→target bytes, padding added on target→client bytes for the first 8
frames of each direction, and payload compared byte-for-byte.

`-rounds 12` deliberately crosses the 8-frame boundary, so both the padded and
the unframed path are covered; `-bulk 262144` pushes 256 KiB in each direction,
which spans many QUIC packets and exercises flow control on both sides. The test
is repeatable and was run repeatedly without failure.

### 6.1 The stall: it was the upstream, not the socket

Two rounds of debugging chased the wrong suspect. `ConnectBlocking()` appeared to
hang, and I built a full plain-POSIX `ConnectingClientSocket`
(`naive_posix_client_socket.{h,cc}`) to route around QUICHE's event-loop socket.
It hung identically — which was the useful signal: the stall was not in the
socket implementation.

The actual cause was that **the test's echo upstream was dead**. `ConnectBlocking`
on a blocking socket to a closed local port did not return promptly, so the
server's single event loop thread stalled inside it, its UDP receive queue backed
up to ~43 KB, and every later request timed out. The fix was to the test harness:
run the echo upstream detached (`setsid nohup`), so it survives the shell that
started it. A secondary fix was to run `ConnectBlocking` synchronously on the
event-loop thread, matching `ConnectTunnel::OpenTunnel` upstream, instead of on a
detached helper thread.

**Lesson worth keeping**: a blocked `ConnectBlocking` in this design takes down
the entire server, not just one tunnel, because QUICHE runs every session on one
thread. A production backend must bound the connect (non-blocking connect with a
timeout, or a connect on a worker thread that never blocks the event loop) rather
than rely on the peer being reachable. The plain-POSIX socket class is kept in
`src/` for that work: it is the natural place to implement a bounded connect.

### 6.2 A real interop bug: capitalised response header

After the stall was fixed, the client rejected the response with
`header field is not lower-case: Padding`. The naive Go server sets `Padding`
(see `forwardproxy.go`), but **HTTP/3 requires lower-case field names** and
quic-go enforces it. Since the naive client matches header names
case-insensitively (`kPaddingHeader = "padding"`), lower-case is both correct and
compatible. Fixed in the backend; this would have broken every real client.

## 6.3 Found by running the real client: the 407 challenge is mandatory

Running the real naive binary against the native server surfaced a bug that the
protocol-level test could not, because that test sent credentials on the first
CONNECT.

The real client issues CONNECT **without** `Proxy-Authorization`, then sends the
header only after being challenged:

```
[naive] CONNECT authorized=0 headers: :authority :method proxy-authorization
        padding padding-type-request accept-encoding user-agent
[naive] CONNECT authorized=1 ...
```

The first version of the backend followed the natural reading of "do not reveal
that a proxy exists" and served an unauthenticated CONNECT as a website request
(405). That is wrong. `forwardproxy.go` answers with **407 plus
`Proxy-Authenticate: Basic realm="Caddy Secure Web Proxy"`**, and the client
depends on it. Without the challenge the client fails with
`ERR_QUIC_PROTOCOL_ERROR` and never authenticates.

This is worth stating plainly because the "obvious" probe-resistance choice is
the broken one: the client itself sends CONNECT, so it already knows it is
talking to a proxy, and the challenge reveals nothing it did not know. The
backend now challenges, and the trace above shows the client retrying with
credentials.

**The lesson for the port**: the protocol-level test covers framing and padding,
but it does not cover the client's actual request sequence. Auth handshakes,
retry behaviour and error surfacing only appear when the real binary runs.

## 6.4 The real-kernel gate: passed

The end-to-end gate now passes against the **real naive kernel**, not just the
protocol-level client:

```
[INFO] Preamble https://127.0.0.1:8444/
[INFO] Connection 0 to 127.0.0.1:18081 via QUIC 127.0.0.1:8444
[INFO] quic://127.0.0.1:8444 negotiated padding type: Variant1
[INFO] Connection 0 closed: OK

$ curl --socks5-hostname ... http://.../big.bin
code=200 size=50000000
50 MB via naive kernel -> native QUICHE server: BYTE-EXACT
```

`negotiated padding type: Variant1` is the kernel confirming that it accepted the
server's padding negotiation and will use the naive padding frame — i.e. the
padding implementation interoperates with the real client, not only with our own
test client.

### Getting there: the release artifact was the wrong profile

`naivereal-kernel-linux-x64.tar.xz` from release **1.3.2** is the `tcp-reality`
profile (its `--help` advertises `--reality-server-name`, which `native-h3`
rejects by design). It fails identically against the Go frontend, which is how
the mismatch was identified — not evidence against the native server.

The working binary came from **CI artifact `kernel-x64` of run 34017096766**,
whose `kernel_profile` input defaulted to `native-h3`. That artifact was still
unexpired. **Worth fixing upstream: the release should publish both profiles, or
name them explicitly**, so the correct kernel does not depend on an unexpired CI
artifact.

## 6.5 A real bug the bulk transfer exposed: 16-bit frame-length overflow

The protocol-level test passed because its payloads were tiny. Pushing a real
file through found a corruption bug immediately:

```
code=200 size=50000000        <- the right NUMBER of bytes
origin sha256: 840a8201c4169cf1b223010f62714497...
via    sha256: 5ee42afbe6470777f71bc39ecd52e644...   <- wrong CONTENT
```

The first differing byte was at offset 15928 and everything after it was
shifted: a whole chunk had gone missing.

Cause: the tunnel's read chunk was **exactly 65536 bytes**, and the naive padding
frame stores the payload length in a `uint16`. 65536 encodes as **0**, so the peer
read a zero-length payload, treated the real 64 KiB as padding, and discarded it.
Everything after that was offset by one chunk.

Fixed by keeping reads strictly below 65536 (`kReadChunkSize = 65535`) and by
clamping the encoded length in `NaivePaddingFramer::AddPadding` so the header can
never alias to zero. The 50 MB transfer is byte-exact afterwards.

**The lesson mirrors §6.3**: a test whose payloads never cross a frame boundary
cannot find a frame-boundary bug. Both of the last two real bugs were invisible to
the synthetic client and appeared within minutes of running real data.

## 6.6 A crash the bulk transfer also exposed

`StatusOr<QuicheMemSlice>` aborted with *"An OK status is not a valid constructor
argument to StatusOr<T>"* (`absl/status/statusor.cc:79`), killing the whole
server. It only appeared once a transfer was large enough to need many reads.

`QuicheMemSlice` is move-only and takes its value from a `QuicheBuffer`, so
returning one **directly** from a function whose return type is
`StatusOr<QuicheMemSlice>` makes absl's overload resolution select
`StatusOr(Status)` and abort on the OK status.

Two attempts failed before the real cause was clear, and the wrong turn is worth
recording:

- **`absl::StatusOr<T> result(absl::in_place, std::move(owned));`** — `in_place`
  selects a **single-argument** constructor, so this compiled while silently
  dropping the buffer, and the crash came back on the next large transfer.
  Compiling is not evidence of correctness here.
- Making the temporary's construction "more explicit" changed nothing, because
  the ambiguity is in the *return*, not the construction.

The fix is to build the slice as a named value and move it into the `StatusOr`:

```cpp
quiche::QuicheMemSlice slice(std::move(owned));
absl::StatusOr<quiche::QuicheMemSlice> result(std::move(slice));
return result;
```

Verified afterwards: 3 x 2 MB bulk rounds through the tunnel with zero aborts,
and a full 50 MB real-kernel transfer with the server still alive.

## 7. What the spike establishes

| question | answer |
|---|---|
| Can the pinned QUICHE be built standalone? | **Yes.** Bazel, no gclient, no naiveproxy changes; three compiler-compatibility fixes on GCC 11. |
| Does a native QUICHE server remove the server-library fingerprint? | **Yes, on every axis measured** — `version_information`, order shuffling, GREASE id length, connection-ID length, payload size, datagram frame size. |
| Does it fix the client half? | Nothing to fix: the client is already byte-identical to Chrome. |
| Does it fix throughput? | **No, and it cannot.** See §8. |
| Does the tunnel work? | **Yes against a protocol-level client** — CONNECT, per-request auth including the 407 retry sequence, padding negotiation in both directions, byte-exact payload (§6, §6.3). |
| Does it work against the real naive kernel? | **Yes.** A `native-h3` CI artifact negotiates `padding type: Variant1` with the server and moves 50 MB byte-exact (§6.4). |
| Throughput | **~2.8 MB/s here against 196 MB/s direct.** This host is CPU-bound and the path is loopback, so the number says nothing about a real link -- but the gap is unexplained and is the first thing a production phase must profile (§8). |
| Is it production-ready? | **Not yet.** The toy server architecture is explicitly not performance-oriented, the connect is unbounded (§6.1), and it has not been run against the real naive kernel. |

## 8. What this spike does not settle

- **The client-side window ceiling.** A Chromium/naive client advertises
  `initial_max_data = 15 MiB` and never grows it: receive-window auto-tuning is
  server-only (`quic_session.cc:178-185`). No server implementation can raise
  that. Whether it binds depends on RTT.
- **Throughput.** `quic_server.h` states it is "in no way expected to be
  performant"; the generator-based `quic::QuicServer` + `Http3ServerBackend`
  architecture is a production-phase choice.

  What was measured: **~2.8 MB/s through the tunnel against 196 MB/s direct** on
  the same host and file. That is a wide gap and it is *not* explained yet.

  What can be ruled out as the cause: the client's receive window. On loopback
  the RTT is ~0.05 ms, so a 15 MiB window permits ~300 GB/s — three orders of
  magnitude above what was measured. Nor is the host saturated: the server
  process sat near 36% of one core during a transfer.

  So the gap lives in the tunnel relay path, and the read-chunk experiment is a
  clue rather than a conclusion: reducing `kReadChunkSize` from 64 KiB to 16 KiB
  appeared to cut throughput roughly 4x, but restoring it to 65535 did not
  restore the earlier rate, so that comparison was confounded by first-request
  effects. The prime suspects are the strict request/response alternation in
  `ReceiveAsync` (one outstanding read at a time, re-armed through the event
  loop) and per-chunk overhead in the framer. Profiling the relay is the first
  task of any production phase.

- **Congestion-control tuning is not a fingerprint, but this host cannot
  demonstrate its benefit.** BBR's congestion window gain and pacing behaviour
  are sender-local dynamics applied after the handshake: they change no transport
  parameter, no frame and no packet layout, so the server-library fingerprint is
  identical either way. The binary now exposes `--bbr_cwnd_gain`,
  `--max_congestion_window`, `--lumpy_pacing_size` and
  `--lumpy_pacing_cwnd_fraction` for that purpose.

  Measured here, tuning them (cwnd gain 2.0 -> 2.5, max cwnd 2000 -> 8000, lumpy
  burst 2/0.25 -> 8/0.5) made no positive difference: 2.81 MB/s versus a 3.02 MB/s
  baseline. That is the expected result on a lossless loopback path, where the
  bottleneck is neither window nor loss. **The tuning surface is correct and the
  fingerprint argument stands; the measurement to justify a particular setting
  has to happen on a real path with RTT and loss.**
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

**Conditional go. The native path is proven feasible and its fingerprint benefit
is measured; what remains is a production engineering decision, not a research
question.**

The fingerprint case is now measured rather than argued: `version_information`
alone separates the native server from the Go one in a single handshake, and the
order-shuffling and connection-ID differences compound it. The build path is
proven at the pinned revision and the tunnel works against a protocol-level
client.

The honest balance:

- **What native buys:** server-role transport indistinguishability, which the Go
  server cannot have by construction — the Go server will always advertise
  quic-go's parameter set, order and connection-ID length.
- **What it costs:** a second language and build system in the deployment, three
  toolchain-compatibility patches in a vendored tree, a proxy backend written and
  maintained in C++, and the loss of `h3frontend`'s working test suite and status
  tooling. The 8 GB / 1.5-hour build is also a real CI cost.
- **What it does not buy:** throughput; and no reduction in the size/timing
  signal that the published measurement actually detects (§8).

What remains before a production decision, in order:

1. **Profile the tunnel relay.** ~2.8 MB/s against 196 MB/s direct is unexplained
   and is the one number that could veto the whole approach (section 8). Start
   with the one-outstanding-read-at-a-time pattern in `ReceiveAsync`.
2. **Bound the connect.** As written, an unreachable upstream blocks the single
   event-loop thread and takes down every session, not just one tunnel (section
   6.1). This is a correctness requirement, not an optimisation.
3. **Measure throughput against `h3frontend` on a real path** with RTT and loss.
   That is also the only place the congestion-control settings can be justified;
   on loopback they changed nothing (section 8).
4. **In parallel and independently, apply the cheap corrections to the Go
   frontend** that the probe already identified: advertise Chrome's window values
   (`6 MiB` / `15 MiB` instead of `8 MiB` / `20 MiB`), send `ack_delay_exponent`,
   stop sending `active_connection_id_limit`, use an 8-byte source connection ID,
   and fix the 404 fallback. These cost nothing and remove every difference that
   does not require an architecture change. **If the goal is "no implementation
   fingerprint", this closes most of the measured gap without a rewrite** -- but
   not the `version_information` and order-shuffling differences, which are
   intrinsic to quic-go, and those are exactly what a server-library classifier
   reads.

The strongest argument against a full native rewrite remains §8's first item: the
client's 15 MiB window, not the server implementation, caps throughput on a long
path, and no server-side work changes it. The native server should be adopted for
the fingerprint property, not for speed.

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

# tunnel: website, probe resistance and padded CONNECT payload
python3 echo_upstream.py 18080 &          # stand-in for the naive HTTP server
cd tunnelcheck && go build -o tunnelcheck .
./tunnelcheck -server 127.0.0.1:8443
```

The echo upstream must be started detached (`setsid nohup ... &`). If it dies,
the server's single event-loop thread stalls inside the connect and the whole
server stops answering -- see section 6.1.
