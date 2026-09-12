package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// diffResult is the machine-checkable outcome of comparing two probe reports.
// A non-empty Differences list means the two endpoints are distinguishable on at
// least one wire-observable property.
type diffResult struct {
	A            string   `json:"a"`
	B            string   `json:"b"`
	SameOrder    bool     `json:"same_tp_order"`
	SameSet      bool     `json:"same_tp_set"`
	Differences  []string `json:"differences"`
	OnlyInA      []string `json:"only_in_a,omitempty"`
	OnlyInB      []string `json:"only_in_b,omitempty"`
}

// diffExitCode is returned to the shell: 0 when the two endpoints look the same,
// 1 when they are distinguishable, 2 on usage or I/O failure.
const (
	diffSame     = 0
	diffDistinct = 1
	diffUsage    = 2
)

func runDiff(args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("diff takes exactly two report files")
	}
	a, err := loadReport(args[0])
	if err != nil {
		return err
	}
	b, err := loadReport(args[1])
	if err != nil {
		return err
	}
	res, code := diffReports(a, b)

	if err := writeJSON(os.Stdout, res); err != nil {
		return err
	}
	switch code {
	case diffSame:
		fmt.Fprintln(os.Stderr, "naive-fp: no observable differences")
	default:
		fmt.Fprintf(os.Stderr, "naive-fp: %d observable difference(s)\n", len(res.Differences))
		for _, d := range res.Differences {
			fmt.Fprintln(os.Stderr, "  -", d)
		}
	}
	// Non-zero exit makes this usable as a CI gate. Returned rather than exited
	// so main owns the process exit and this stays testable.
	if code != diffSame {
		return exitError{code: code}
	}
	return nil
}

// exitError carries a specific process exit code up to main.
type exitError struct{ code int }

func (e exitError) Error() string { return fmt.Sprintf("exit %d", e.code) }

// diffReports is the pure form of the diff command: it compares two reports and
// reports whether they are distinguishable.
func diffReports(a, b *report) (diffResult, int) {
	res := compare(a, b)
	if len(res.Differences) == 0 {
		return res, diffSame
	}
	return res, diffDistinct
}

func loadReport(path string) (*report, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var r report
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &r, nil
}

func compare(a, b *report) diffResult {
	res := diffResult{A: a.URL, B: b.URL}

	// Order and set are compared after removing parameters whose presence or
	// placement carries no signal: GREASE (random id by definition) and
	// per-connection random values. Everything else is shape.
	//
	// A value-only difference counts as distinguishable too, so that
	// SameOrder/SameSet never both read true for endpoints a caller must treat as
	// different. Reporting it only in Differences would let a CI check that looks
	// at the booleans pass on a real mismatch.
	res.SameOrder = order(shapeParams(a)) == order(shapeParams(b))
	res.SameSet = setOf(shapeParams(a)) == setOf(shapeParams(b))

	if !res.SameOrder {
		res.Differences = append(res.Differences,
			fmt.Sprintf("transport-parameter encoding order differs:\n      A: %s\n      B: %s", order(shapeParams(a)), order(shapeParams(b))))
	}
	if !res.SameSet {
		res.OnlyInA, res.OnlyInB = setDifference(shapeParams(a), shapeParams(b))
		res.Differences = append(res.Differences,
			fmt.Sprintf("transport-parameter set differs (only in A: %s; only in B: %s)",
				strings.Join(res.OnlyInA, ","), strings.Join(res.OnlyInB, ",")))
	}

	// Per-parameter values, compared by name so ordering differences do not
	// mask a value difference.
	av, bv := valuesByName(a), valuesByName(b)
	valueDiffers := false
	for name, va := range av {
		vb, ok := bv[name]
		if !ok {
			continue // already reported as a set difference
		}
		if perConnectionRandom[name] {
			// Differs on every connection by construction; carrying no signal.
			continue
		}
		if va != vb {
			valueDiffers = true
			res.Differences = append(res.Differences,
				fmt.Sprintf("%s differs: A=%s B=%s", name, va, vb))
		}
	}
	if valueDiffers {
		// Shape is not identical if any shape-bearing value differs.
		res.SameOrder = false
		res.SameSet = false
	}

	for _, kv := range []struct {
		label string
		av    string
		bv    string
	}{
		{"QUIC version", a.Handshake.QUICVersion, b.Handshake.QUICVersion},
		{"ALPN", a.Handshake.ALPN, b.Handshake.ALPN},
		{"TLS version", a.Handshake.TLSVersion, b.Handshake.TLSVersion},
		{"HTTP proto", a.HTTP.Proto, b.HTTP.Proto},
		{"HTTP status", fmt.Sprint(a.HTTP.Status), fmt.Sprint(b.HTTP.Status)},
	} {
		if kv.av != kv.bv {
			res.Differences = append(res.Differences, fmt.Sprintf("%s differs: A=%s B=%s", kv.label, kv.av, kv.bv))
		}
	}
	return res
}

