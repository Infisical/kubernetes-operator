package crypto

import "testing"

func TestComputeRenderedEtag_StableAcrossMapOrder(t *testing.T) {
	a := map[string][]byte{
		"alpha": []byte("1"),
		"beta":  []byte("2"),
		"gamma": []byte("3"),
	}
	b := map[string][]byte{
		"gamma": []byte("3"),
		"alpha": []byte("1"),
		"beta":  []byte("2"),
	}
	for i := 0; i < 50; i++ {
		if ComputeRenderedEtag(a) != ComputeRenderedEtag(b) {
			t.Fatalf("ComputeRenderedEtag must be insensitive to map iteration order")
		}
	}
}

func TestComputeRenderedEtag_DistinguishesContent(t *testing.T) {
	base := map[string][]byte{"k": []byte("v")}
	cases := []map[string][]byte{
		{"k": []byte("v2")},                  // value differs
		{"K": []byte("v")},                   // key differs
		{"k": []byte("v"), "k2": []byte("")}, // extra empty entry
	}
	baseEtag := ComputeRenderedEtag(base)
	for i, c := range cases {
		if ComputeRenderedEtag(c) == baseEtag {
			t.Fatalf("case %d collided with base", i)
		}
	}
}

func TestComputeRenderedEtag_KeyValueBoundary(t *testing.T) {
	// Without a delimiter, "ab"+"c" and "a"+"bc" would hash the same.
	if ComputeRenderedEtag(map[string][]byte{"ab": []byte("c")}) ==
		ComputeRenderedEtag(map[string][]byte{"a": []byte("bc")}) {
		t.Fatal("key/value boundary collision")
	}
}
