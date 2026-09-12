module naivereal/naivefp

go 1.26.0

require github.com/apernet/quic-go v0.61.1-0.20260806010916-184d081eef3e

require (
	github.com/andybalholm/brotli v1.0.6 // indirect
	github.com/klauspost/compress v1.17.4 // indirect
	github.com/quic-go/qpack v0.6.0 // indirect
	github.com/refraction-networking/utls v1.8.2 // indirect
	github.com/stretchr/testify v1.12.1 // indirect
	golang.org/x/crypto v0.54.0 // indirect
	golang.org/x/net v0.56.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.40.0 // indirect
)

// The capture hook that reports a peer's transport parameters in wire order is
// not in the upstream module. orderhook/setup.sh materialises a patched copy of
// the pinned revision here; run it before building. The path is relative so the
// tool builds anywhere.
replace github.com/apernet/quic-go => ./orderhook/quic-go
