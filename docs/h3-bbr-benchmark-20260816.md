# H3 BBR vs Hysteria2 BBR benchmark (2026-08-16, first VPS run)

Status: initial feasibility run. Single VPS, uncontrolled shared path; numbers are
indicative, not a final acceptance test.

## Environment

- VPS: 198.46.146.78 (Ubuntu 24.04, 1 vCPU, ~1GB RAM)
- Server side:
  - official naive server: 127.0.0.1:18080
  - bench HTTP server: 127.0.0.1:18081 (download streams zeros, upload discards)
  - h3frontend: UDP 8443, BBR standard, 8MiB/20MiB QUIC windows, GSO on
  - Hysteria2 v2.12.1: UDP 8444, BBR standard, same QUIC windows, `ignoreClientBandwidth: true`
- Client side: same local WSL host
  - naiveproxy client: patched x64 kernel, `quic://...:8443`
  - Hysteria2 client: SOCKS5 at 127.0.0.1:11081
- Transfer: HTTP over SOCKS5 to `http://127.0.0.1:18081`
- BBR profiles: both sides set to `standard` for the download comparison; upload
  comparison also tested `aggressive` because it matches Hysteria2's tunable profile set.

## Download (server -> client), 512 MiB

| run | naiveproxy H3 (standard) | Hysteria2 BBR (standard) |
|---|---|---|
| 1 | 15.73 MiB/s (34.12s) | 14.70 MiB/s (36.52s) |

## Upload (client -> server)

512 MiB first pass:

| run | naiveproxy H3 | Hysteria2 BBR |
|---|---|---|
| standard | 2.77 MiB/s after retry; first attempt reset at 164MiB | 7.98 MiB/s |
| aggressive | 8.20 MiB/s | 7.74 MiB/s |

256 MiB repeat runs:

| run | naiveproxy H3 standard | naiveproxy H3 aggressive | Hysteria2 aggressive |
|---|---|---|---|
| 1 | 7.44 MiB/s | 7.42 MiB/s | 8.25 MiB/s |
| 2 | - | 7.61 MiB/s | 8.19 MiB/s |
| 3 | - | 8.17 MiB/s | 7.97 MiB/s |

## Initial conclusion

- H3 CONNECT chain works end-to-end with the new Go `h3frontend` and the patched
  naiveproxy kernel.
- Download: H3 standard BBR is at or slightly above HY2 standard BBR.
- Upload: repeated 256MiB runs put H3 and HY2 within roughly 90-97% of each other.
  The first 512MiB standard H3 run was an outlier (connection reset / slow retry)
  and needs more controlled repetition.
- `--quic-bbr-profile=aggressive` is available and in the repeat runs brings H3
  upload to HY2 levels.

## Next steps before final acceptance

- Use a larger VPS and quiescent path.
- Run at least 5 alternating 1GiB/2GiB runs per direction and take median.
- Capture qlog/NetLog and packet captures to compare CWND, pacing rate, RTT, loss.
- Decide whether H3 default should remain `standard` or use `aggressive` to match
  a typical HY2 deployment.

---

## Profile comparison (audio.230406.xyz, 256MiB, 2026-08-16)

Domain certificate was issued on the VPS for `audio.230406.xyz`; the CDN domain
was not touched. Server side remained `h3frontend` + official naive server, and
client side used the patched naive kernel. All services were removed after the
run.

### Download 256MiB by server BBR profile

| profile | run1 MiB/s | run2 MiB/s | median MiB/s | notes |
|---|---:|---:|---:|---|
| standard | 16.55 | 20.04 | 18.29 | fastest |
| aggressive | 16.71 | 15.84 | 16.28 | stable, slightly behind standard |
| conservative | ~11.2 | 16.89 | ~14.0 | slow first run, unstable |

### Upload 256MiB by client BBR profile

| profile | runs MiB/s | median MiB/s |
|---|---:|---:|
| standard | 7.48, 7.11, 7.15, 7.40, 6.86 | 7.15 |
| aggressive | 7.12, 6.13, 7.68, 7.02, 6.68 | 7.02 |
| conservative | 7.65, 7.85, 7.31 | 7.65 |

### Conclusion for this VPS/path

- Download direction: `standard` is the fastest and most consistent profile.
- Upload direction: `conservative` has a small edge, but the spread is close to
  the noise level of the shared 1-vCPU VPS.
- `aggressive` is not the fastest profile on this path.
- Recommended default remains `standard`; use `conservative` only if upload is
  the dominant workload and verify with a longer repeated benchmark.
