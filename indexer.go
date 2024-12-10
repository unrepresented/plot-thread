package plotthread

import (
	"encoding/base64"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	olc "github.com/google/open-location-code/go"
	"golang.org/x/crypto/ed25519"
)

type Indexer struct {
	plotStore        PlotStorage
	ledger           Ledger
	processor        *Processor
	latestPlotID 	 PlotID
	latestHeight     int64	
	txGraph          *Graph
	catchments		 []string
	shutdownChan     chan struct{}
	wg               sync.WaitGroup
}

func NewIndexer(
	repGraph *Graph,
	plotStore PlotStorage,
	ledger Ledger,
	processor *Processor,
	genesisPlotID PlotID,
) *Indexer {
	return &Indexer{
		txGraph:          repGraph,
		plotStore:        plotStore,
		ledger:           ledger,
		processor:        processor,
		latestPlotID:     genesisPlotID,
		latestHeight:     0,	
		catchments: 		  make([]string, 0),
		shutdownChan:     make(chan struct{}),
	}
}

// Run executes the indexer's main loop in its own goroutine.
func (idx *Indexer) Run() {
	idx.wg.Add(1)
	go idx.run()
}

func (idx *Indexer) run() {
	defer idx.wg.Done()

	ticker := time.NewTicker(30 * time.Second)

	// don't start indexing until we think we're synced.
	// we're just wasting time and slowing down the sync otherwise
	ibd, _, err := IsInitialPlotDownload(idx.ledger, idx.plotStore)
	if err != nil {
		panic(err)
	}
	if ibd {
		log.Printf("Indexer waiting for plotthread sync\n")
	ready:
		for {
			select {
			case _, ok := <-idx.shutdownChan:
				if !ok {
					log.Printf("Indexer shutting down...\n")
					return
				}
			case <-ticker.C:
				var err error
				ibd, _, err = IsInitialPlotDownload(idx.ledger, idx.plotStore)
				if err != nil {
					panic(err)
				}
				if !ibd {
					// time to start indexing
					break ready
				}
			}
		}
	}

	ticker.Stop()

	header, _, err := idx.plotStore.GetPlotHeader(idx.latestPlotID)
	if err != nil {
		log.Println(err)
		return
	}
	if header == nil {
		// don't have it
		log.Println(err)
		return
	}
	branchType, err := idx.ledger.GetBranchType(idx.latestPlotID)
	if err != nil {
		log.Println(err)
		return
	}
	if branchType != MAIN {
		// not on the main branch
		log.Println(err)
		return
	}

	var height int64 = header.Height
	for {
		nextID, err := idx.ledger.GetPlotIDForHeight(height)
		if err != nil {
			log.Println(err)
			return
		}
		if nextID == nil {
			height -= 1
			break
		}

		plot, err := idx.plotStore.GetPlot(*nextID)
		if err != nil {
			// not found
			log.Println(err)
			return
		}

		if plot == nil {
			// not found
			log.Printf("No plot found with ID %v", nextID)
			return
		}

		idx.indexRepresentations(plot, *nextID, true)

		height += 1
	}	
	
	log.Printf("Finished indexing at height %v", idx.latestHeight)
	log.Printf("Latest indexed plotID: %v", idx.latestPlotID)
	
	idx.rankGraph()
	

	// register for tip changes
	tipChangeChan := make(chan TipChange, 1)
	idx.processor.RegisterForTipChange(tipChangeChan)
	defer idx.processor.UnregisterForTipChange(tipChangeChan)

	for {
		select {
		case tip := <-tipChangeChan:			
			log.Printf("Indexer received notice of new tip plot: %s at height: %d\n", tip.PlotID, tip.Plot.Header.Height)
			idx.indexRepresentations(tip.Plot, tip.PlotID, tip.Connect)//Todo: Does this capture every last representation?
			if !tip.More {
				idx.rankGraph()
			}
		case _, ok := <-idx.shutdownChan:
			if !ok {
				log.Printf("Indexer shutting down...\n")
				return
			}
		}
	}
}

func pubKeyToString(ppk ed25519.PublicKey) string{
	if(ppk == nil){
		return "0000000000000000000000000000000000000000000="
	}
	return base64.StdEncoding.EncodeToString(ppk[:])
}

// pads the input string to the required Base64 length for ED25519 keys
func padToBase64Length(input string) string {
	// ED25519 keys are 32 bytes, which in Base64 is 44 characters including padding
	const base64Length = 44

	// If the input string is already longer than or equal to the base64Length, return the input
	if len(input) >= base64Length {
		return input
	}

	// Calculate the number of zeros needed
	padLength := base64Length - len(input) - 1

	// Pad the input with trailing zeros
	paddedString := input + strings.Repeat("0", padLength) + "="

	return paddedString
}

