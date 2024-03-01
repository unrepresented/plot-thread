package plotthread

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"log"
	"math/big"
	"sort"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/ed25519"
)

// Processor processes plots and representations in order to construct the ledger.
// It also manages the storage of all plot thread data as well as inclusion of new representations into the representation queue.
type Processor struct {
	genesisID               PlotID
	plotStore              PlotStorage                  // storage of raw plot data
	txQueue                 RepresentationQueue              // queue of representations to confirm
	ledger                  Ledger                        // ledger built from processing plots
	txChan                  chan txToProcess              // receive new representations to process on this channel
	plotChan               chan plotToProcess           // receive new plots to process on this channel
	registerNewTxChan       chan chan<- NewTx             // receive registration requests for new representation notifications
	unregisterNewTxChan     chan chan<- NewTx             // receive unregistration requests for new representation notifications
	registerTipChangeChan   chan chan<- TipChange         // receive registration requests for tip change notifications
	unregisterTipChangeChan chan chan<- TipChange         // receive unregistration requests for tip change notifications
	newTxChannels           map[chan<- NewTx]struct{}     // channels needing notification of newly processed representations
	tipChangeChannels       map[chan<- TipChange]struct{} // channels needing notification of changes to main thread tip plots
	shutdownChan            chan struct{}
	wg                      sync.WaitGroup
}

// NewTx is a message sent to registered new representation channels when a representation is queued.
type NewTx struct {
	RepresentationID RepresentationID // representation ID
	Representation   *Representation  // new representation
	Source        string        // who sent it
}

// TipChange is a message sent to registered new tip channels on main thread tip (dis-)connection..
type TipChange struct {
	PlotID PlotID // plot ID of the main thread tip plot
	Plot   *Plot  // full plot
	Source  string  // who sent the plot that caused this change
	Connect bool    // true if the tip has been connected. false for disconnected
	More    bool    // true if the tip has been connected and more connections are expected
}

type txToProcess struct {
	id         RepresentationID // representation ID
	tx         *Representation  // representation to process
	source     string        // who sent it
	resultChan chan<- error  // channel to receive the result
}

type plotToProcess struct {
	id         PlotID      // plot ID
	plot      *Plot       // plot to process
	source     string       // who sent it
	resultChan chan<- error // channel to receive the result
}

// NewProcessor returns a new Processor instance.
func NewProcessor(genesisID PlotID, plotStore PlotStorage, txQueue RepresentationQueue, ledger Ledger) *Processor {
	return &Processor{
		genesisID:               genesisID,
		plotStore:              plotStore,
		txQueue:                 txQueue,
		ledger:                  ledger,
		txChan:                  make(chan txToProcess, 100),
		plotChan:               make(chan plotToProcess, 10),
		registerNewTxChan:       make(chan chan<- NewTx),
		unregisterNewTxChan:     make(chan chan<- NewTx),
		registerTipChangeChan:   make(chan chan<- TipChange),
		unregisterTipChangeChan: make(chan chan<- TipChange),
		newTxChannels:           make(map[chan<- NewTx]struct{}),
		tipChangeChannels:       make(map[chan<- TipChange]struct{}),
		shutdownChan:            make(chan struct{}),
	}
}

// Run executes the Processor's main loop in its own goroutine.
// It verifies and processes plots and representations.
func (p *Processor) Run() {
	p.wg.Add(1)
	go p.run()
}

