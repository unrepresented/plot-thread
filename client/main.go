package main

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"math/rand"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	. "github.com/unrepresented/plot-thread"
	"golang.org/x/crypto/ed25519"
)

// A peer node in the plotthread network
func main() {
	rand.Seed(time.Now().UnixNano())

	// flags
	pubKeyPtr := flag.String("pubkey", "", "A public key which receives newly scribed plot rewards")
	dataDirPtr := flag.String("datadir", "", "Path to a directory to save plot thread data")
	memoPtr := flag.String("memo", "", "A memo to include in newly scribed plots")
	portPtr := flag.Int("port", DEFAULT_PLOTTHREAD_PORT, "Port to listen for incoming peer connections")
	peerPtr := flag.String("peer", "", "Address of a peer to connect to")
	upnpPtr := flag.Bool("upnp", false, "Attempt to forward the plotthread port on your router with UPnP")
	dnsSeedPtr := flag.Bool("dnsseed", false, "Run a DNS server to allow others to find peers")
	compressPtr := flag.Bool("compress", false, "Compress plots on disk with lz4")
	numScribersPtr := flag.Int("numscribers", 1, "Number of scribers to run")
	noIrcPtr := flag.Bool("noirc", true, "Disable use of IRC for peer discovery")
	noAcceptPtr := flag.Bool("noaccept", false, "Disable inbound peer connections")
	prunePtr := flag.Bool("prune", false, "Prune representation and public key representation indices")
	keyFilePtr := flag.String("keyfile", "", "Path to a file containing public keys to use when scribing")
	genPlotPtr := flag.String("genplot", "", "Path to a json file containing the genesis plot for the thread")
	tlsCertPtr := flag.String("tlscert", "", "Path to a file containing a PEM-encoded X.509 certificate to use with TLS")
	tlsKeyPtr := flag.String("tlskey", "", "Path to a file containing a PEM-encoded private key to use with TLS")
	inLimitPtr := flag.Int("inlimit", MAX_INBOUND_PEER_CONNECTIONS, "Limit for the number of inbound peer connections.")
	banListPtr := flag.String("banlist", "", "Path to a file containing a list of banned host addresses")
	flag.Parse()

	if len(*genPlotPtr) == 0 {
		log.Fatal("-genplot argument required")
	}

	if len(*dataDirPtr) == 0 {
		log.Fatal("-datadir argument required")
	}
	if len(*tlsCertPtr) != 0 && len(*tlsKeyPtr) == 0 {
		log.Fatal("-tlskey argument missing")
	}
	if len(*tlsCertPtr) == 0 && len(*tlsKeyPtr) != 0 {
		log.Fatal("-tlscert argument missing")
	}

	if len(*peerPtr) != 0 {
		// add default port, if one was not supplied
		if i := strings.LastIndex(*peerPtr, ":"); i < 0 {
			*peerPtr = *peerPtr + ":" + strconv.Itoa(DEFAULT_PLOTTHREAD_PORT)
		}
	}

	// load any ban list
	banMap := make(map[string]bool)
	if len(*banListPtr) != 0 {
		var err error
		banMap, err = loadBanList(*banListPtr)
		if err != nil {
			log.Fatal(err)
		}
	}

	// load public keys to scribe to
	var pubKeys []ed25519.PublicKey
	if *numScribersPtr > 0 {
		if len(*pubKeyPtr) == 0 && len(*keyFilePtr) == 0 {
			log.Fatal("-pubkey or -keyfile argument required to receive newly scribed plot rewards")
		}
		if len(*pubKeyPtr) != 0 && len(*keyFilePtr) != 0 {
			log.Fatal("Specify only one of -pubkey or -keyfile but not both")
		}
		var err error
		pubKeys, err = loadPublicKeys(*pubKeyPtr, *keyFilePtr)
		if err != nil {
			log.Fatal(err)
		}
	}

	// load genesis plot
	file, err := os.Open(*genPlotPtr)
    if err != nil {
        log.Fatal(err)
    }
    defer file.Close()

    // Read the file's content
    content, err := io.ReadAll(file)
    if err != nil {
        log.Fatal(err)
    }

    // Convert the content to a string
    jsonString := string(content)


	genesisPlot := new(Plot)
	if err := json.Unmarshal([]byte(jsonString), genesisPlot); err != nil {
		log.Fatal(err)
	}

	genesisID, err := genesisPlot.ID()
	if err != nil {
		log.Fatal(err)
	}

	log.Println("Starting up...")
	log.Printf("Genesis plot ID: %s\n", genesisID)

	// instantiate the representation graph
	repGraph := NewGraph()

	// instantiate storage
	plotStore, err := NewPlotStorageDisk(
		filepath.Join(*dataDirPtr, "plots"),
		filepath.Join(*dataDirPtr, "headers.db"),
		false, // not read-only
		*compressPtr,
	)
	if err != nil {
		log.Fatal(err)
	}

	// instantiate the ledger
	ledger, err := NewLedgerDisk(filepath.Join(*dataDirPtr, "ledger.db"),
		false, // not read-only
		*prunePtr,
		plotStore,
		repGraph)

	if err != nil {
		plotStore.Close()
		log.Fatal(err)
	}

	// instantiate peer storage
	peerStore, err := NewPeerStorageDisk(filepath.Join(*dataDirPtr, "peers.db"))
	if err != nil {
		ledger.Close()
		plotStore.Close()
		log.Fatal(err)
	}

	// instantiate the representation queue
	txQueue := NewRepresentationQueueMemory(ledger, repGraph)

	// create and run the processor
	processor := NewProcessor(genesisID, plotStore, txQueue, ledger)
	processor.Run()

	// process the genesis plot
	if err := processor.ProcessPlot(genesisID, genesisPlot, ""); err != nil {
		processor.Shutdown()
		peerStore.Close()
		ledger.Close()
		plotStore.Close()
		log.Fatal(err)
	}

	indexer := NewIndexer(repGraph, plotStore, ledger, processor, genesisID)
	indexer.Run()

	var scribers []*Scriber
	var hashrateMonitor *HashrateMonitor
	if *numScribersPtr > 0 {
		hashUpdateChan := make(chan int64, *numScribersPtr)
		// create and run scribers
		for i := 0; i < *numScribersPtr; i++ {
			scriber := NewScriber(pubKeys, *memoPtr, plotStore, txQueue, ledger, processor, hashUpdateChan, i)
			scribers = append(scribers, scriber)
			scriber.Run()
		}
		// print hashrate updates
		hashrateMonitor = NewHashrateMonitor(hashUpdateChan)
		hashrateMonitor.Run()
	} else {
		log.Println("Scribing is currently disabled")
	}

	// start a dns server
	var seeder *DNSSeeder
	if *dnsSeedPtr {
		seeder = NewDNSSeeder(peerStore, *portPtr)
		seeder.Run()
	}

	// enable port forwarding (accept must also be enabled)
	var myExternalIP string
	if *upnpPtr == true && *noAcceptPtr == false {
		log.Printf("Enabling forwarding for port %d...\n", *portPtr)
		var ok bool
		var err error
		if myExternalIP, ok, err = HandlePortForward(uint16(*portPtr), true); err != nil || !ok {
			log.Printf("Failed to enable forwarding: %s\n", err)
		} else {
			log.Println("Successfully enabled forwarding")
		}
	}

	// manage peer connections
	peerManager := NewPeerManager(genesisID, peerStore, plotStore, ledger, processor, indexer, txQueue,
		*dataDirPtr, myExternalIP, *peerPtr, *tlsCertPtr, *tlsKeyPtr,
		*portPtr, *inLimitPtr, !*noAcceptPtr, !*noIrcPtr, *dnsSeedPtr, banMap)
	peerManager.Run()

	// shutdown on ctrl-c
	c := make(chan os.Signal, 1)
	done := make(chan struct{})
	signal.Notify(c, os.Interrupt)

	go func() {
		defer close(done)
		<-c

		log.Println("Shutting down...")

		if len(myExternalIP) != 0 {
			// disable port forwarding
			log.Printf("Disabling forwarding for port %d...", *portPtr)
			if _, ok, err := HandlePortForward(uint16(*portPtr), false); err != nil || !ok {
				log.Printf("Failed to disable forwarding: %s", err)
			} else {
				log.Println("Successfully disabled forwarding")
			}
		}

		// shut everything down now
		peerManager.Shutdown()
		if seeder != nil {
			seeder.Shutdown()
		}
		for _, scriber := range scribers {
			scriber.Shutdown()
		}
		if hashrateMonitor != nil {
			hashrateMonitor.Shutdown()
		}
		
		indexer.Shutdown()
		processor.Shutdown()

		// close storage
		if err := peerStore.Close(); err != nil {
			log.Println(err)
		}
		if err := ledger.Close(); err != nil {
			log.Println(err)
		}
		if err := plotStore.Close(); err != nil {
			log.Println(err)
		}
	}()

	log.Println("Client started")
	<-done
	log.Println("Exiting")
}