// perConnectionRandom lists transport parameters whose values are fresh random
// bytes on every connection, so a value mismatch between two probes carries no
// information about the implementation.
var perConnectionRandom = map[string]bool{
	"original_destination_connection_id": true,
	"stateless_reset_token":              true,
	"initial_source_connection_id":       true,
	"retry_source_connection_id":         true,
}

// isGreaseName reports whether a parameter is a reserved GREASE codepoint
// (RFC 9000 section 18.1 reserves ids of the form 31*N+27). Its id is random by
// definition, so neither its presence nor its position is a stable signal.
func isGreaseName(name string) bool {
	return strings.HasPrefix(name, "GREASE(")
}

// greaseLabel replaces a GREASE parameter's random id.
//
// A GREASE id is drawn fresh per connection, so the id itself cannot be compared.
// Its *presence and position* can, and they are shape: one implementation may
// always send a GREASE parameter and another may never send one. Collapsing the
// ids to a single label keeps that signal while removing the per-connection
// randomness.
const greaseLabel = "GREASE"

// shapeParams returns the parameters that describe an implementation's shape,
// with GREASE ids normalised to a single label.
func shapeParams(r *report) []param {
	out := make([]param, 0, len(r.TransportParameters))
	for _, p := range r.TransportParameters {
		if isGreaseName(p.ID) {
			p.ID = greaseLabel
		}
		out = append(out, p)
	}
	return out
}

func order(params []param) string {
	ids := make([]string, 0, len(params))
	for _, p := range params {
		ids = append(ids, p.ID)
	}
	return strings.Join(ids, ",")
}

func setOf(params []param) string {
	seen := map[string]bool{}
	var ids []string
	for _, p := range params {
		if !seen[p.ID] {
			seen[p.ID] = true
			ids = append(ids, p.ID)
		}
	}
	// Stable, order-insensitive.
	for i := 0; i < len(ids); i++ {
		for j := i + 1; j < len(ids); j++ {
			if ids[j] < ids[i] {
				ids[i], ids[j] = ids[j], ids[i]
			}
		}
	}
	return strings.Join(ids, ",")
}

func setDifference(a, b []param) (onlyA, onlyB []string) {
	inA, inB := map[string]bool{}, map[string]bool{}
	for _, p := range a {
		inA[p.ID] = true
	}
	for _, p := range b {
		inB[p.ID] = true
	}
	for id := range inA {
		if !inB[id] {
			onlyA = append(onlyA, id)
		}
	}
	for id := range inB {
		if !inA[id] {
			onlyB = append(onlyB, id)
		}
	}
	return onlyA, onlyB
}

func valuesByName(r *report) map[string]string {
	out := make(map[string]string, len(r.TransportParameters))
	for _, p := range r.TransportParameters {
		if p.Decoded != nil {
			out[p.ID] = fmt.Sprintf("%d", *p.Decoded)
		} else {
			out[p.ID] = p.ValueHex
		}
	}
	return out
}
