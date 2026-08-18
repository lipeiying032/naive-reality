module naivereal/h3frontend

go 1.26

require (
	github.com/apernet/quic-go v0.61.1-0.20260806010916-184d081eef3e
	github.com/pelletier/go-toml/v2 v2.4.3
	github.com/xtls/reality v0.0.0-20260322125925-9234c772ba8f
	golang.org/x/crypto v0.54.0
	golang.org/x/exp v0.0.0-20260813180055-c1d0aacb2297
)

// The upstream xtls/reality module does not carry the REALITY-over-QUIC
// additions (ClientHelloVerifier random-field auth, dest cert chain fetch).
// We vendor the extended fork (same pseudo-version) used by
// lipeiying032/h3-reality-deploy so CI stays self-contained.
replace github.com/xtls/reality => ./third_party/xtls-reality

require (
	github.com/andybalholm/brotli v1.2.1 // indirect
	github.com/cloudflare/circl v1.6.3 // indirect
	github.com/juju/ratelimit v1.0.2 // indirect
	github.com/klauspost/compress v1.18.6 // indirect
	github.com/kr/text v0.2.0 // indirect
	github.com/pires/go-proxyproto v0.12.0 // indirect
	github.com/quic-go/qpack v0.6.0 // indirect
	github.com/refraction-networking/utls v1.8.2 // indirect
	golang.org/x/net v0.56.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.40.0 // indirect
)
