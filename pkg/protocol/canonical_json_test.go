package protocol

import (
	"crypto/sha256"
	"fmt"
	"testing"
)

func TestMarshalCanonical_DeterministicKeyOrdering(t *testing.T) {
	// 构造具有多层嵌套且 key 乱序的 map
	map1 := map[string]any{
		"zebra": "z",
		"apple": "a",
		"nested": map[string]any{
			"beta":  2,
			"alpha": 1,
			"gamma": 3,
		},
		"banana": "b",
	}

	map2 := map[string]any{
		"apple": "a",
		"banana": "b",
		"nested": map[string]any{
			"gamma": 3,
			"alpha": 1,
			"beta":  2,
		},
		"zebra": "z",
	}

	b1, err1 := MarshalCanonical(map1)
	if err1 != nil {
		t.Fatalf("MarshalCanonical(map1) failed: %v", err1)
	}

	b2, err2 := MarshalCanonical(map2)
	if err2 != nil {
		t.Fatalf("MarshalCanonical(map2) failed: %v", err2)
	}

	h1 := fmt.Sprintf("%x", sha256.Sum256(b1))
	h2 := fmt.Sprintf("%x", sha256.Sum256(b2))

	if string(b1) != string(b2) {
		t.Fatalf("Output mismatch:\nB1: %s\nB2: %s", string(b1), string(b2))
	}

	if h1 != h2 {
		t.Fatalf("SHA-256 hash mismatch:\nH1: %s\nH2: %s", h1, h2)
	}

	expectedPrefix := `{"apple":"a","banana":"b","nested":{"alpha":1,"beta":2,"gamma":3},"zebra":"z"}`
	if string(b1) != expectedPrefix {
		t.Fatalf("Expected %s, got %s", expectedPrefix, string(b1))
	}
}

func TestMarshalCanonical_CRLFNormalization(t *testing.T) {
	inputWithCRLF := map[string]any{
		"prompt": "Line 1\r\nLine 2\r\nLine 3",
		"nested": []any{
			"Item 1\r\nSub",
		},
	}

	inputWithLF := map[string]any{
		"prompt": "Line 1\nLine 2\nLine 3",
		"nested": []any{
			"Item 1\nSub",
		},
	}

	b1, err := MarshalCanonical(inputWithCRLF)
	if err != nil {
		t.Fatalf("MarshalCanonical failed: %v", err)
	}

	b2, err := MarshalCanonical(inputWithLF)
	if err != nil {
		t.Fatalf("MarshalCanonical failed: %v", err)
	}

	if string(b1) != string(b2) {
		t.Fatalf("CRLF not normalized to LF:\nCRLF: %s\nLF:   %s", string(b1), string(b2))
	}
}

func TestMarshalCanonical_PreservesSpecialCharacters(t *testing.T) {
	input := map[string]any{
		"rule": "Use <tag> and & and > without HTML escaping",
	}

	b, err := MarshalCanonical(input)
	if err != nil {
		t.Fatalf("MarshalCanonical failed: %v", err)
	}

	expected := `{"rule":"Use <tag> and & and > without HTML escaping"}`
	if string(b) != expected {
		t.Fatalf("HTML characters were escaped:\nExpected: %s\nGot:      %s", expected, string(b))
	}
}