func (p *Processor) run() {
	defer p.wg.Done()

	for {
		select {
		case txToProcess := <-p.txChan:
			// process a representation
			err := p.processRepresentation(txToProcess.id, txToProcess.tx, txToProcess.source)
			if err != nil {
				log.Println(err)
			}

			// send back the result
			txToProcess.resultChan <- err

		case plotToProcess := <-p.plotChan:
			// process a plot
			before := time.Now().UnixNano()
			err := p.processPlot(plotToProcess.id, plotToProcess.plot, plotToProcess.source)
			if err != nil {
				log.Println(err)
			}
			after := time.Now().UnixNano()

			log.Printf("Processing took %d ms, %d representation(s), representation queue length: %d\n",
				(after-before)/int64(time.Millisecond),
				len(plotToProcess.plot.Representations),
				p.txQueue.Len())

			// send back the result
			plotToProcess.resultChan <- err

		case ch := <-p.registerNewTxChan:
			p.newTxChannels[ch] = struct{}{}

		case ch := <-p.unregisterNewTxChan:
			delete(p.newTxChannels, ch)

		case ch := <-p.registerTipChangeChan:
			p.tipChangeChannels[ch] = struct{}{}

		case ch := <-p.unregisterTipChangeChan:
			delete(p.tipChangeChannels, ch)

		case _, ok := <-p.shutdownChan:
			if !ok {
				log.Println("Processor shutting down...")
				return
			}
		}
	}
}

// ProcessRepresentation is called to process a new candidate representation for the representation queue.
func (p *Processor) ProcessRepresentation(id RepresentationID, tx *Representation, from string) error {
	resultChan := make(chan error)
	p.txChan <- txToProcess{id: id, tx: tx, source: from, resultChan: resultChan}
	return <-resultChan
}

// ProcessPlot is called to process a new candidate plot thread tip.
func (p *Processor) ProcessPlot(id PlotID, plot *Plot, from string) error {
	resultChan := make(chan error)
	p.plotChan <- plotToProcess{id: id, plot: plot, source: from, resultChan: resultChan}
	return <-resultChan
}

// RegisterForNewRepresentations is called to register to receive notifications of newly queued representations.
func (p *Processor) RegisterForNewRepresentations(ch chan<- NewTx) {
	p.registerNewTxChan <- ch
}

// UnregisterForNewRepresentations is called to unregister to receive notifications of newly queued representations
func (p *Processor) UnregisterForNewRepresentations(ch chan<- NewTx) {
	p.unregisterNewTxChan <- ch
}

// RegisterForTipChange is called to register to receive notifications of tip plot changes.
func (p *Processor) RegisterForTipChange(ch chan<- TipChange) {
	p.registerTipChangeChan <- ch
}

// UnregisterForTipChange is called to unregister to receive notifications of tip plot changes.
func (p *Processor) UnregisterForTipChange(ch chan<- TipChange) {
	p.unregisterTipChangeChan <- ch
}

// Shutdown stops the processor synchronously.
func (p *Processor) Shutdown() {
	close(p.shutdownChan)
	p.wg.Wait()
	log.Println("Processor shutdown")
}

// Process a representation
func (p *Processor) processRepresentation(id RepresentationID, tx *Representation, source string) error {
	log.Printf("Processing representation %s\n", id)

	// context-free checks
	if err := checkRepresentation(id, tx); err != nil {
		return err
	}

	// no loose plotroots
	if tx.IsPlotroot() {
		return fmt.Errorf("Plotroot representation %s only allowed in plot", id)
	}

	// is the queue full?
	if p.txQueue.Len() >= MAX_REPRESENTATION_QUEUE_LENGTH {
		return fmt.Errorf("No room for representation %s, queue is full", id)
	}

	// is it confirmed already?
	plotID, _, err := p.ledger.GetRepresentationIndex(id)
	if err != nil {
		return err
	}
	if plotID != nil {
		return fmt.Errorf("Representation %s is already confirmed", id)
	}

	// check series, maturity and expiration
	tipID, tipHeight, err := p.ledger.GetThreadTip()
	if err != nil {
		return err
	}
	if tipID == nil {
		return fmt.Errorf("No main thread tip id found")
	}

	// is the series current for inclusion in the next plot?
	if !checkRepresentationSeries(tx, tipHeight+1) {
		return fmt.Errorf("Representation %s would have invalid series", id)
	}

	// would it be mature if included in the next plot?
	if !tx.IsMature(tipHeight + 1) {
		return fmt.Errorf("Representation %s would not be mature", id)
	}

	// is it expired if included in the next plot?
	if tx.IsExpired(tipHeight + 1) {
		return fmt.Errorf("Representation %s is expired, height: %d, expires: %d",
			id, tipHeight, tx.Expires)
	}

	// verify signature
	ok, err := tx.Verify()
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("Signature verification failed for %s", id)
	}

	// rejects a representation if sender would have insufficient imbalance
	ok, err = p.txQueue.Add(id, tx)
	if err != nil {
		return err
	}
	if !ok {
		// don't notify others if the representation already exists in the queue
		return nil
	}

	// notify channels
	for ch := range p.newTxChannels {
		ch <- NewTx{RepresentationID: id, Representation: tx, Source: source}
	}
	return nil
}