func getNumericPrefixAsInt(s string) (int, error) {
	prefix := ""
	for _, char := range s {
		if unicode.IsDigit(char) {
			prefix += string(char)
		} else {
			break
		}
	}
	return strconv.Atoi(prefix)
}

func plusCodeFromIdentifier(pubKey string, catchments []string) (bool, string, string, string) {
	trimmed := strings.Trim(pubKey, "0=")
	splitTrimmed := strings.Split(trimmed, "/")

	if len(splitTrimmed) < 2 {
		return false, "", "", ""
	}

	houseFragment := splitTrimmed[1]

	catchmentIndex, err := getNumericPrefixAsInt(splitTrimmed[0])
	if err != nil {
		return false, "", "", ""
	}

	if len(catchments) < catchmentIndex + 1 {
		return false, "", "", ""
	}
	
	catchment := catchments[catchmentIndex]

	catchmentHouseId := catchment + houseFragment

	plusCode := strings.Split(catchmentHouseId, "//")[1]	

	if len(plusCode) != 12 && !strings.Contains(plusCode, "+") {
		
		// if len(plusCode) == 11{
		// 	/** adopt leniency?? */
		// 	plusCode = plusCode[:8] + "+" + plusCode[8:]
		// }
		
		return false, "", "", ""
	}		

	if err := olc.CheckFull(plusCode); err != nil {		
		return false, "", "", ""
	}

	houseId := splitTrimmed[0] + splitTrimmed[1]

	return true, plusCode, catchment, houseId
}

//:= [issues//0FR5CX/odorous000000000000000000000=
func inflateCatchmentIssues(pubKey string) (bool, string, string) {
	catchmentIssue, ok := strings.CutPrefix(strings.Trim(pubKey, "0="), "issues//")

	splitPK := strings.Split(catchmentIssue, "/")

	if ok && len(splitPK) == 2 {

		plusArea := splitPK[0] //TODO: validate
		issue := splitPK[1]

		if (plusArea != "") && (issue != "") {		
			return true, plusArea, issue
		}
	}	

	return false, "", ""
}


func inflateCoverageFrontier(pubKey string) (bool, string, string, string, string) {
		
	splitPK := strings.Split(strings.Trim(pubKey, "0="), "/")

	if len(splitPK) == 5 {

		houseId := splitPK[0] + "/" + splitPK[1]
		room := splitPK[2]
		fixture := splitPK[3]
		issues := splitPK[4]

		if (room != "") && (fixture != "") && (issues != "") {		
			return true, houseId, room, fixture, issues
		}
	}	

	return false, "", "", "", ""
}

func (idx *Indexer) rankGraph(){
	log.Printf("Indexer ranking at height: %d\n", idx.latestHeight)
	idx.txGraph.Rank(1.0, 1e-6)
	log.Printf("Ranking finished")
}	

func (idx *Indexer) indexRepresentations(plot *Plot, id PlotID, increment bool) {
	idx.latestPlotID = id
	idx.latestHeight = plot.Header.Height

	for i := 0; i < len(plot.Representations); i++ {
		rep := plot.Representations[i]

		repFor := pubKeyToString(rep.For)
		repBy := pubKeyToString(rep.By)

		/* Capture catchments */
		if rep.By == nil && strings.HasPrefix(repFor, "issues//") {
			trimmed := strings.Trim(repFor, "0=")
			//Todo: validate catchment protocol
			
			if increment {
				idx.catchments = append(idx.catchments, trimmed)
			}
			//Todo: remove when increment = false
				
		}
		


		incrementBy := 0.00

		if increment {
			incrementBy = 1
		} else {
			incrementBy = -1 //Plot/block disconnect
		}

		if ok, _, catchment, _ := plusCodeFromIdentifier(repFor, idx.catchments); ok {

			if okk, houseId, room, _, issues := inflateCoverageFrontier(repFor); okk {

				houseIdKey := padToBase64Length(houseId)
				roomKey := padToBase64Length(houseId + "/" + room)
				
				idx.txGraph.Link(repBy, houseIdKey, incrementBy)
				idx.txGraph.Link(houseIdKey, roomKey, incrementBy)
				idx.txGraph.Link(roomKey, repFor, incrementBy)

				isss := strings.Split(issues, "+")

				for _, issue := range isss {
					idx.txGraph.Link(repFor, padToBase64Length(catchment + "/" + issue), incrementBy)
					idx.txGraph.Link(padToBase64Length(catchment + "/" + issue), padToBase64Length(catchment), incrementBy)
				}	
			}			
					
		} else {
			idx.txGraph.Link(repBy, repFor, incrementBy)
		}
	}
}

// Shutdown stops the indexer synchronously.
func (idx *Indexer) Shutdown() {
	close(idx.shutdownChan)
	idx.wg.Wait()
	log.Printf("Indexer shutdown\n")
}