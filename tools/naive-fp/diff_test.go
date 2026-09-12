package main

import "testing"

// mkReport builds a minimal report whose transport parameters are given as
// id-name/value pairs in wire order.
//
// ValueHex is what compare() reads for parameters that do not decode as a single
// varint, so the helper sets it. (A live probe sets it from the captured bytes.)
func mkReport(pairs ...[2]string) *report {
	r := &report{}
	for i, p := range pairs {
		r.TransportParameters = append(r.TransportParameters, param{
			Index:    i,
			ID:       p[0],
			ValueHex: p[1],
		})
	}
	return r
}

func TestDiffReportsFindsNoDifferenceForIdenticalShapes(t *testing.T) {
	a := mkReport(
		[2]string{"max_idle_timeout", "30000"},
		[2]string{"initial_max_data", "1048576"},
	)
	b := mkReport(
		[2]string{"max_idle_timeout", "30000"},
		[2]string{"initial_max_data", "1048576"},
	)
	res, code := diffReports(a, b)
	if code != diffSame {
		t.Errorf("code = %d, want diffSame; differences: %v", code, res.Differences)
	}
}

// The encoding order is the axis a server-library classifier scores on, so a
// difference in order alone must be reported.
func TestDiffReportsDetectsOrderOnlyDifference(t *testing.T) {
	a := mkReport([2]string{"max_idle_timeout", "1"}, [2]string{"initial_max_data", "2"})
	b := mkReport([2]string{"initial_max_data", "2"}, [2]string{"max_idle_timeout", "1"})
	res, code := diffReports(a, b)
	if code != diffDistinct {
		t.Fatalf("code = %d, want diffDistinct", code)
	}
	if res.SameOrder {
		t.Error("SameOrder = true for reports encoded in different orders")
	}
	if !res.SameSet {
		t.Error("SameSet = false for reports holding the same parameters")
	}
}

func TestDiffReportsDetectsSetDifference(t *testing.T) {
	a := mkReport([2]string{"max_idle_timeout", "1"})
	b := mkReport([2]string{"max_idle_timeout", "1"}, [2]string{"version_information", "00"})
	res, code := diffReports(a, b)
	if code != diffDistinct {
		t.Fatalf("code = %d, want diffDistinct", code)
	}
	if res.SameSet {
		t.Error("SameSet = true although B carries an extra parameter")
	}
	if len(res.OnlyInB) != 1 || res.OnlyInB[0] != "version_information" {
		t.Errorf("OnlyInB = %v, want [version_information]", res.OnlyInB)
	}
}

func TestDiffReportsDetectsValueDifference(t *testing.T) {
	a := mkReport([2]string{"initial_max_data", "20971520"})
	b := mkReport([2]string{"initial_max_data", "1048576"})
	res, code := diffReports(a, b)
	if code != diffDistinct {
		t.Fatalf("code = %d, want diffDistinct", code)
	}
	if res.SameSet || res.SameOrder {
		t.Error("set and order are identical here; only the value differs")
	}
}

// GREASE ids are random per connection, and the connection ids and reset tokens
// are random per connection by construction. None of them may be reported as a
// difference, or every comparison would be noise.
func TestDiffReportsIgnoresPerConnectionRandomness(t *testing.T) {
	a := mkReport(
		[2]string{"GREASE(0x1625)", "aa"},
		[2]string{"original_destination_connection_id", "aabb"},
		[2]string{"stateless_reset_token", "ccdd"},
		[2]string{"max_idle_timeout", "30000"},
	)
	b := mkReport(
		[2]string{"GREASE(0x79e3)", "bb"},
		[2]string{"original_destination_connection_id", "1122"},
		[2]string{"stateless_reset_token", "3344"},
		[2]string{"max_idle_timeout", "30000"},
	)
	res, code := diffReports(a, b)
	if code != diffSame {
		t.Errorf("code = %d, want diffSame; differences: %v", code, res.Differences)
	}
}

// A GREASE parameter's presence is still part of the shape even though its id is
// random: a peer that sends one is shaped differently from a peer that does not.
func TestDiffReportsTreatsGreasePresenceAsShape(t *testing.T) {
	a := mkReport([2]string{"max_idle_timeout", "1"}, [2]string{"GREASE(0x1625)", "aa"})
	b := mkReport([2]string{"max_idle_timeout", "1"})
	_, code := diffReports(a, b)
	if code != diffDistinct {
		t.Error("a GREASE parameter present on one side only was not reported")
	}
}

func TestDiffReportsFindsNoDifferenceWhenBothUseTheSameGreaseShape(t *testing.T) {
	a := mkReport([2]string{"GREASE(0x1625)", "aa"}, [2]string{"max_idle_timeout", "1"})
	b := mkReport([2]string{"GREASE(0x79e3)", "bb"}, [2]string{"max_idle_timeout", "1"})
	res, code := diffReports(a, b)
	if code != diffSame {
		t.Errorf("code = %d, want diffSame; differences: %v", code, res.Differences)
	}
}
