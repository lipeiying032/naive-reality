# Third-party notices

naivereal consists of:
- Go code under frontend/ and tui/ (MIT, see LICENSE-Go),
- a patchset under patches/ for the naiveproxy kernel (BSD-3-Clause, following
  the upstream klzgrad/naiveproxy license; patches contain no copied GPL/MPL code),
- the upstream naiveproxy source tree under src/ (BSD-3-Clause, see LICENSE).

## Go module dependencies

| module | license |
|---|---|
| github.com/xtls/reality | MPL-2.0 (fork of Go stdlib crypto/tls) |
| github.com/sagernet/utls | BSD-3-Clause |
| golang.org/x/net, x/sys, x/crypto | BSD-3-Clause |
| github.com/pelletier/go-toml/v2 | MIT |
| github.com/charmbracelet/bubbletea, bubbles, lipgloss | MIT |
| github.com/atotto/clipboard | BSD-3-Clause |
| golang.zx2c4.com/wireguard (tun/wintun) | MIT; wintun driver: see WireGuard wintun license,
|   include the wintun license text when redistributing wintun.dll |
| gvisor.dev/gvisor | Apache-2.0 |

## Interop references (not linked)

- Xray-core / XTLS REALITY: wire format compatibility verified by interop tests;
  no code copied.
- naivereal-tui bundles no third-party core; the user supplies naive.exe
  (official build or our patched kernel).