// Context-free representation sanity checker
func checkRepresentation(id RepresentationID, tx *Representation) error {
	// sane-ish time.
	// representation timestamps are strictly for user and application usage.
	// we make no claims to their validity and rely on them for nothing.
	if tx.Time < 0 || tx.Time > MAX_NUMBER {
		return fmt.Errorf("Invalid representation time, representation: %s", id)
	}

	// no negative nonces
	if tx.Nonce < 0 {
		return fmt.Errorf("Negative nonce value, representation: %s", id)
	}

	if tx.IsPlotroot() {
		// no maturity for plotroot
		if tx.Matures > 0 {
			return fmt.Errorf("Plotroot can't have a maturity, representation: %s", id)
		}
		// no expiration for plotroot
		if tx.Expires > 0 {
			return fmt.Errorf("Plotroot can't expire, representation: %s", id)
		}
		// no signature on plotroot
		if len(tx.Signature) != 0 {
			return fmt.Errorf("Plotroot can't have a signature, representation: %s", id)
		}
	} else {
		// sanity check sender
		if len(tx.From) != ed25519.PublicKeySize {
			return fmt.Errorf("Invalid representation sender, representation: %s", id)
		}
		// sanity check signature
		if len(tx.Signature) != ed25519.SignatureSize {
			return fmt.Errorf("Invalid representation signature, representation: %s", id)
		}
	}

	// sanity check recipient
	if tx.To == nil {
		return fmt.Errorf("Representation %s missing recipient", id)
	}
	if len(tx.To) != ed25519.PublicKeySize {
		return fmt.Errorf("Invalid representation recipient, representation: %s", id)
	}

	// no pays to self
	if bytes.Equal(tx.From, tx.To) {
		return fmt.Errorf("Representation %s to self is invalid", id)
	}

	// make sure memo is valid ascii/utf8
	if !utf8.ValidString(tx.Memo) {
		return fmt.Errorf("Representation %s memo contains invalid utf8 characters", id)
	}

	// check memo length
	if len(tx.Memo) > MAX_MEMO_LENGTH {
		return fmt.Errorf("Representation %s memo length exceeded", id)
	}

	// sanity check maturity, expiration and series
	if tx.Matures < 0 || tx.Matures > MAX_NUMBER {
		return fmt.Errorf("Invalid maturity, representation: %s", id)
	}
	if tx.Expires < 0 || tx.Expires > MAX_NUMBER {
		return fmt.Errorf("Invalid expiration, representation: %s", id)
	}
	if tx.Series <= 0 || tx.Series > MAX_NUMBER {
		return fmt.Errorf("Invalid series, representation: %s", id)
	}

	return nil
}

// The series must be within the acceptable range given the current height
func checkRepresentationSeries(tx *Representation, height int64) bool {	 
	if tx.IsPlotroot() {
		// plotroots must start a new series right on time
		return tx.Series == height/PLOTS_UNTIL_NEW_SERIES+1
	}

	// user representations have a grace period (1 full series) to mitigate effects
	// of any potential queueing delay and/or reorgs near series switchover time
	high := height/PLOTS_UNTIL_NEW_SERIES + 1
	low := high - 1
	if low == 0 {
		low = 1
	}
	return tx.Series >= low && tx.Series <= high
}

