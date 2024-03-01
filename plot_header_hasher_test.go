package plotthread

import (
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/crypto/ed25519"
)

// create a deterministic test plot
func makeTestPlot(n int) (*Plot, error) {
	txs := make([]*Representation, n)

	// create txs
	for i := 0; i < n; i++ {
		// create a sender
		seed := strings.Repeat(strconv.Itoa(i%10), ed25519.SeedSize)
		privKey := ed25519.NewKeyFromSeed([]byte(seed))
		pubKey := privKey.Public().(ed25519.PublicKey)

		// create a recipient
		seed2 := strings.Repeat(strconv.Itoa((i+1)%10), ed25519.SeedSize)
		privKey2 := ed25519.NewKeyFromSeed([]byte(seed2))
		pubKey2 := privKey2.Public().(ed25519.PublicKey)

		matures := MAX_NUMBER
		expires := MAX_NUMBER
		height := MAX_NUMBER

		tx := NewRepresentation(pubKey, pubKey2, matures, height, expires, "こんにちは")
		if len(tx.Memo) != 15 {
			// make sure len() gives us bytes not rune count
			return nil, fmt.Errorf("Expected memo length to be 15 but received %d", len(tx.Memo))
		}
		tx.Nonce = int32(123456789 + i)

		// sign the representation
		if err := tx.Sign(privKey); err != nil {
			return nil, err
		}
		txs[i] = tx
	}

	// create the plot
	targetBytes, err := hex.DecodeString(INITIAL_TARGET)
	if err != nil {
		return nil, err
	}
	var target PlotID
	copy(target[:], targetBytes)
	plot, err := NewPlot(PlotID{}, 0, target, PlotID{}, txs)
	if err != nil {
		return nil, err
	}
	return plot, nil
}

func TestPlotHeaderHasher(t *testing.T) {
	plot, err := makeTestPlot(10)
	if err != nil {
		t.Fatal(err)
	}

	if !compareIDs(plot) {
		t.Fatal("ID mismatch 1")
	}

	plot.Header.Time = 1234

	if !compareIDs(plot) {
		t.Fatal("ID mismatch 2")
	}

	plot.Header.Nonce = 1234

	if !compareIDs(plot) {
		t.Fatal("ID mismatch 3")
	}

	plot.Header.Nonce = 1235

	if !compareIDs(plot) {
		t.Fatal("ID mismatch 4")
	}

	plot.Header.Nonce = 1236
	plot.Header.Time = 1234

	if !compareIDs(plot) {
		t.Fatal("ID mismatch 5")
	}

	plot.Header.Time = 123498
	plot.Header.Nonce = 12370910

	txID, _ := plot.Representations[0].ID()
	if err := plot.AddRepresentation(txID, plot.Representations[0]); err != nil {
		t.Fatal(err)
	}

	if !compareIDs(plot) {
		t.Fatal("ID mismatch 6")
	}

	plot.Header.Time = 987654321

	if !compareIDs(plot) {
		t.Fatal("ID mismatch 7")
	}
}

func compareIDs(plot *Plot) bool {
	// compute header ID
	id, _ := plot.ID()

	// use delta method
	idInt, _ := plot.Header.IDFast(0)
	id2 := new(PlotID).SetBigInt(idInt)
	return id == *id2
}
