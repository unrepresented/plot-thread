package main

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/logrusorgru/aurora"
	. "github.com/unrepresented/plot-thread"
	"golang.org/x/crypto/ed25519"
)

// A small tool to inspect the plot thread and ledger offline
func main() {
	var commands = []string{
		"height", "imbalance", "imbalance_at", "plot", "plot_at", "tx", "history", "verify",
	}

	dataDirPtr := flag.String("datadir", "", "Path to a directory containing plot thread data")
	pubKeyPtr := flag.String("pubkey", "", "Base64 encoded public key")
	cmdPtr := flag.String("command", "height", "Commands: "+strings.Join(commands, ", "))
	heightPtr := flag.Int("height", 0, "Plot thread height")
	plotIDPtr := flag.String("plot_id", "", "Plot ID")
	txIDPtr := flag.String("tx_id", "", "Representation ID")
	startHeightPtr := flag.Int("start_height", 0, "Start plot height (for use with \"history\")")
	startIndexPtr := flag.Int("start_index", 0, "Start representation index (for use with \"history\")")
	endHeightPtr := flag.Int("end_height", 0, "End plot height (for use with \"history\")")
	limitPtr := flag.Int("limit", 3, "Limit (for use with \"history\")")
	flag.Parse()

	if len(*dataDirPtr) == 0 {
		log.Printf("You must specify a -datadir\n")
		os.Exit(-1)
	}

	var pubKey ed25519.PublicKey
	if len(*pubKeyPtr) != 0 {
		// decode the key
		pubKeyBytes, err := base64.StdEncoding.DecodeString(*pubKeyPtr)
		if err != nil {
			log.Fatal(err)
		}
		pubKey = ed25519.PublicKey(pubKeyBytes)
	}

	var plotID *PlotID
	if len(*plotIDPtr) != 0 {
		plotIDBytes, err := hex.DecodeString(*plotIDPtr)
		if err != nil {
			log.Fatal(err)
		}
		plotID = new(PlotID)
		copy(plotID[:], plotIDBytes)
	}

	var txID *RepresentationID
	if len(*txIDPtr) != 0 {
		txIDBytes, err := hex.DecodeString(*txIDPtr)
		if err != nil {
			log.Fatal(err)
		}
		txID = new(RepresentationID)
		copy(txID[:], txIDBytes)
	}

	// instatiate plot storage (read-only)
	plotStore, err := NewPlotStorageDisk(
		filepath.Join(*dataDirPtr, "plots"),
		filepath.Join(*dataDirPtr, "headers.db"),
		true,  // read-only
		false, // compress (if a plot is compressed storage will figure it out)
	)
	if err != nil {
		log.Fatal(err)
	}

	// instantiate the ledger (read-only)
	ledger, err := NewLedgerDisk(filepath.Join(*dataDirPtr, "ledger.db"),
		true,  // read-only
		false, // prune (no effect with read-only set)
		plotStore)
	if err != nil {
		log.Fatal(err)
	}

	// get the current height
	_, currentHeight, err := ledger.GetThreadTip()
	if err != nil {
		log.Fatal(err)
	}

	switch *cmdPtr {
	case "height":
		log.Printf("Current plot thread height is: %d\n", aurora.Bold(currentHeight))

	case "imbalance":
		if pubKey == nil {
			log.Fatal("-pubkey required for \"imbalance\" command")
		}
		imbalance, err := ledger.GetPublicKeyImbalance(pubKey)
		if err != nil {
			log.Fatal(err)
		}
		log.Printf("Current imbalance: %+d\n", aurora.Bold(imbalance))

	case "imbalance_at":
		if pubKey == nil {
			log.Fatal("-pubkey required for \"imbalance_at\" command")
		}
		imbalance, err := ledger.GetPublicKeyImbalanceAt(pubKey, int64(*heightPtr))
		if err != nil {
			log.Fatal(err)
		}
		log.Printf("Imbalance at height %d: %+d\n", *heightPtr, aurora.Bold(imbalance))

	case "plot_at":
		id, err := ledger.GetPlotIDForHeight(int64(*heightPtr))
		if err != nil {
			log.Fatal(err)
		}
		if id == nil {
			log.Fatalf("No plot found at height %d\n", *heightPtr)
		}
		plot, err := plotStore.GetPlot(*id)
		if err != nil {
			log.Fatal(err)
		}
		if plot == nil {
			log.Fatalf("No plot with ID %s\n", *id)
		}
		displayPlot(*id, plot)

	case "plot":
		if plotID == nil {
			log.Fatalf("-plot_id required for \"plot\" command")
		}
		plot, err := plotStore.GetPlot(*plotID)
		if err != nil {
			log.Fatal(err)
		}
		if plot == nil {
			log.Fatalf("No plot with id %s\n", *plotID)
		}
		displayPlot(*plotID, plot)

	case "tx":
		if txID == nil {
			log.Fatalf("-tx_id required for \"tx\" command")
		}
		id, index, err := ledger.GetRepresentationIndex(*txID)
		if err != nil {
			log.Fatal(err)
		}
		if id == nil {
			log.Fatalf("Representation %s not found", *txID)
		}
		tx, header, err := plotStore.GetRepresentation(*id, index)
		if err != nil {
			log.Fatal(err)
		}
		if tx == nil {
			log.Fatalf("No representation found with ID %s\n", *txID)
		}
		displayRepresentation(*txID, header, index, tx)

	case "history":
		if pubKey == nil {
			log.Fatal("-pubkey required for \"history\" command")
		}
		bIDs, indices, stopHeight, stopIndex, err := ledger.GetPublicKeyRepresentationIndicesRange(
			pubKey, int64(*startHeightPtr), int64(*endHeightPtr), int(*startIndexPtr), int(*limitPtr))
		if err != nil {
			log.Fatal(err)
		}
		displayHistory(bIDs, indices, stopHeight, stopIndex, plotStore)

	case "verify":
		verify(ledger, plotStore, pubKey, currentHeight)
	}

	// close storage
	if err := plotStore.Close(); err != nil {
		log.Println(err)
	}
	if err := ledger.Close(); err != nil {
		log.Println(err)
	}
}

