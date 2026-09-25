package main

import (
	"math"
	"testing"
)

func TestParseModelWindowTokenRejectsOverflow(t *testing.T) {
	for _, token := range []string{"18446744073709552K", "18446744073710M", "18446744073709551616"} {
		if got, ok := parseModelWindowToken(token); ok {
			t.Fatalf("parseModelWindowToken(%q) = %d, true; want rejected overflow", token, got)
		}
	}
	if got, ok := parseModelWindowToken("18446744073709K"); !ok || got != 18446744073709000 {
		t.Fatalf("near-limit K token = %d, %v", got, ok)
	}
	if got, ok := parseModelWindowToken("18446744073709551615"); !ok || got != math.MaxUint64 {
		t.Fatalf("max uint token = %d, %v", got, ok)
	}
}
