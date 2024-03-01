package plotthread

import (
	"encoding/base64"
	"fmt"
	"log"
	"math"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ed25519"
)

type Indexer struct {
	plotStore       PlotStorage
	ledger           Ledger
	processor        *Processor
	latestPlotID 	 PlotID
	latestHeight     int64
	handle	         map[string]string
	bio		         map[string]string
	online		 	 map[string]string
	txGraph          *Graph
	shutdownChan     chan struct{}
	wg               sync.WaitGroup
}

func NewIndexer(
	plotStore PlotStorage,
	ledger Ledger,
	processor *Processor,
	genesisPlotID PlotID,
) *Indexer {
	return &Indexer{
		plotStore:        plotStore,
		ledger:           ledger,
		processor:        processor,
		latestPlotID:     genesisPlotID,
		latestHeight:     0,
		handle:           make(map[string]string),
		bio:     	      make(map[string]string),
		online:		      make(map[string]string),
		txGraph:          NewGraph(),
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
			idx.indexRepresentations(tip.Plot, tip.PlotID, tip.Connect)
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
	return base64.StdEncoding.EncodeToString(ppk[:])
}

func truncateString(s string, length int) string {
	if length > len(s) {
		return s
	}
	return s[:length]
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

	// Pad the input with leading zeros
	paddedString := input + strings.Repeat("0", padLength) + "="

	return paddedString
}

func (idx *Indexer) rankGraph(){
	log.Printf("Indexer commencing ranking at height: %d\n", idx.latestHeight)
	idx.txGraph.Rank(1.0, 1e-6)
	log.Printf("Ranking finished")
}

func (idx *Indexer) indexRepresentations(plot *Plot, id PlotID, increment bool) {
	idx.latestPlotID = id
	idx.latestHeight = plot.Header.Height

	for i := 0; i < len(plot.Representations); i++ {
		rep := plot.Representations[i]

		repTo := pubKeyToString(rep.To)
		repFrom := pubKeyToString(rep.From)
		
		//index username/handles
		if repTo == padToBase64Length("//handle") {
			idx.handle[repFrom] = truncateString(rep.Memo, 10)
		}else {
			index := strings.Index(repTo, "/handle")
			if index != -1 {
				beforeSubstring := repTo[:index]
				idx.handle[padToBase64Length(beforeSubstring)] = truncateString(rep.Memo, 15)
			}
		}		

		//index bios
		if repTo == padToBase64Length("//bio") {
			idx.bio[repFrom] = strings.TrimSpace(rep.Memo)
		}else {
			index := strings.Index(repTo, "/bio")
			if index != -1 {
				beforeSubstring := repTo[:index]
				idx.bio[padToBase64Length(beforeSubstring)] = strings.TrimSpace(rep.Memo)
			}
		}

		//index online prescence (TODO: validate url)
		if repTo == padToBase64Length("//online") {
			idx.online[repFrom] = strings.TrimSpace(rep.Memo)
		}else {
			indexOn := strings.Index(repTo, "/online")
			if indexOn != -1 {
				beforeSubstring := repTo[:indexOn]
				idx.online[padToBase64Length(beforeSubstring)] = strings.TrimSpace(rep.Memo)
			}
		}
		
		if increment {
			idx.txGraph.Link(repFrom, repTo, 1)
		} else {
			idx.txGraph.Link(repFrom, repTo, -1)
		}
	}
}

// Shutdown stops the indexer synchronously.
func (idx *Indexer) Shutdown() {
	close(idx.shutdownChan)
	idx.wg.Wait()
	log.Printf("Indexer shutdown\n")
}

type node struct {
	pubkey    string
	ranking  float64
	outbound float64
}

// Graph holds node and edge data.
type Graph struct {
	index map[string]uint32
	nodes map[uint32]*node
	edges map[uint32](map[uint32]float64)
}

// NewGraph initializes and returns a new graph.
func NewGraph() *Graph {
	return &Graph{
		edges: make(map[uint32](map[uint32]float64)),
		nodes: make(map[uint32]*node),
		index: make(map[string]uint32),
	}
}

//TODO: Enforce Directed Acyclic Graph with exception for root node.
// Link creates a weighted edge between a source-target node pair.
// If the edge already exists, the weight is incremented.
func (graph *Graph) Link(source, target string, weight float64) {
	if _, ok := graph.index[source]; !ok {
		index := uint32(len(graph.index))
		graph.index[source] = index
		graph.nodes[index] = &node{
			ranking:     0,
			outbound: 0,
			pubkey:    source,
		}
	}

	if _, ok := graph.index[target]; !ok {
		index := uint32(len(graph.index))
		graph.index[target] = index
		graph.nodes[index] = &node{
			ranking:     0,
			outbound: 0,
			pubkey:    target,
		}
	}

	sIndex := graph.index[source]
	tIndex := graph.index[target]

	if _, ok := graph.edges[sIndex]; !ok {
		graph.edges[sIndex] = map[uint32]float64{}
	}

	graph.nodes[sIndex].outbound += weight
	graph.edges[sIndex][tIndex] += weight
}

func (g *Graph) ToDOT(pubKey string, shortHandles map[string]string) string {
	

	pkInt, ok := g.index[pubKey]	
	if !ok {
		defaultPKInt, defaultPKOk := g.index["AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="]
		if defaultPKOk {
			pkInt = defaultPKInt
			ok = defaultPKOk
		}		
	}

	includedNodes := []uint32 {}	

	if ok {
		includedNodes = append(includedNodes, pkInt)
	}

	excludedNodes := []uint32 {}

	for pubkey, id := range g.index {

		indexSH := strings.Index(pubkey, "/handle")
		indexMB := strings.Index(pubkey, "/bio")
		indexOn := strings.Index(pubkey, "/online")
		indexH := strings.Index(pubkey, "/harvest")
		
		if indexSH != -1 || indexMB != -1 || indexH != -1 || indexOn != -1 {
			excludedNodes = append(excludedNodes, id)
		}		 
	}


	var builder strings.Builder
	builder.WriteString("digraph G {\n")

	for from, edge := range g.edges {
		for to, weight := range edge {
			if contains(excludedNodes, from) || contains(excludedNodes, to){
				continue
			}
			if ok && (from == pkInt || to == pkInt){				
				builder.WriteString(fmt.Sprintf("  \"%d\" -> \"%d\" [weight=\"%.f\"];\n", from, to, weight))
				if from == pkInt{
					includedNodes = append(includedNodes, to)
				}else{
					includedNodes = append(includedNodes, from)
				}
			}			
		}
	}	
	// Add nodes with ranks
	for _, id := range includedNodes {	
		node := g.nodes[id]
		shortHandle, ok := shortHandles[node.pubkey]
		if !ok {
			shortHandle = ""
		}	
		builder.WriteString(fmt.Sprintf("  \"%d\" [pubkey=\"%s\", label=\"%s\", ranking=\"%f\"];\n", id, node.pubkey, shortHandle, node.ranking))		
	}

	builder.WriteString("}\n")
	return builder.String()
}

func contains(slice []uint32, value uint32) bool {
    for _, v := range slice {
        if v == value {
            return true
        }
    }
    return false
}

//Checks for relationship to prevent cycles.
func (g *Graph) IsParentDescendant(parent, descendant string) bool {
	parentIndex := g.index[parent]
	descendantIndex := g.index[descendant]

	visited := make(map[uint32]bool)
	return g.dfs(parentIndex, descendantIndex, visited)
}

func (g *Graph) dfs(current, target uint32, visited map[uint32]bool) bool {
	if current == target {
		return true
	}

	visited[current] = true

	for edge := range g.edges[current] {
		if !visited[edge] {
			if g.dfs(edge, target, visited) {
				return true
			}
		}
	}

	return false
}


// https://github.com/alixaxel/pagerank/blob/master/pagerank.go
// Rank computes the RepresentivityRank of every node in the directed graph.
// α (alpha) is the damping factor, usually set to 0.85.
// ε (epsilon) is the convergence criteria, usually set to a tiny value.
//
// This method will run as many iterations as needed, until the graph converges.
func (graph *Graph) Rank(alpha, epsilon float64) {

	normalizedWeights := make(map[uint32](map[uint32]float64))

	Δ := float64(1.0)
	inverse := 1 / float64(len(graph.nodes))

	// Normalize all the edge weights so that their sum amounts to 1.
	for source := range graph.edges {
		if graph.nodes[source].outbound > 0 {
			normalizedWeights[source] = make(map[uint32]float64)
			for target := range graph.edges[source] {
				normalizedWeights[source][target] = graph.edges[source][target] / graph.nodes[source].outbound
			}
		}
	}

	for key := range graph.nodes {
		graph.nodes[key].ranking = inverse
	}

	for Δ > epsilon {
		leak := float64(0)
		nodes := map[uint32]float64{}

		for key, value := range graph.nodes {
			nodes[key] = value.ranking

			if value.outbound == 0 {
				leak += value.ranking
			}

			graph.nodes[key].ranking = 0
		}

		leak *= alpha

		for source := range graph.nodes {
			for target, weight := range normalizedWeights[source] {
				graph.nodes[target].ranking += alpha * nodes[source] * weight
			}

			graph.nodes[source].ranking += (1-alpha)*inverse + leak*inverse
		}

		Δ = 0

		for key, value := range graph.nodes {
			Δ += math.Abs(value.ranking - nodes[key])
		}
	}
}

// Reset clears all the current graph data.
func (graph *Graph) Reset() {
	graph.edges = make(map[uint32](map[uint32]float64))
	graph.nodes = make(map[uint32]*node)
	graph.index = make(map[string]uint32)
}

