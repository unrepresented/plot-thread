package main

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"time"

	. "github.com/unrepresented/plot-thread"
	"golang.org/x/crypto/ed25519"
)

// Scribe a genesis plot
func main() {
	rand.Seed(time.Now().UnixNano())

	memoPtr := flag.String("memo", "", "A memo to include in the genesis plot's plotroot memo field")
	pubKeyPtr := flag.String("pubkey", "", "A public key to include in the genesis plot's plotroot output")
	flag.Parse()

	if len(*memoPtr) == 0 {
		log.Fatal("Memo required for genesis plot")
	}

	if len(*pubKeyPtr) == 0 {
		log.Fatal("Public key required for genesis plot")
	}

	pubKeyBytes, err := base64.StdEncoding.DecodeString(*pubKeyPtr)
	if err != nil {
		log.Fatal(err)
	}
	pubKey := ed25519.PublicKey(pubKeyBytes)

	// create the plotroot
	tx := NewRepresentation(nil, pubKey, 0, 0, 0, *memoPtr)

	// create the plot
	targetBytes, err := hex.DecodeString(INITIAL_TARGET)
	if err != nil {
		log.Fatal(err)
	}
	var target PlotID
	copy(target[:], targetBytes)
	plot, err := NewPlot(PlotID{}, 0, target, PlotID{}, []*Representation{tx})
	if err != nil {
		log.Fatal(err)
	}

	// scribe it
	targetInt := plot.Header.Target.GetBigInt()
	ticker := time.NewTicker(30 * time.Second)
done:
	for {
		select {
		case <-ticker.C:
			plot.Header.Time = time.Now().Unix()
		default:
			// keep hashing until proof-of-work is satisfied
			idInt, _ := plot.Header.IDFast(0)
			if idInt.Cmp(targetInt) <= 0 {
				break done
			}
			plot.Header.Nonce += 1
			if plot.Header.Nonce > MAX_NUMBER {
				plot.Header.Nonce = 0
			}
		}
	}

	plotJson, err := json.Marshal(plot)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("%s\n", plotJson)
}