// Process a plot
func (p *Processor) processPlot(id PlotID, plot *Plot, source string) error {
	log.Printf("Processing plot %s\n", id)

	now := time.Now().Unix()

	// did we process this plot already?
	branchType, err := p.ledger.GetBranchType(id)
	if err != nil {
		return err
	}
	if branchType != UNKNOWN {
		log.Printf("Already processed plot %s", id)
		return nil
	}

	// sanity check the plot
	if err := checkPlot(id, plot, now); err != nil {
		return err
	}

	// have we processed its parent?
	branchType, err = p.ledger.GetBranchType(plot.Header.Previous)
	if err != nil {
		return err
	}
	if branchType != MAIN && branchType != SIDE {
		if id == p.genesisID {
			// store it
			if err := p.plotStore.Store(id, plot, now); err != nil {
				return err
			}
			// begin the ledger
			if err := p.connectPlot(id, plot, source, false); err != nil {
				return err
			}
			log.Printf("Connected plot %s\n", id)
			return nil
		}
		// current plot is an orphan
		return fmt.Errorf("Plot %s is an orphan", id)
	}

	// attempt to extend the thread
	return p.acceptPlot(id, plot, now, source)
}

// Context-free plot sanity checker
func checkPlot(id PlotID, plot *Plot, now int64) error {
	// sanity check time
	if plot.Header.Time < 0 || plot.Header.Time > MAX_NUMBER {
		return fmt.Errorf("Time value is invalid, plot %s", id)
	}

	// check timestamp isn't too far in the future
	if plot.Header.Time > now+MAX_FUTURE_SECONDS {
		return fmt.Errorf(
			"Timestamp %d too far in the future, now %d, plot %s",
			plot.Header.Time,
			now,
			id,
		)
	}

	// proof-of-work should satisfy declared target
	if !plot.CheckPOW(id) {
		return fmt.Errorf("Insufficient proof-of-work for plot %s", id)
	}

	// sanity check nonce
	if plot.Header.Nonce < 0 || plot.Header.Nonce > MAX_NUMBER {
		return fmt.Errorf("Nonce value is invalid, plot %s", id)
	}

	// sanity check height
	if plot.Header.Height < 0 || plot.Header.Height > MAX_NUMBER {
		return fmt.Errorf("Height value is invalid, plot %s", id)
	}

	// check against known checkpoints
	if err := CheckpointCheck(id, plot.Header.Height); err != nil {
		return err
	}

	// sanity check representation count
	if plot.Header.RepresentationCount < 0 {
		return fmt.Errorf("Negative representation count in header of plot %s", id)
	}

	if int(plot.Header.RepresentationCount) != len(plot.Representations) {
		return fmt.Errorf("Representation count in header doesn't match plot %s", id)
	}

	// must have at least one representation
	if len(plot.Representations) == 0 {
		return fmt.Errorf("No representations in plot %s", id)
	}

	// first tx must be a plotroot
	if !plot.Representations[0].IsPlotroot() {
		return fmt.Errorf("First representation is not a plotroot in plot %s", id)
	}

	// check max number of representations
	max := computeMaxRepresentationsPerPlot(plot.Header.Height)
	if len(plot.Representations) > max {
		return fmt.Errorf("Plot %s contains too many representations %d, max: %d",
			id, len(plot.Representations), max)
	}

	// the rest must not be plotroots
	if len(plot.Representations) > 1 {
		for i := 1; i < len(plot.Representations); i++ {
			if plot.Representations[i].IsPlotroot() {
				return fmt.Errorf("Multiple plotroot representations in plot %s", id)
			}
		}
	}

	// basic representation checks that don't depend on context
	txIDs := make(map[RepresentationID]bool)
	for _, tx := range plot.Representations {
		id, err := tx.ID()
		if err != nil {
			return err
		}
		if err := checkRepresentation(id, tx); err != nil {
			return err
		}
		txIDs[id] = true
	}

	// check for duplicate representations
	if len(txIDs) != len(plot.Representations) {
		return fmt.Errorf("Duplicate representation in plot %s", id)
	}

	// verify hash list root
	hashListRoot, err := computeHashListRoot(nil, plot.Representations)
	if err != nil {
		return err
	}
	if hashListRoot != plot.Header.HashListRoot {
		return fmt.Errorf("Hash list root mismatch for plot %s", id)
	}

	return nil
}

