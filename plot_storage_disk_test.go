package plotthread

import (
	"encoding/hex"
	"testing"

	"golang.org/x/crypto/ed25519"
)

func TestEncodePlotHeader(t *testing.T) {
	pubKey, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}

	// create a plotroot
	tx := NewRepresentation(nil, pubKey, 0, 0, 0, "hello")

	// create a plot
	targetBytes, err := hex.DecodeString(INITIAL_TARGET)
	if err != nil {
		t.Fatal(err)
	}
	var target PlotID
	copy(target[:], targetBytes)
	plot, err := NewPlot(PlotID{}, 0, target, PlotID{}, []*Representation{tx})
	if err != nil {
		t.Fatal(err)
	}

	// encode the header
	encodedHeader, err := encodePlotHeader(plot.Header, 12345)
	if err != nil {
		t.Fatal(err)
	}

	// decode the header
	header, when, err := decodePlotHeader(encodedHeader)
	if err != nil {
		t.Fatal(err)
	}

	// compare
	if *header != *plot.Header {
		t.Fatal("Decoded header doesn't match original")
	}

	if when != 12345 {
		t.Fatal("Decoded timestamp doesn't match original")
	}
}
