// Copyright 2021 Tamás Gulácsi
//
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"testing"
)

func TestCamelCase(t *testing.T) {
	for _, s := range []struct {
		In, Want string
	}{
		{"db_pgw_ws", "DbPgwWs"},
		{"db", "Db"},
	} {
		got := CamelCase(s.In)
		t.Logf("%q -> %q", s.In, got)
		if got != s.Want {
			t.Errorf("%q: got %q, wanted %q", s.In, got, s.Want)
		}
		if de := unCamelCase(got); de != s.In {
			t.Errorf("%q: got %q, wanted %q", got, de, s.In)
		}
	}
}