// Computes the maximum number of representations allowed in a plot at the given height. Inspired by BIP 101
func computeMaxRepresentationsPerPlot(height int64) int {
	if height >= MAX_REPRESENTATIONS_PER_PLOT_EXCEEDED_AT_HEIGHT {
		// I guess we can revisit this sometime in the next 35 years if necessary
		return MAX_REPRESENTATIONS_PER_PLOT
	}

	// piecewise-linear-between-doublings growth
	doublings := height / PLOTS_UNTIL_REPRESENTATIONS_PER_PLOT_DOUBLING
	if doublings >= 64 {
		panic("Overflow uint64")
	}
	remainder := height % PLOTS_UNTIL_REPRESENTATIONS_PER_PLOT_DOUBLING
	factor := int64(1 << uint64(doublings))
	interpolate := (INITIAL_MAX_REPRESENTATIONS_PER_PLOT * factor * remainder) /
		PLOTS_UNTIL_REPRESENTATIONS_PER_PLOT_DOUBLING
	return int(INITIAL_MAX_REPRESENTATIONS_PER_PLOT*factor + interpolate)
}

// Attempt to extend the thread with the new plot
func (p *Processor) acceptPlot(id PlotID, plot *Plot, now int64, source string) error {
	prevHeader, _, err := p.plotStore.GetPlotHeader(plot.Header.Previous)
	if err != nil {
		return err
	}

	// check height
	newHeight := prevHeader.Height + 1
	if plot.Header.Height != newHeight {
		return fmt.Errorf("Expected height %d found %d for plot %s",
			newHeight, plot.Header.Height, id)
	}

	// did we process it already?
	branchType, err := p.ledger.GetBranchType(id)
	if err != nil {
		return err
	}
	if branchType != UNKNOWN {
		log.Printf("Already processed plot %s", id)
		return nil
	}

	// check declared proof of work is correct
	target, err := computeTarget(prevHeader, p.plotStore, p.ledger)
	if err != nil {
		return err
	}
	if plot.Header.Target != target {
		return fmt.Errorf("Incorrect target %s, expected %s for plot %s",
			plot.Header.Target, target, id)
	}

	// check that cumulative work is correct
	threadWork := computeThreadWork(plot.Header.Target, prevHeader.ThreadWork)
	if plot.Header.ThreadWork != threadWork {
		return fmt.Errorf("Incorrect thread work %s, expected %s for plot %s",
			plot.Header.ThreadWork, threadWork, id)
	}

	// check that the timestamp isn't too far in the past
	medianTimestamp, err := computeMedianTimestamp(prevHeader, p.plotStore)
	if err != nil {
		return err
	}
	if plot.Header.Time <= medianTimestamp {
		return fmt.Errorf("Timestamp is too early for plot %s", id)
	}

	// check series, maturity, expiration then verify signatures
	for _, tx := range plot.Representations {
		txID, err := tx.ID()
		if err != nil {
			return err
		}
		if !checkRepresentationSeries(tx, plot.Header.Height) {
			return fmt.Errorf("Representation %s would have invalid series", txID)
		}
		if !tx.IsPlotroot() {
			if !tx.IsMature(plot.Header.Height) {
				return fmt.Errorf("Representation %s is immature", txID)
			}
			if tx.IsExpired(plot.Header.Height) {
				return fmt.Errorf("Representation %s is expired", txID)
			}
			// if it's in the queue with the same signature we've verified it already
			if !p.txQueue.ExistsSigned(txID, tx.Signature) {
				ok, err := tx.Verify()
				if err != nil {
					return err
				}
				if !ok {
					return fmt.Errorf("Signature verification failed, representation: %s", txID)
				}
			}
		}
	}

	// store the plot if we think we're going to accept it
	if err := p.plotStore.Store(id, plot, now); err != nil {
		return err
	}

	// get the current tip before we try adjusting the thread
	tipID, _, err := p.ledger.GetThreadTip()
	if err != nil {
		return err
	}

	// finish accepting the plot if possible
	if err := p.acceptPlotContinue(id, plot, now, prevHeader, source); err != nil {
		// we may have disconnected the old best thread and partially
		// connected the new one before encountering a problem. re-activate it now
		if err2 := p.reconnectTip(*tipID, source); err2 != nil {
			log.Printf("Error reconnecting tip: %s, plot: %s\n", err2, *tipID)
		}
		// return the original error
		return err
	}

	return nil
}

