// The Python sample beside this folder is the canonical implementation, and its
// questions.yml is the canonical payload. go:embed cannot reach outside this
// directory, so the file here is a copy, and this test fails the moment the two
// diverge.
//
// A file ending in _test.go holds tests and stays out of the built binary. Run
// them with:
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
// Without this test the two payloads drift in silence: someone edits a weight in
// the Python sample, the Go binary keeps triaging with the old one, and the two
// tools disagree about the same pull request for a reason nobody can see. The
// test turns that into a build failure with the fix in the message.
func TestEmbeddedPayloadMatchesPython(t *testing.T) {
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
// otherwise surface as a zero load or a division by zero at runtime.
func TestEmbeddedPayloadLoads(t *testing.T) {
	// An empty path means "use the embedded copy", which is what the binary does
	// when nobody passes -questions.
	set, specs, err := loadPayload("")
	if err != nil {
		t.Fatalf("loading the embedded payload: %v", err)
	}

	// %+v prints a struct with its field names, so a failure says which field came
	// back empty. Every budget matters: a zero one truncates that part of the
	// state to nothing and the load then reads a pull request that is not there.
	switch {
	case set.Model == "":
		t.Fatalf("no model pinned: %+v", set)
	case set.MaxDescriptionChars == 0 || set.MaxFileListChars == 0 || set.MaxDiffChars == 0:
		t.Fatalf("a state budget came back zero: %+v", set)
	case set.InputUSDPerMillion == 0:
		t.Fatalf("no price, so every run would report as free: %+v", set)
	}

	// The load divides by the total weight, so at least one question has to carry
	// weight for the number to mean anything.
	if weightedTotal(specs) <= 0 {
		t.Fatal("no question carries a weight, so the load would divide by zero")
	}
}

// TestInvertedQuestionsCarryWeight guards the three questions whose meaning is
// flipped. An inverted question with no weight is a silent no-op: it gets asked,
// costs tokens, and changes nothing, which is the kind of mistake that survives
// review because the output still looks right.
func TestInvertedQuestionsCarryWeight(t *testing.T) {
	_, specs, err := loadPayload("")
	if err != nil {
		t.Fatalf("loading the embedded payload: %v", err)
	}

	inverted := 0
	for _, s := range specs {
		if !s.Q.Invert {
			continue
		}
		inverted++
		if s.Q.Weight <= 0 {
			t.Errorf("question %s inverts but carries no weight, so it cannot lower a load", s.ID)
		}
	}
	// mechanical, has_tests and description_quality are the three. A payload that
	// loses one of them would still load and still triage, with the load quietly
	// reading higher on every tested and repeated change.
	if inverted != 3 {
		t.Errorf("expected 3 inverted questions, found %d", inverted)
	}
}