type concisePlot struct {
	ID           PlotID         `json:"id"`
	Header       PlotHeader     `json:"header"`
	Representations []RepresentationID `json:"representations"`
}

func displayPlot(id PlotID, plot *Plot) {
	b := concisePlot{
		ID:           id,
		Header:       *plot.Header,
		Representations: make([]RepresentationID, len(plot.Representations)),
	}

	for i := 0; i < len(plot.Representations); i++ {
		txID, err := plot.Representations[i].ID()
		if err != nil {
			panic(err)
		}
		b.Representations[i] = txID
	}

	bJson, err := json.MarshalIndent(&b, "", "    ")
	if err != nil {
		panic(err)
	}

	fmt.Println(string(bJson))
}

type txWithContext struct {
	PlotID     PlotID       `json:"plot_id"`
	PlotHeader PlotHeader   `json:"plot_header"`
	TxIndex     int           `json:"representation_index_in_plot"`
	ID          RepresentationID `json:"representation_id"`
	Representation *Representation  `json:"representation"`
}

func displayRepresentation(txID RepresentationID, header *PlotHeader, index int, tx *Representation) {
	plotID, err := header.ID()
	if err != nil {
		panic(err)
	}

	t := txWithContext{
		PlotID:     plotID,
		PlotHeader: *header,
		TxIndex:     index,
		ID:          txID,
		Representation: tx,
	}

	txJson, err := json.MarshalIndent(&t, "", "    ")
	if err != nil {
		panic(err)
	}

	fmt.Println(string(txJson))
}

type history struct {
	Representations []txWithContext `json:"representations"`
}

func displayHistory(bIDs []PlotID, indices []int, stopHeight int64, stopIndex int, plotStore PlotStorage) {
	h := history{Representations: make([]txWithContext, len(indices))}
	for i := 0; i < len(indices); i++ {
		tx, header, err := plotStore.GetRepresentation(bIDs[i], indices[i])
		if err != nil {
			panic(err)
		}
		if tx == nil {
			panic("No representation found at index")
		}
		txID, err := tx.ID()
		if err != nil {
			panic(err)
		}
		h.Representations[i] = txWithContext{
			PlotID:     bIDs[i],
			PlotHeader: *header,
			TxIndex:     indices[i],
			ID:          txID,
			Representation: tx,
		}
	}

	hJson, err := json.MarshalIndent(&h, "", "    ")
	if err != nil {
		panic(err)
	}

	fmt.Println(string(hJson))
}

func verify(ledger Ledger, plotStore PlotStorage, pubKey ed25519.PublicKey, height int64) {
	var err error
	var expect, found int64

	if pubKey == nil {
		// compute expected total imbalance
		if height-PLOTROOT_MATURITY >= 0 {
			// sum all mature rewards per schedule
			var i int64
			for i = 0; i <= height-PLOTROOT_MATURITY; i++ {
				expect += 1
			}
		}

		// compute the imbalance given the sum of all public key imbalances
		found, err = ledger.Imbalance()
	} else {
		// get expected imbalance
		expect, err = ledger.GetPublicKeyImbalance(pubKey)
		if err != nil {
			log.Fatal(err)
		}

		// compute the imbalance based on history
		found, err = ledger.GetPublicKeyImbalanceAt(pubKey, height)
		if err != nil {
			log.Fatal(err)
		}
	}

	if err != nil {
		log.Fatal(err)
	}

	if expect != found {
		log.Fatalf("%s: At height %d, we expected %+d crux but we found %+d\n",
			aurora.Bold(aurora.Red("FAILURE")),
			aurora.Bold(height),
			aurora.Bold(expect),
			aurora.Bold(found))
	}

	log.Printf("%s: At height %d, we expected %+d crux and we found %+d\n",
		aurora.Bold(aurora.Green("SUCCESS")),
		aurora.Bold(height),
		aurora.Bold(expect),
		aurora.Bold(found))
}