// Compute expected target of the current plot
func computeTarget(prevHeader *PlotHeader, plotStore PlotStorage, ledger Ledger) (PlotID, error) {
	if prevHeader.Height >= BITCOIN_CASH_RETARGET_ALGORITHM_HEIGHT {
		return computeTargetBitcoinCash(prevHeader, plotStore, ledger)
	}
	return computeTargetBitcoin(prevHeader, plotStore)
}

// Original target computation
func computeTargetBitcoin(prevHeader *PlotHeader, plotStore PlotStorage) (PlotID, error) {
	if (prevHeader.Height+1)%RETARGET_INTERVAL != 0 {
		// not 2016th plot, use previous plot's value
		return prevHeader.Target, nil
	}

	// defend against time warp attack
	plotsToGoBack := RETARGET_INTERVAL - 1
	if (prevHeader.Height + 1) != RETARGET_INTERVAL {
		plotsToGoBack = RETARGET_INTERVAL
	}

	// walk back to the first plot of the interval
	firstHeader := prevHeader
	for i := 0; i < plotsToGoBack; i++ {
		var err error
		firstHeader, _, err = plotStore.GetPlotHeader(firstHeader.Previous)
		if err != nil {
			return PlotID{}, err
		}
	}

	actualTimespan := prevHeader.Time - firstHeader.Time

	minTimespan := int64(RETARGET_TIME / 4)
	maxTimespan := int64(RETARGET_TIME * 4)

	if actualTimespan < minTimespan {
		actualTimespan = minTimespan
	}
	if actualTimespan > maxTimespan {
		actualTimespan = maxTimespan
	}

	actualTimespanInt := big.NewInt(actualTimespan)
	retargetTimeInt := big.NewInt(RETARGET_TIME)

	initialTargetBytes, err := hex.DecodeString(INITIAL_TARGET)
	if err != nil {
		return PlotID{}, err
	}

	maxTargetInt := new(big.Int).SetBytes(initialTargetBytes)
	prevTargetInt := new(big.Int).SetBytes(prevHeader.Target[:])
	newTargetInt := new(big.Int).Mul(prevTargetInt, actualTimespanInt)
	newTargetInt.Div(newTargetInt, retargetTimeInt)

	var target PlotID
	if newTargetInt.Cmp(maxTargetInt) > 0 {
		target.SetBigInt(maxTargetInt)
	} else {
		target.SetBigInt(newTargetInt)
	}

	return target, nil
}

