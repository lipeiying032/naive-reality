module naivereal/naivefp

go 1.26

require github.com/apernet/quic-go v0.61.1-0.20260806010916-184d081eef3e

require (
	github.com/andybalholm/brotli v1.2.1 // indirect
	github.com/klauspost/compress v1.18.6 // indirect
	github.com/quic-go/qpack v0.6.0 // indirect
	github.com/refraction-networking/utls v1.8.2 // indirect
	golang.org/x/crypto v0.54.0 // indirect
	golang.org/x/net v0.56.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.40.0 // indirect
)

// The capture hook that reports a peer's transport parameters in wire order is a
// local addition to the pinned fork; see tools/naive-fp/README.md.
replace github.com/apernet/quic-go => /root/native-h3/fork/quic-go
