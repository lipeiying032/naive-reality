// Command naive-fp probes an HTTP/3 endpoint and reports the wire-observable
// properties that distinguish one QUIC server implementation from another.
//
// It exists because stock QUIC clients hand you a parsed transport-parameter
// struct and throw away the encoding order, which is the axis that server-library
// classifiers score on. See README.md.
package main

import (
	"flag"
	"fmt"
	"os"
)

const usage = `naive-fp - HTTP/3 wire-fingerprint probe

usage:
  naive-fp probe [flags] <url>     probe one endpoint, write JSON to stdout
  naive-fp diff  <a.json> <b.json> compare two probe reports

run "naive-fp probe -h" for probe flags.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "probe":
		err = runProbe(os.Args[2:])
	case "diff":
		err = runDiff(os.Args[2:])
	case "-h", "--help", "help":
		fmt.Print(usage)
		return
	default:
		fmt.Fprintf(os.Stderr, "naive-fp: unknown command %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "naive-fp:", err)
		os.Exit(1)
	}
}

func runProbe(args []string) error {
	fs := flag.NewFlagSet("probe", flag.ExitOnError)
	authority := fs.String("authority", "", "value for the :authority pseudo-header (defaults to the URL host)")
	insecure := fs.Bool("insecure", false, "skip certificate verification (probe only; never use against a real origin)")
	path := fs.String("path", "/", "path to request over HTTP/3")
	proto := fs.String("proto", "h3", "ALPN to offer")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("probe takes exactly one URL argument")
	}
	rep, err := probe(probeOptions{
		url:       fs.Arg(0),
		authority: *authority,
		insecure:  *insecure,
		path:      *path,
		alpn:      *proto,
	})
	if err != nil {
		// A report may still be partially populated; emit it so the failure is
		// diagnosable, then report the error on stderr.
		if rep != nil {
			_ = writeJSON(os.Stdout, rep)
		}
		return err
	}
	return writeJSON(os.Stdout, rep)
}
