# REALITY client mode for naiveproxy

This patchset adds an optional REALITY client mode to the official naive client so
that its https-proxy connection uses the REALITY protocol (XTLS/REALITY) instead of
a plain TLS handshake. The server side is the existing Go REALITY server
(`frontend/`) for TCP and `h3frontend` (mode=reality) for REALITY-over-QUIC.

For TCP the wire format follows Xray's REALITY (`transport/internet/reality/reality.go`,
`UClient`). For QUIC the C-gamma stage-2 variant is used: the auth payload is sealed
into the ClientHello **random field** (session_id stays empty per RFC 9001), and the
client skips CertificateVerify verification (the server presents Dest's real chain
signed with a throwaway key). No GPL/MPL code was copied — everything here is
implemented from the protocol description in the reference implementation.

**Wire-format reference (interop-verified).** The repo's Go test client
`frontend/internal/realitytest/realitytest.go` (`Dial`) implements the exact
client wire format and passes interop with the xtls/reality server fork. The
C++ patch matches it byte-for-byte:

- session id plaintext = `version(3) | 0x00 | unix_time(4, BE) | short_id(8)`, where
  `version = {1, 0, 0}` (REALITY protocol version 1.0.0, not the TLS version);
- `AuthKey = HKDF-SHA256(X25519(client_eph_priv, server_static_pub), salt=random[0:20], info="REALITY")`;
- session id = `AES-256-GCM(Key=AuthKey, nonce=random[20:32], plaintext=16B, AAD=hello-with-zeroed-session-id)` at offset 39;
- cert check = `HMAC-SHA512(AuthKey, leaf_ed25519_pub) == cert.Signature`.

The server fork pre-dials the target per connection and, on auth success, uses the
target's ServerHello as a template (replacing only the keyshare); the client needs
no special handling for this — after a successful REALITY handshake it proceeds to
the ordinary h2 CONNECT, exactly like upstream naive.

## Files

| file | tree | purpose |
|------|------|---------|
| `001-boringssl-reality.patch` | `E:\deepseekwork\boringssl` | BoringSSL reality.cc/.h + public API + ClientHello patch + build registration |
| `002-net-reality-plumbing.patch` | `E:\deepseekwork\naivereal\src` | config parsing, global RealityConfig, SSLClientSocketImpl wiring, new net error |
| `003-spider-mode.patch` | `E:\deepseekwork\naivereal\src` | spider mode (one GET to the real target, then fail) |
| `004-net-build-registration.patch` | `E:\deepseekwork\naivereal\src` | registers net/socket/reality_config.{cc,h} in src/net/BUILD.gn |
| `010-quic-hysteria2-bbr-tuning.patch` | `E:\deepseekwork\naivereal\src` | Hysteria2-aligned QUIC windows/socket buffers/BBR profiles |
| `011-quic-reality-boringssl.patch` | boringssl tree | QUIC REALITY: random-field auth, `SSL_set1_reality_config_quic`, skip CertificateVerify |
| `012-quic-reality-net.patch` | `E:\deepseekwork\naivereal\src` | thread global RealityConfig into QUIC TLS handshake (QuicSSLConfig.reality, SNI override, ProofVerifier bypass) |
| `manifest.json` | — | patch order / dependencies / apply notes |

Apply 001 then 011 to the boringssl tree, and 002, 003, 004, 010, 012 (in order) to
the naiveproxy `src` tree. All patches are plain unified diffs with `a/`/`b/` prefixes
and verified with `git apply --check` against the exact pinned revisions.

---

## Verified hook points

### BoringSSL (patch 001)

1. **TLS 1.3 ClientHello serialization.** `ssl/handshake_client.cc`,
   `ssl_add_client_hello()` at line 219. It builds the message via
   `ssl->method->init_message` → `ssl_write_client_hello_without_extensions`
   (line 181) → `ssl_add_clienthello_tlsext` → `ssl->method->finish_message`
   (produces `Array<uint8_t> msg` including the 4-byte handshake header), then
   `ssl->method->add_message` (hashes into the transcript and writes). The patch
   inserts `ssl_reality_patch_client_hello(hs, msg)` **between** `finish_message`
   and `add_message`, so the patched hello is what is hashed and sent.
