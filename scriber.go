package plotthread

import (
	"encoding/base64"
	"log"
	"math/big"
	"math/rand"
	"sync"
	"time"

	"golang.org/x/crypto/ed25519"
)

// Scriber tries to scribe a new tip plot.
type Scriber struct {
	pubKeys        []ed25519.PublicKey // receipients of any plotroots we scribe
	memo           string              // memo for plotroot of any plots we scriber
	plotStore      PlotStorage
	txQueue        RepresentationQueue
	ledger         Ledger
	processor      *Processor
	num            int
	keyIndex       int
	hashUpdateChan chan int64
	shutdownChan   chan struct{}
	wg             sync.WaitGroup
}

// HashrateMonitor collects hash counts from all scribers in order to monitor and display the aggregate hashrate.
type HashrateMonitor struct {
	hashUpdateChan chan int64
	shutdownChan   chan struct{}
	wg             sync.WaitGroup
}

// NewScriber returns a new Scriber instance.
func NewScriber(pubKeys []ed25519.PublicKey, memo string,
	plotStore PlotStorage, txQueue RepresentationQueue,
	ledger Ledger, processor *Processor,
	hashUpdateChan chan int64, num int) *Scriber {
	return &Scriber{
		pubKeys:        pubKeys,
		memo:           memo,
		plotStore:     plotStore,
		txQueue:        txQueue,
		ledger:         ledger,
		processor:      processor,
		num:            num,
		keyIndex:       rand.Intn(len(pubKeys)),
		hashUpdateChan: hashUpdateChan,
		shutdownChan:   make(chan struct{}),
	}
}

// NewHashrateMonitor returns a new HashrateMonitor instance.
func NewHashrateMonitor(hashUpdateChan chan int64) *HashrateMonitor {
	return &HashrateMonitor{
		hashUpdateChan: hashUpdateChan,
		shutdownChan:   make(chan struct{}),
	}
}

// Run executes the scriber's main loop in its own goroutine.
func (m *Scriber) Run() {
	m.wg.Add(1)
	go m.run()
}

func (m *Scriber) run() {
	defer m.wg.Done()

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	// don't start scribing until we think we're synced.
	// we're just wasting time and slowing down the sync otherwise
	ibd, _, err := IsInitialPlotDownload(m.ledger, m.plotStore)
	if err != nil {
		panic(err)
	}
	if ibd {
		log.Printf("Scriber %d waiting for plotthread sync\n", m.num)
	ready:
		for {
			select {
			case _, ok := <-m.shutdownChan:
				if !ok {
					log.Printf("Scriber %d shutting down...\n", m.num)
					return
				}
			case <-ticker.C:
				var err error
				ibd, _, err = IsInitialPlotDownload(m.ledger, m.plotStore)
				if err != nil {
					panic(err)
				}
				if ibd == false {
					// time to start scribing
					break ready
				}
			}
		}
	}

	// register for tip changes
	tipChangeChan := make(chan TipChange, 1)
	m.processor.RegisterForTipChange(tipChangeChan)
	defer m.processor.UnregisterForTipChange(tipChangeChan)

	// register for new representations
	newTxChan := make(chan NewTx, 1)
	m.processor.RegisterForNewRepresentations(newTxChan)
	defer m.processor.UnregisterForNewRepresentations(newTxChan)

	// main scribing loop
	var hashes, medianTimestamp int64
	var plot *Plot
	var targetInt *big.Int
	for {
		select {
		case tip := <-tipChangeChan:
			if !tip.Connect || tip.More {
				// only build off newly connected tip plots
				continue
			}

			// give up whatever plot we were working on
			log.Printf("Scriber %d received notice of new tip plot %s\n", m.num, tip.PlotID)

			var err error
			// start working on a new plot
			plot, err = m.createNextPlot(tip.PlotID, tip.Plot.Header)
			if err != nil {
				// ledger state is broken
				panic(err)
			}
			// make sure we're at least +1 the median timestamp
			medianTimestamp, err = computeMedianTimestamp(tip.Plot.Header, m.plotStore)
			if err != nil {
				panic(err)
			}
			if plot.Header.Time <= medianTimestamp {
				plot.Header.Time = medianTimestamp + 1
			}
			// convert our target to a big.Int
			targetInt = plot.Header.Target.GetBigInt()

		case newTx := <-newTxChan:
			log.Printf("Scriber %d received notice of new representation %s\n", m.num, newTx.RepresentationID)
			if plot == nil {
				// we're not working on a plot yet
				continue
			}

			if MAX_REPRESENTATIONS_TO_INCLUDE_PER_PLOT != 0 &&
				len(plot.Representations) >= MAX_REPRESENTATIONS_TO_INCLUDE_PER_PLOT {
				log.Printf("Per-plot representation limit hit (%d)\n", len(plot.Representations))
				continue
			}

			// add the representation to the plot (it updates the plotroot fee)
			if err := plot.AddRepresentation(newTx.RepresentationID, newTx.Representation); err != nil {
				log.Printf("Error adding new representation %s to plot: %s\n",
					newTx.RepresentationID, err)
				// abandon the plot
				plot = nil
			}

		case _, ok := <-m.shutdownChan:
			if !ok {
				log.Printf("Scriber %d shutting down...\n", m.num)
				return
			}

		case <-ticker.C:
			// update hashcount for hashrate monitor
			m.hashUpdateChan <- hashes
			hashes = 0

			if plot != nil {
				// update plot time every so often
				now := time.Now().Unix()
				if now > medianTimestamp {
					plot.Header.Time = now
				}
			}

		default:
			if plot == nil {
				// find the tip to start working off of
				tipID, tipHeader, _, err := getThreadTipHeader(m.ledger, m.plotStore)
				if err != nil {
					panic(err)
				}
				// create a new plot
				plot, err = m.createNextPlot(*tipID, tipHeader)
				if err != nil {
					panic(err)
				}
				// make sure we're at least +1 the median timestamp
				medianTimestamp, err = computeMedianTimestamp(tipHeader, m.plotStore)
				if err != nil {
					panic(err)
				}
				if plot.Header.Time <= medianTimestamp {
					plot.Header.Time = medianTimestamp + 1
				}
				// convert our target to a big.Int
				targetInt = plot.Header.Target.GetBigInt()
			}

			// hash the plot and check the proof-of-work
			idInt, attempts := plot.Header.IDFast(m.num)
			hashes += attempts
			if idInt.Cmp(targetInt) <= 0 {
				// found a solution
				id := new(PlotID).SetBigInt(idInt)
				log.Printf("Scriber %d scribed new plot %s\n", m.num, *id)

				// process the plot
				if err := m.processor.ProcessPlot(*id, plot, "localhost"); err != nil {
					log.Printf("Error processing scribed plot: %s\n", err)
				}

				plot = nil
				m.keyIndex = rand.Intn(len(m.pubKeys))
			} else {
				// no solution yet
				plot.Header.Nonce += attempts
				if plot.Header.Nonce > MAX_NUMBER {
					plot.Header.Nonce = 0
				}
			}
		}
	}
}

