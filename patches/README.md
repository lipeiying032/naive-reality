# Kernel profiles

The default `native-h3` profile uses the upstream Chromium network stack and
standard TLS. The server is `h3frontend` in `mode="origin"`, with an operator-owned
certificate and website. See [H3 architecture and migration](../docs/h3-origin.md).

| Profile | Patches | Protocol |
|---|---|---|
| `native-h3` (default) | 005, 007 | Standard TLS; configuration rejection and initialization fix |
| `tcp-reality` (opt-in) | 001, 002, 003, 004, 006 | Existing TCP REALITY; QUIC REALITY rejected |

Use a clean checkout at the exact commit pinned by `manifest.json` and CI:

```sh
python3 scripts/apply-kernel-patches.py /path/to/upstream --profile native-h3
```

The script verifies the upstream commit, CHROMIUM_VERSION, clean worktree and
patch application. For `native-h3`, it additionally requires the complete changed
file set to be exactly `src/net/tools/naive/{naive_config,naive_proxy_bin}.cc`. No BoringSSL, QUICHE,
QUIC transport, congestion or HTTP/3 source is patched in the default build.

005 rejects legacy REALITY and custom QUIC options before any sockets open. It
prevents an old config from being silently interpreted as ordinary TLS. It does
not provide TCP REALITY; use the explicitly named TCP profile for that protocol.

007 sets the existing QUIC proxy options before `URLRequestContextBuilder::Build`
constructs `QuicSessionPool` and copies `QuicParams`. Setting them afterwards
leaves the pool's proxy hostname allowlist empty: Chromium then rejects even a
locally trusted test CA with `ERR_QUIC_CERT_ROOT_NOT_KNOWN`. The fix uses
Chromium's existing policy for the configured proxy hosts; certificate chain
and hostname verification still apply. See [failure analysis](../docs/native-h3-e2e.md).

The optional TCP profile retains the existing BoringSSL session-ID authentication,
HMAC certificate verification and spider behavior (001–004). 006 rejects QUIC
proxy chains combined with REALITY and rejects the retired custom QUIC options.
It does not reintroduce QUIC-specific BoringSSL or QUICHE hooks.

010 (Hysteria2 client tuning), 011 (QUIC random-field authentication and custom
CertificateVerify proof) and 012 (QUIC REALITY wiring / verifier bypass) have been
removed. Historical implementations are available at commit `2b12eb6`.
The old C-gamma proof must not be described as a secure standard CertificateVerify.

Patches remain plain unified diffs. `manifest.json` is the source of truth for
ordering and target directories. Patch application checks are static validation;
compilation and interoperability require building the pinned upstream tree via
its `src/get-clang.sh` and `src/build.sh` as in the kernel workflow.