2. **Legacy session id at offset 39.** `ssl_write_client_hello_without_extensions`
   writes `client_version` (2 bytes) then `client_random` (32 bytes) then a
   `CBB_add_u8_length_prefixed` length byte then the 32-byte session id. In the
   full message that is 4 (header) + 2 + 32 + 1 = **39**; the session id occupies
   `msg[39..70]`. The patch zeroes those 32 bytes, seals over the zeroed buffer,
   then overwrites `msg[39..70]` with ciphertext||tag.
3. **Client X25519 keyshare private key.** `ssl/internal.h:869` (`SSLKeyShare`),
   `ssl/ssl_key_share.cc:140` (`X25519KeyShare`, `private_key_[32]` +
   `SerializePrivateKey(CBB*)` at line 180). The active key shares live in
   `SSL_HANDSHAKE::key_shares` (`ssl/internal.h:1840`,
   `InplaceVector<UniquePtr<SSLKeyShare>, kNumNamedGroups>`), populated by
   `ssl_setup_key_shares` (`ssl/extensions.cc:2374`). `reality_derive_auth_key`
   finds the entry with `GroupID() == SSL_GROUP_X25519` and calls
   `SerializePrivateKey`.
4. **client random / nonce / salt.** `ssl->s3->client_random` (`ssl/internal.h:2809`,
   `SSL3_STATE`). Salt = `client_random[0:20]`, nonce = `client_random[20:32]`.
5. **Crypto primitives.** `HKDF()` (`include/openssl/hkdf.h:35`),
   `X25519()` (`include/openssl/curve25519.h:51`),
   `EVP_aead_aes_256_gcm()` + `EVP_AEAD_CTX_init/seal` (`include/openssl/aead.h`),
   `HMAC_CTX_new/Init_ex/Update/Final` (`include/openssl/hmac.h`),
   `EVP_sha256/EVP_sha512` (`include/openssl/digest.h`),
   `CRYPTO_memcmp` (`include/openssl/mem.h:73`).
6. **Certificate check.** `SSL_reality_verify_certificate()` parses the leaf DER
   with `d2i_X509` (`include/openssl/x509.h:106`), `X509_get_pubkey`
   (`x509.h:167`), `EVP_PKEY_get_raw_public_key` (`include/openssl/evp.h:466`),
   `X509_get0_signature` (`x509.h:382`). Ed25519 SPKI parsing is confirmed present
   (`crypto/evp/p_ed25519.cc`).
7. **Per-SSL storage.** `struct ssl_st` (`ssl/internal.h:4214`, global scope after
   `BSSL_NAMESPACE_END` at line 4212). Reality fields were added immediately before
   `bool server : 1;` (line 4288).
8. **Build registration.** Authoritative list is `build.json` (the `"ssl"` target:
   `srcs` and `internal_hdrs`). Generated lists: `gen/sources.{gni,cmake,bzl,mk}`
   (`ssl_sources` / `ssl_internal_headers`). Chromium's
   `third_party/boringssl/BUILD.gn` imports `gen/sources.gni` and references
   `ssl_sources`; it is **not** in the naiveproxy sparse checkout, so no edit there
   is required — the new files flow in through the imported list.

### net/ (patch 002)

1. **SSL_new / injection site.** `net/socket/ssl_client_socket_impl.cc:676`
   (`ssl_.reset(SSL_new(context->ssl_ctx()))` inside `Init()`). The patch adds a
   SNI override (use `reality.server_name`) right after the existing SNI block
   (line ~691) and calls `SSL_set1_reality_config(...)` **at the end of Init()**
   (after the compliance-policy block at line ~908) so its protocol restrictions
   (TLS 1.3 only, X25519-only key shares, caller-configured ALPN, no tickets)
   override the earlier
   group/version/ALPN setup.
2. **Certificate verification callback.** Chromium already installs
   `SSL_CTX_set_custom_verify(ssl_ctx_.get(), SSL_VERIFY_PEER, VerifyCertCallback)`
   at `ssl_client_socket_impl.cc:198` (SSLContext constructor). BoringSSL allows only
   one custom-verify callback per `SSL_CTX`, so the REALITY check is performed
   **inside Chromium's** `VerifyCert()` (`ssl_client_socket_impl.cc:1084`) by calling
   the BoringSSL helper `SSL_reality_verify_certificate()`; `HandleVerifyResult()`
   (line 1201) maps a normally-verifying cert under REALITY to the distinctive
   `ERR_REALITY_REAL_TARGET` error.
