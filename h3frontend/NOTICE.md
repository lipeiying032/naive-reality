# Third-party notices for h3frontend

This component links against and contains code derived from:

## h3-reality-deploy (REALITY-over-QUIC precheck / relay)

Source: https://github.com/lipeiying032/h3-reality-deploy (MIT; Xray-core MIT)
Files adapted into this directory:
- `reality_precheck.go` — QUIC Initial decryption / ClientHello extraction
- `reality_precheck_conn.go` — PENDING/AUTH/RELAY precheck state machine
- `reality_relay.go` — 5-tuple UDP NAT relay
- `third_party/xtls-reality/` — vendored extended `github.com/xtls/reality`
  fork (same pseudo-version as upstream plus QUIC random-field auth,
  ClientHelloVerifier and dest-cert-chain helpers)

## apernet/quic-go

https://github.com/apernet/quic-go
Commit: 184d081eef3e9edd5cb7c0ddf2460c91f2e6adb1
License: MIT

Copyright (c) 2016 the quic-go authors & Google, Inc.

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.

## Hysteria2 BBR / pacer code

Source: https://github.com/apernet/hysteria
Files copied/adapted from `core/internal/congestion/bbr` and
`core/internal/congestion/common/pacer.go`.
License: MIT

Copyright 2023 Toby

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