func loadPublicKeys(pubKeyEncoded, keyFile string) ([]ed25519.PublicKey, error) {
	var pubKeysEncoded []string
	var pubKeys []ed25519.PublicKey

	if len(pubKeyEncoded) != 0 {
		pubKeysEncoded = append(pubKeysEncoded, pubKeyEncoded)
	} else {
		file, err := os.Open(keyFile)
		if err != nil {
			return nil, err
		}
		defer file.Close()
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			pubKeysEncoded = append(pubKeysEncoded, scanner.Text())
		}
		if err := scanner.Err(); err != nil {
			return nil, err
		}
		if len(pubKeysEncoded) == 0 {
			return nil, fmt.Errorf("No public keys found in '%s'", keyFile)
		}
	}

	for _, pubKeyEncoded = range pubKeysEncoded {
		pubKeyBytes, err := base64.StdEncoding.DecodeString(pubKeyEncoded)
		if len(pubKeyBytes) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("Invalid public key: %s\n", pubKeyEncoded)
		}
		if err != nil {
			return nil, err
		}
		pubKeys = append(pubKeys, ed25519.PublicKey(pubKeyBytes))
	}
	return pubKeys, nil
}

func loadBanList(banListFile string) (map[string]bool, error) {
	file, err := os.Open(banListFile)
	if err != nil {
		return nil, err
	}
	banMap := make(map[string]bool)
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		banMap[strings.TrimSpace(scanner.Text())] = true
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return banMap, nil
}