3. **Distinctive error surfacing.** `HandleVerifyResult()` calls
   `OpenSSLPutNetError(FROM_HERE, ERR_REALITY_REAL_TARGET)`
   (`net/ssl/openssl_ssl_util.cc:138`), which encodes the net error as a 12-bit
   reason code in the OpenSSL error queue; `MapOpenSSLErrorWithDetails`
   (`openssl_ssl_util.cc:154`) decodes it back to the net error when
   `SSL_do_handshake` fails. The 12-bit limit (<= 4095) is satisfied by `-509`.
4. **New net error.** `net/base/net_error_list.h` — `NET_ERROR(REALITY_REAL_TARGET, -509)`
   added in the 500-599 range, right after `HTTPENGINE_PROVIDER_IN_USE` (-508).
5. **Config parsing pattern.** CLI flags are flattened into a `base::DictValue` by
   `GetSwitchesAsValue()` (`net/tools/naive/naive_command_line.cc:50`), so
   `--reality-server-name` etc. arrive as flat keys; config.json arrives as a nested
   `"reality"` object. `NaiveConfig::Parse` (`net/tools/naive/naive_config.cc:77`)
   handles both. The global `SetRealityConfig` is installed from
   `naive_proxy_bin.cc` `main()` right after `config.Parse` succeeds (line ~443).
6. **Global accessor.** `net/socket/reality_config.{h,cc}` (new). `GetRealityConfig()`
   returns nullptr while suspended (spider mode). Single-threaded, no locking
   (mirrors `g_duplicate_switch_collector`).

### net/tools/naive (patch 003)

1. **Spider trigger points.** `naive_proxy.cc` `DoPreambleComplete` (line 184),
   `HandleConnectResult` (line 245) and `HandleRunResult` (line 271). A one-shot
   guard `spider_mode_triggered_` avoids duplicate fetches.
2. **The "one normal GET".** Reuses `PreambleGetter` (`preamble_getter.cc:93`),
   whose root URL is `https://<proxy_host>:<port>/` — for a REALITY proxy that host
   is exactly `server_name`. REALITY is suspended for the duration of the fetch
   (`SetRealityConfigSuspended`) so the fetch uses ordinary TLS.

---

### QUIC REALITY (patches 011 + 012)

REALITY-over-QUIC uses the C-gamma stage-2 design (see h3-reality-deploy):
the ClientHello keeps a zero-length session_id, and the 32-byte REALITY auth
payload is sealed into the **random field** (bytes 6..38 of the handshake
message). The server's h3frontend precheck decrypts the QUIC Initial, extracts
the ClientHello, and verifies the random-field credential before letting the
flow into quic-go.

1. **BoringSSL random-field sealing (011).** `ssl_reality_patch_client_hello`
   branches on `ssl->reality_quic`: it zeroes `msg[6:38]`, computes
   `adHash = SHA256(msg)`, derives
   `AuthKey = HKDF-SHA256(shared, salt=adHash[0:20], info="REALITY")`, and seals
   the 16-byte plaintext with AES-256-GCM using nonce `adHash[20:32]`. The
   ciphertext overwrites `msg[6:38]` and `ssl->s3->client_random`, so the
   transcript hashes the patched hello.
2. **No group pinning (011).** `SSL_set1_reality_config_quic` does not call
   `SSL_set1_group_ids` / `SSL_set1_client_key_shares`, so Chromium's default
   QUIC groups and hybrid X25519MLKEM768 key share are preserved (the server's
   `extractClientKeyShare` handles both X25519 and the hybrid trailing bytes).
3. **Skip CertificateVerify (011).** `do_read_server_certificate_verify`
   skips both `ssl_verify_peer_cert` and `tls13_process_certificate_verify`
   when `reality_configured && reality_quic`.
4. **net wiring (012).** A quiche-side `RealityQuicConfig` is added to
   `QuicSSLConfig`; `QuicChromiumClientSession::GetSSLConfig()` populates it
   from the global `net::GetRealityConfig()`; `TlsConnection` applies
   `SSL_set1_reality_config_quic` to the per-connection SSL and bypasses
   `VerifyCallback`; `TlsClientHandshaker::CryptoConnect()` overrides the SNI
   with `reality.server_name`.

---

## Assumptions and caveats

- **All SSL connections are proxy TLS.** naive.exe makes only proxy TLS connections
  in normal operation, so applying `SSL_set1_reality_config` to every
  `SSLClientSocketImpl` is acceptable. The spider-mode fetch is the one exception
  and is handled by suspending the global config for its duration.
