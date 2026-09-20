// The Python sample beside this folder is the canonical implementation, and its
// questions.yml is the canonical payload. go:embed cannot reach outside this
// directory, so the file here is a copy, and this test fails the moment the two
// diverge.
//
// A file ending in _test.go holds tests and stays out of the built binary. Run them
// with:
//
//	go test ./...
//
// Each test is a function starting with Test that takes *testing.T, the handle it
// calls to report a failure. t.Fatalf prints a message and stops that test.
package main

import (
	"bytes"
	"os"
	"testing"
)

// canonical is the payload the Python sample reads, one directory up.
const canonical = "../questions.yml"

// TestEmbeddedPayloadMatchesPython compares the copy compiled into the binary
// against the canonical file on disk.
//
// Without this test, the two payloads drift in silence: someone edits a weight in
// the Python sample, the Go binary keeps scoring with the old one, and the two
// tools disagree about the same document for a reason nobody can see. The test
// turns that into a build failure with the fix in the message.
func TestEmbeddedPayloadMatchesPython(t *testing.T) {
	// os.ReadFile returns the bytes and an error. A test binary runs in its own
	// package directory, so the relative path resolves the same way every time.
	want, err := os.ReadFile(canonical)
	if err != nil {
		t.Fatalf("reading %s: %v", canonical, err)
	}

	// payloadFS is the embedded filesystem from payload.go. Reading from it reads
	// bytes baked into this binary rather than anything on disk.
	got, err := payloadFS.ReadFile(payloadName)
	if err != nil {
		t.Fatalf("reading the embedded %s: %v", payloadName, err)
	}

	// Go cannot compare two byte slices with ==, so bytes.Equal does it.
	if !bytes.Equal(want, got) {
		t.Fatalf("%s and the embedded copy differ: run ./build.sh, which copies the canonical file in", canonical)
	}
}

// TestEmbeddedPayloadLoads parses the embedded payload and checks the fields the
// rest of the program depends on. A payload that parses but arrives empty would
// otherwise surface as a zero readiness or a division by zero at runtime.
func TestEmbeddedPayloadLoads(t *testing.T) {
	// An empty path means "use the embedded copy", which is what the binary does
	// when nobody passes -questions.
	set, specs, err := loadPayload("")
	if err != nil {
		t.Fatalf("loading the embedded payload: %v", err)
	}

	// %+v prints a struct with its field names, so a failure says which field came
	// back empty.
	if set.Model == "" || set.MaxStateChars == 0 || set.InputUSDPerMillion == 0 {
		t.Fatalf("settings came back empty: %+v", set)
	}

	// readinessOf divides by the total weight, so at least one question has to
	// carry weight for the score to mean anything.
	var total float64
	for _, s := range weighted(specs) {
		total += s.Q.Weight
	}
	if total <= 0 {
		t.Fatal("no question carries a weight, so readiness would divide by zero")
	}
}