// Revised target computation
func computeTargetBitcoinCash(prevHeader *PlotHeader, plotStore PlotStorage, ledger Ledger) (
	targetID PlotID, err error) {

	firstID, err := ledger.GetPlotIDForHeight(prevHeader.Height - RETARGET_SMA_WINDOW)
	if err != nil {
		return
	}
	firstHeader, _, err := plotStore.GetPlotHeader(*firstID)
	if err != nil {
		return
	}

	workInt := new(big.Int).Sub(prevHeader.ThreadWork.GetBigInt(), firstHeader.ThreadWork.GetBigInt())
	workInt.Mul(workInt, big.NewInt(TARGET_SPACING))

	// "In order to avoid difficulty cliffs, we bound the amplitude of the
	// adjustment we are going to do to a factor in [0.5, 2]." - Bitcoin-ABC
	actualTimespan := prevHeader.Time - firstHeader.Time
	if actualTimespan > 2*RETARGET_SMA_WINDOW*TARGET_SPACING {
		actualTimespan = 2 * RETARGET_SMA_WINDOW * TARGET_SPACING
	} else if actualTimespan < (RETARGET_SMA_WINDOW/2)*TARGET_SPACING {
		actualTimespan = (RETARGET_SMA_WINDOW / 2) * TARGET_SPACING
	}

	workInt.Div(workInt, big.NewInt(actualTimespan))

	// T = (2^256 / W) - 1
	maxInt := new(big.Int).Exp(big.NewInt(2), big.NewInt(256), nil)
	newTargetInt := new(big.Int).Div(maxInt, workInt)
	newTargetInt.Sub(newTargetInt, big.NewInt(1))

	// don't go above the initial target
	initialTargetBytes, err := hex.DecodeString(INITIAL_TARGET)
	if err != nil {
		return
	}
	maxTargetInt := new(big.Int).SetBytes(initialTargetBytes)
	if newTargetInt.Cmp(maxTargetInt) > 0 {
		targetID.SetBigInt(maxTargetInt)
	} else {
		targetID.SetBigInt(newTargetInt)
	}

	return
}

// Compute the median timestamp of the last NUM_PLOTS_FOR_MEDIAN_TIMESTAMP plots
func computeMedianTimestamp(prevHeader *PlotHeader, plotStore PlotStorage) (int64, error) {
	var timestamps []int64
	var err error
	for i := 0; i < NUM_PLOTS_FOR_MEDIAN_TMESTAMP; i++ {
		timestamps = append(timestamps, prevHeader.Time)
		prevHeader, _, err = plotStore.GetPlotHeader(prevHeader.Previous)
		if err != nil {
			return 0, err
		}
		if prevHeader == nil {
			break
		}
	}
	sort.Slice(timestamps, func(i, j int) bool {
		return timestamps[i] < timestamps[j]
	})
	return timestamps[len(timestamps)/2], nil
}

// Continue accepting the plot
func (p *Processor) acceptPlotContinue(
	id PlotID, plot *Plot, plotWhen int64, prevHeader *PlotHeader, source string) error {

	// get the current tip
	tipID, tipHeader, tipWhen, err := getThreadTipHeader(p.ledger, p.plotStore)
	if err != nil {
		return err
	}
	if id == *tipID {
		// can happen if we failed connecting a new plot
		return nil
	}

	// is this plot better than the current tip?
	if !plot.Header.Compare(tipHeader, plotWhen, tipWhen) {
		// flag this as a side branch plot
		log.Printf("Plot %s does not represent the tip of the best thread", id)
		return p.ledger.SetBranchType(id, SIDE)
	}

	// the new plot is the better thread
	tipAncestor := tipHeader
	newAncestor := prevHeader

	minHeight := tipAncestor.Height
	if newAncestor.Height < minHeight {
		minHeight = newAncestor.Height
	}

	var plotsToDisconnect, plotsToConnect []PlotID

	// walk back each thread to the common minHeight
	tipAncestorID := *tipID
	for tipAncestor.Height > minHeight {
		plotsToDisconnect = append(plotsToDisconnect, tipAncestorID)
		tipAncestorID = tipAncestor.Previous
		tipAncestor, _, err = p.plotStore.GetPlotHeader(tipAncestorID)
		if err != nil {
			return err
		}
	}

	newAncestorID := plot.Header.Previous
	for newAncestor.Height > minHeight {
		plotsToConnect = append([]PlotID{newAncestorID}, plotsToConnect...)
		newAncestorID = newAncestor.Previous
		newAncestor, _, err = p.plotStore.GetPlotHeader(newAncestorID)
		if err != nil {
			return err
		}
	}

	// scan both threads until we get to the common ancestor
	for *newAncestor != *tipAncestor {
		plotsToDisconnect = append(plotsToDisconnect, tipAncestorID)
		plotsToConnect = append([]PlotID{newAncestorID}, plotsToConnect...)
		tipAncestorID = tipAncestor.Previous
		tipAncestor, _, err = p.plotStore.GetPlotHeader(tipAncestorID)
		if err != nil {
			return err
		}
		newAncestorID = newAncestor.Previous
		newAncestor, _, err = p.plotStore.GetPlotHeader(newAncestorID)
		if err != nil {
			return err
		}
	}

	// we're at common ancestor. disconnect any main thread plots we need to
	for _, id := range plotsToDisconnect {
		plotToDisconnect, err := p.plotStore.GetPlot(id)
		if err != nil {
			return err
		}
		if err := p.disconnectPlot(id, plotToDisconnect, source); err != nil {
			return err
		}
	}

	// connect any new thread plots we need to
	for _, id := range plotsToConnect {
		plotToConnect, err := p.plotStore.GetPlot(id)
		if err != nil {
			return err
		}
		if err := p.connectPlot(id, plotToConnect, source, true); err != nil {
			return err
		}
	}

	// and finally connect the new plot
	return p.connectPlot(id, plot, source, false)
}

