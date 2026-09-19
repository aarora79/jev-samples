// The Python sample beside this folder is the canonical implementation, and its
// questions.yml is the canonical payload. go:embed cannot reach outside this
// directory, so the file here is a copy, and this test fails the moment the two
// diverge.
//
//	go test ./...
package main

import (
	"bytes"
	"os"
	"testing"
)

const canonical = "../questions.yml"

func TestEmbeddedPayloadMatchesPython(t *testing.T) {
	want, err := os.ReadFile(canonical)
	if err != nil {
		t.Fatalf("reading %s: %v", canonical, err)
	}

	got, err := payloadFS.ReadFile(payloadName)
	if err != nil {
		t.Fatalf("reading the embedded %s: %v", payloadName, err)
	}

	if !bytes.Equal(want, got) {
		t.Fatalf("%s and the embedded copy differ: run ./build.sh, which copies the canonical file in", canonical)
	}
}

func TestEmbeddedPayloadLoads(t *testing.T) {
	set, specs, err := loadPayload("")
	if err != nil {
		t.Fatalf("loading the embedded payload: %v", err)
	}

	if set.Model == "" || set.MaxStateChars == 0 || set.InputUSDPerMillion == 0 {
		t.Fatalf("settings came back empty: %+v", set)
	}

	var total float64
	for _, s := range weighted(specs) {
		total += s.Q.Weight
	}
	if total <= 0 {
		t.Fatal("no question carries a weight, so readiness would divide by zero")
	}
}
