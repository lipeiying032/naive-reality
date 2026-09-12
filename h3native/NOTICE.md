# h3native notices

`h3native/` is built against, links, and derives from third-party code that is
not vendored in this repository. This file records what is used and under which
terms.

## QUICHE

- Source: https://quiche.googlesource.com/quiche
- Revision: read at build time from upstream naiveproxy's `DEPS` (see
  `build.sh`); it is not pinned here so that it cannot drift from the kernel
  build.
- Licence: BSD-3-Clause, Copyright Google LLC.
- Use: `naive_server_backend.{h,cc}` implements QUICHE's
  `QuicSimpleServerBackend` interface and `naive_h3_server_bin.cc` drives
  QUICHE's `QuicToyServer`. The HTTP/3 stack, transport, flow control, loss
  recovery and congestion control all come from QUICHE.
- Modification: `patches/` changes three QUICHE files. Two are build
  configuration (a visibility attribute and a dependency's compiler
  compatibility), one removes a `constexpr` for pre-C++23 libstdc++. **No QUICHE
  protocol or transport source is modified.**

## BoringSSL

- Pulled by Bazel from the Bazel Central Registry as `boringssl`.
- Licence: OpenSSL Licence and the ISC Licence; see the BoringSSL `LICENSE` file
  in the fetched tree.
- Use: TLS for the QUIC handshake, linked statically into the binary.

## Abseil, protobuf, re2, zlib, gurl

- Pulled by Bazel from the Bazel Central Registry or from GitHub
  (`build.sh`'s `MODULE.bazel` patch records the gurl compatibility fix).
- Licences: Apache-2.0 (Abseil, protobuf), BSD-3-Clause (re2, gurl), zlib
  Licence (zlib).
- gurl additionally links the system ICU (`libicu-dev`), which is why the
  workflow installs it.

## This repository's own code

`naive_server_backend.{h,cc}`, `naive_h3_server_bin.cc`,
`naive_posix_client_socket.{h,cc}` and `build.sh` are original to this project and
are covered by the repository's `LICENSE-Go`/`LICENSE` terms as applicable.