// Shutdown stops the scriber synchronously.
func (m *Scriber) Shutdown() {
	close(m.shutdownChan)
	m.wg.Wait()
	log.Printf("Scriber %d shutdown\n", m.num)
}

// Create a new plot off of the given tip plot.
func (m *Scriber) createNextPlot(tipID PlotID, tipHeader *PlotHeader) (*Plot, error) {
	log.Printf("Scriber %d scribing new plot from current tip %s\n", m.num, tipID)
	pubKey := m.pubKeys[m.keyIndex]
	return createNextPlot(tipID, tipHeader, m.txQueue, m.plotStore, m.ledger, pubKey, m.memo)
}

// Called by the scriber as well as the peer to support get_work.
func createNextPlot(tipID PlotID, tipHeader *PlotHeader, txQueue RepresentationQueue,
	plotStore PlotStorage, ledger Ledger, pubKey ed25519.PublicKey, memo string) (*Plot, error) {

	// fetch representations to confirm from the queue
	txs := txQueue.Get(MAX_REPRESENTATIONS_TO_INCLUDE_PER_PLOT - 1)

	// calculate total plot reward
	var newHeight int64 = tipHeader.Height + 1

	// build plotroot
	baseKey, _ := base64.StdEncoding.DecodeString("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	//baseKey := ed25519.PublicKey(rootKeyBytes)
	tx := NewRepresentation(baseKey, pubKey, 0, 0, newHeight, memo)

	// prepend plotroot
	txs = append([]*Representation{tx}, txs...)

	// compute the next target
	newTarget, err := computeTarget(tipHeader, plotStore, ledger)
	if err != nil {
		return nil, err
	}

	// create the plot
	plot, err := NewPlot(tipID, newHeight, newTarget, tipHeader.ThreadWork, txs)
	if err != nil {
		return nil, err
	}
	return plot, nil
}

// Run executes the hashrate monitor's main loop in its own goroutine.
func (h *HashrateMonitor) Run() {
	h.wg.Add(1)
	go h.run()
}

func (h *HashrateMonitor) run() {
	defer h.wg.Done()

	var totalHashes int64
	updateInterval := 1 * time.Minute
	ticker := time.NewTicker(updateInterval)
	defer ticker.Stop()

	for {
		select {
		case _, ok := <-h.shutdownChan:
			if !ok {
				log.Println("Hashrate monitor shutting down...")
				return
			}
		case hashes := <-h.hashUpdateChan:
			totalHashes += hashes
		case <-ticker.C:
			hps := float64(totalHashes) / updateInterval.Seconds()
			totalHashes = 0
			log.Printf("Hashrate: %.2f MH/s", hps/1000/1000)
		}
	}
}

// Shutdown stops the hashrate monitor synchronously.
func (h *HashrateMonitor) Shutdown() {
	close(h.shutdownChan)
	h.wg.Wait()
	log.Println("Hashrate monitor shutdown")
}