// Update the ledger and representation queue and notify undo tip channels
func (p *Processor) disconnectPlot(id PlotID, plot *Plot, source string) error {
	// Update the ledger
	txIDs, err := p.ledger.DisconnectPlot(id, plot)
	if err != nil {
		return err
	}

	log.Printf("Plot %s has been disconnected, height: %d\n", id, plot.Header.Height)

	// Add newly disconnected non-plotroot representations back to the queue
	if err := p.txQueue.AddBatch(txIDs[1:], plot.Representations[1:], plot.Header.Height-1); err != nil {
		return err
	}

	// Notify tip change channels
	for ch := range p.tipChangeChannels {
		ch <- TipChange{PlotID: id, Plot: plot, Source: source}
	}
	return nil
}

// Update the ledger and representation queue and notify new tip channels
func (p *Processor) connectPlot(id PlotID, plot *Plot, source string, more bool) error {
	// Update the ledger
	txIDs, err := p.ledger.ConnectPlot(id, plot)
	if err != nil {
		return err
	}

	log.Printf("Plot %s is the new tip, height: %d\n", id, plot.Header.Height)

	// Remove newly confirmed non-plotroot representations from the queue
	if err := p.txQueue.RemoveBatch(txIDs[1:], plot.Header.Height, more); err != nil {
		return err
	}

	// Notify tip change channels
	for ch := range p.tipChangeChannels {
		ch <- TipChange{PlotID: id, Plot: plot, Source: source, Connect: true, More: more}
	}
	return nil
}

// Try to reconnect the previous tip plot when acceptPlotContinue fails for the new plot
func (p *Processor) reconnectTip(id PlotID, source string) error {
	plot, err := p.plotStore.GetPlot(id)
	if err != nil {
		return err
	}
	if plot == nil {
		return fmt.Errorf("Plot %s not found", id)
	}
	_, when, err := p.plotStore.GetPlotHeader(id)
	if err != nil {
		return err
	}
	prevHeader, _, err := p.plotStore.GetPlotHeader(plot.Header.Previous)
	if err != nil {
		return err
	}
	return p.acceptPlotContinue(id, plot, when, prevHeader, source)
}

// Convenience method to get the current main thread's tip ID, header, and storage time.
func getThreadTipHeader(ledger Ledger, plotStore PlotStorage) (*PlotID, *PlotHeader, int64, error) {
	// get the current tip
	tipID, _, err := ledger.GetThreadTip()
	if err != nil {
		return nil, nil, 0, err
	}
	if tipID == nil {
		return nil, nil, 0, nil
	}

	// get the header
	tipHeader, tipWhen, err := plotStore.GetPlotHeader(*tipID)
	if err != nil {
		return nil, nil, 0, err
	}
	return tipID, tipHeader, tipWhen, nil
}