- **No ECH, no DTLS, no session resumption (TCP REALITY).** TCP REALITY forces TLS 1.3 +
  X25519 + no tickets (ALPN remains caller-configured), so resumption/ECH/DTLS
  paths are not exercised. The QUIC path (011/012) keeps Chromium's default groups,
  disables tickets, and skips CertificateVerify — h3frontend mode=reality is the
  matching server.
- **SNI == server_name == proxy host.** The REALITY cert check's "real target" branch
  verifies the certificate against the normal SNI (`host_and_port_.host()`). In the
  intended setup the proxy host equals the REALITY `server_name`, so they coincide.
- **Verification is done in Chromium, not in a BoringSSL custom-verify callback.**
  The task suggested `SSL_CTX_set_custom_verify`; BoringSSL allows only one such
  callback per context and Chromium already uses it, so the HMAC/Ed25519 check is
  exposed as `SSL_reality_verify_certificate()` and invoked from `VerifyCert()`.

## Uncertainties (verify before shipping)

1. ~~**The three "client version" bytes.**~~ **RESOLVED.** The bytes are the
   REALITY **protocol** version `{1, 0, 0}` (= 1.0.0), **not** the TLS version.
   Confirmed against the interop-verified Go reference client
   `frontend/internal/realitytest/realitytest.go` (line 91:
   `SessionId[0],[1],[2] = 1,0,0`). The patch hardcodes `{1, 0, 0}`.
2. **Unverifiable base/ APIs.** `base/base64url.h` and
   `base/strings/string_number_conversions.h` are not in the sparse checkout, so
   `naive_config.cc` uses small self-contained base64url/hex decoders instead.
3. **net/cert not in the sparse checkout.** The leaf DER is obtained from
   `SSL_get0_peer_certificates` + `CRYPTO_BUFFER_*` (already used in that file)
   rather than `X509Certificate::cert_buffer()`.
4. **Build registration: use 004.** Upstream naiveproxy has NO `net/socket/BUILD.gn`;
   all net sources are listed flat in `src/net/BUILD.gn` (around line 1015). Patch
   `004-net-build-registration.patch` adds `socket/reality_config.{cc,h}` there.
   (`net/tools/naive` already depends on `//net`, so the header is reachable.)
5. **boringssl checkout has no .git.** The source was fetched as a codeload
   tarball of the pinned revision `3a9254f16eda7a4c5d2260039ff23456a0a34de4` and
   extracted directly into `E:\deepseekwork\boringssl` (no `.git`), so `git log`
   is unavailable; the revision is nevertheless the exact pinned commit, and the
   patches were verified with `git apply --check` against it.

## Test plan (when CI builds are available)

1. **Build.** Apply 001 to boringssl, then 002 + 003 + 004 to src (004 registers
   the new files in `net/BUILD.gn`). Regenerate build lists if CI does.
2. **Happy path.** Configure a REALITY server (Go XTLS/REALITY). Run
   `naive --proxy=https://<server_name>:443 --reality-public-key=<b64url>
   --reality-short-id=<hex> --reality-server-name=<server_name>`. A normal
   CONNECT should succeed with TLS 1.3. The REALITY frontend dispatches by client
   preface: h2 CONNECT remains supported, and HTTP/1.1 CONNECT is accepted when
   ALPN is not signalled. Confirm (keylog / capture) that
   the ClientHello legacy session id at offset 39 is the 32-byte AEAD blob.
3. **Cert check.** Confirm the REALITY server's temporary Ed25519 cert is accepted
   without normal chain verification (no trust-anchor fetch, handshake completes).
4. **Real-target / spider.** Point the same config at the real target site (or MITM
   the SNI to the real site). Expect the connection to fail with
   `ERR_REALITY_REAL_TARGET` (-509) and the log line
   `REALITY: reached real target site, spider mode`, followed by one GET to
   `https://<server_name>/` and connection failure.
5. **Config validation.** Exercise bad `public_key` (wrong length / bad base64url)
   and bad `short_id` (>16 hex, non-hex) and confirm clean config errors.
6. **Negative.** Without `--reality-*` flags, behavior must be byte-identical to
   upstream (no session-id patch, normal verification).
7. **QUIC REALITY happy path.** Run `tests/quic-reality-e2e.sh` (CI job
   `quic-reality-e2e`): patched `naive --proxy=quic://...` + `--reality-*` flags →
   h3frontend (mode=reality, h3_cert/h3_key) → official naive server. Confirm the
   ClientHello random field is the 32-byte AEAD blob and the handshake completes
   without CertificateVerify verification.
