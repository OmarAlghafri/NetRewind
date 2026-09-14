module github.com/OmarAlghafri/netrewind

go 1.25.0

toolchain go1.26.6

require (
	github.com/cilium/ebpf v0.22.0
	github.com/oklog/ulid/v2 v2.1.2
	github.com/spf13/cobra v1.10.2
	github.com/vishvananda/netlink v1.3.1
	golang.org/x/net v0.58.0
	golang.org/x/sys v0.47.0
	gopkg.in/yaml.v3 v3.0.1
	modernc.org/sqlite v1.57.0
)

require (
	github.com/Microsoft/go-winio v0.6.2 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/spf13/pflag v1.0.10 // indirect
	github.com/vishvananda/netns v0.0.5 // indirect
	modernc.org/libc v1.74.4 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.11.0 // indirect
)

// The toolchain line, rather than a raised go line, is deliberate.
//
// go1.26.6 is the first release carrying fixes for an escaper bypass in
// html/template - which this project renders network-supplied strings through -
// and for the header timeout on unencrypted HTTP/2. Builds and releases must
// use it, and the toolchain directive is what makes that happen.
//
// Raising the go directive instead would have the same effect on a normal
// machine and break every distro that sets GOTOOLCHAIN=local. Alpine does,
// because upstream Go toolchains are linked against glibc and will not run on
// musl, so its packaged Go cannot fetch a newer one. The go line therefore
// stays at the language version actually required.
