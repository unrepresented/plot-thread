package main

import (
	"bufio"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/c-bata/go-prompt"
	"github.com/logrusorgru/aurora"
	. "github.com/unrepresented/plot-thread"
	"golang.org/x/crypto/ed25519"
	"golang.org/x/crypto/ssh/terminal"
)

// This is a lightweight keyholder client. It pretty much does the bare minimum at the moment so we can test the system
func main() {
	rand.Seed(time.Now().UnixNano())

	DefaultPeer := "127.0.0.1:" + strconv.Itoa(DEFAULT_PLOTTHREAD_PORT)
	peerPtr := flag.String("peer", DefaultPeer, "Address of a peer to connect to")
	dbPathPtr := flag.String("keyholderdb", "", "Path to a keyholder database (created if it doesn't exist)")
	tlsVerifyPtr := flag.Bool("tlsverify", false, "Verify the TLS certificate of the peer is signed by a recognized CA and the host matches the CN")
	recoverPtr := flag.Bool("recover", false, "Attempt to recover a corrupt keyholderdb")
	flag.Parse()

	if len(*dbPathPtr) == 0 {
		log.Fatal("Path to the keyholder database required")
	}
	if len(*peerPtr) == 0 {
		log.Fatal("Peer address required")
	}
	// add default port, if one was not supplied
	i := strings.LastIndex(*peerPtr, ":")
	if i < 0 {
		*peerPtr = *peerPtr + ":" + strconv.Itoa(DEFAULT_PLOTTHREAD_PORT)
	}

	// load genesis plot
	var genesisPlot Plot
	if err := json.Unmarshal([]byte(GenesisPlotJson), &genesisPlot); err != nil {
		log.Fatal(err)
	}
	genesisID, err := genesisPlot.ID()
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("Starting up...")
	fmt.Printf("Genesis plot ID: %s\n", genesisID)

	if *recoverPtr {
		fmt.Println("Attempting to recover keyholder...")
	}

	// instantiate keyholder
	keyholder, err := NewKeyholder(*dbPathPtr, *recoverPtr)
	if err != nil {
		log.Fatal(err)
	}

	for {
		// load keyholder passphrase
		passphrase := promptForPassphrase()
		ok, err := keyholder.SetPassphrase(passphrase)
		if err != nil {
			log.Fatal(err)
		}
		if ok {
			break
		}
		fmt.Println(aurora.Bold(aurora.Red("Passphrase is not the one used to encrypt your most recent key.")))
	}

	// connect the keyholder ondemand
	connectKeyholder := func() error {
		if keyholder.IsConnected() {
			return nil
		}
		if err := keyholder.Connect(*peerPtr, genesisID, *tlsVerifyPtr); err != nil {
			return err
		}
		go keyholder.Run()
		return keyholder.SetFilter()
	}

	var newTxs []*Representation
	var newConfs []*representationWithHeight
	var newTxsLock, newConfsLock, cmdLock sync.Mutex

	// handle new incoming representations
	keyholder.SetRepresentationCallback(func(tx *Representation) {
		ok, err := representationIsRelevant(keyholder, tx)
		if err != nil {
			fmt.Printf("Error: %s\n", err)
			return
		}
		if !ok {
			// false positive
			return
		}
		newTxsLock.Lock()
		showMessage := len(newTxs) == 0
		newTxs = append(newTxs, tx)
		newTxsLock.Unlock()
		if showMessage {
			go func() {
				// don't interrupt a user during a command
				cmdLock.Lock()
				defer cmdLock.Unlock()
				fmt.Printf("\n\nNew incoming representation! ")
				fmt.Printf("Type %s to view it.\n\n",
					aurora.Bold(aurora.Green("show")))
			}()
		}
	})

	// handle new incoming filter plots
	keyholder.SetFilterPlotCallback(func(fb *FilterPlotMessage) {
		for _, tx := range fb.Representations {
			ok, err := representationIsRelevant(keyholder, tx)
			if err != nil {
				fmt.Printf("Error: %s\n", err)
				continue
			}
			if !ok {
				// false positive
				continue
			}
			newConfsLock.Lock()
			showMessage := len(newConfs) == 0
			newConfs = append(newConfs, &representationWithHeight{tx: tx, height: fb.Header.Height})
			newConfsLock.Unlock()
			if showMessage {
				go func() {
					// don't interrupt a user during a command
					cmdLock.Lock()
					defer cmdLock.Unlock()
					fmt.Printf("\n\nNew representation confirmation! ")
					fmt.Printf("Type %s to view it.\n\n",
						aurora.Bold(aurora.Green("conf")))
				}()
			}
		}
	})

	// setup prompt
	completer := func(d prompt.Document) []prompt.Suggest {
		s := []prompt.Suggest{
			{Text: "newkey", Description: "Generate and store a new private key"},
			{Text: "listkeys", Description: "List all known public keys"},
			{Text: "genkeys", Description: "Generate multiple keys at once"},
			{Text: "dumpkeys", Description: "Dump all of the keyholder's public keys to a text file"},
			{Text: "imbalance", Description: "Retrieve the current imbalance of all public keys"},
			{Text: "ranking", Description: "Retrieve the current representivity ranking of all public keys"},
			{Text: "graph", Description: "Retrieve the DOT graph representation of all public keys"},
			{Text: "send", Description: "Send seeds to someone"},
			{Text: "show", Description: "Show new incoming representations"},
			{Text: "txstatus", Description: "Show confirmed representation information given a representation ID"},
			{Text: "clearnew", Description: "Clear all pending incoming representation notifications"},
			{Text: "conf", Description: "Show new representation confirmations"},
			{Text: "clearconf", Description: "Clear all pending representation confirmation notifications"},
			{Text: "rewards", Description: "Show immature plot rewards for all public keys"},
			{Text: "verify", Description: "Verify the private key is decryptable and intact for all public keys displayed with 'listkeys'"},
			{Text: "export", Description: "Save all of the keyholder's public-private key pairs to a text file"},
			{Text: "import", Description: "Import public-private key pairs from a text file"},
			{Text: "quit", Description: "Quit this keyholder session"},
		}
		return prompt.FilterHasPrefix(s, d.GetWordBeforeCursor(), true)
	}

	fmt.Println("Please select a command.")
	fmt.Printf("To connect to your keyholder peer you need to issue a command requiring it, e.g. %s\n",
		aurora.Bold(aurora.Green("imbalance")))
	for {
		// run interactive prompt
		cmd := prompt.Input("> ", completer)
		cmdLock.Lock()
		switch cmd {
		case "newkey":
			pubKeys, err := keyholder.NewKeys(1)
			if err != nil {
				fmt.Printf("Error: %s\n", err)
				break
			}
			fmt.Printf("New key generated, public key: %s\n",
				aurora.Bold(base64.StdEncoding.EncodeToString(pubKeys[0][:])))
			if keyholder.IsConnected() {
				// update our filter if online
				if err := keyholder.SetFilter(); err != nil {
					fmt.Printf("Error: %s\n", err)
				}
			}

		case "listkeys":
			pubKeys, err := keyholder.GetKeys()
			if err != nil {
				fmt.Printf("Error: %s\n", err)
				break
			}
			for i, pubKey := range pubKeys {
				fmt.Printf("%4d: %s\n",
					i+1, base64.StdEncoding.EncodeToString(pubKey[:]))
			}

		case "genkeys":
			count, err := promptForNumber("Count", 4, bufio.NewReader(os.Stdin))
			if err != nil {
				fmt.Printf("Error: %s\n", err)
				break
			}
			if count <= 0 {
				break
			}
			pubKeys, err := keyholder.NewKeys(count)
			if err != nil {
				fmt.Printf("Error: %s\n", err)
				break
			}
			fmt.Printf("Generated %d new keys\n", len(pubKeys))
			if keyholder.IsConnected() {
				// update our filter if online
				if err := keyholder.SetFilter(); err != nil {
					fmt.Printf("Error: %s\n", err)
				}
			}

		case "dumpkeys":
			pubKeys, err := keyholder.GetKeys()
			if err != nil {
				fmt.Printf("Error: %s\n", err)
				break
			}
			if len(pubKeys) == 0 {
				fmt.Printf("No public keys found\n")
				break
			}
			name := "keys.txt"
			f, err := os.Create(name)
			if err != nil {
				fmt.Printf("Error: %s\n", err)
				break
			}
			for _, pubKey := range pubKeys {
				key := fmt.Sprintf("%s\n", base64.StdEncoding.EncodeToString(pubKey[:]))
				f.WriteString(key)
			}
			f.Close()
			fmt.Printf("%d public keys saved to '%s'\n", len(pubKeys), aurora.Bold(name))
		
		case "graph":
			if err := connectKeyholder(); err != nil {
				fmt.Printf("Error: %s\n", err)
				break
			}
			pubKeys, err := keyholder.GetKeys()
			if err != nil {
				fmt.Printf("Error: %s\n", err)
				break
			}
			
			for i, pubKey := range pubKeys {
				graph, _, err := keyholder.GetGraph(pubKey)
				if err != nil {
					fmt.Printf("Error: %s\n", err)
					break
				}
								
				fmt.Printf("%4d: %s %s\n",
					i+1,
					base64.StdEncoding.EncodeToString(pubKey[:]),
					graph)
			}

		case "ranking":
			if err := connectKeyholder(); err != nil {
				fmt.Printf("Error: %s\n", err)
				break
			}
			pubKeys, err := keyholder.GetKeys()
			if err != nil {
				fmt.Printf("Error: %s\n", err)
				break
			}
			
			for i, pubKey := range pubKeys {
				ranking, _, err := keyholder.GetRanking(pubKey)
				if err != nil {
					fmt.Printf("Error: %s\n", err)
					break
				}
								
				fmt.Printf("%4d: %s %.4f\n",
					i+1,
					base64.StdEncoding.EncodeToString(pubKey[:]),
					ranking)
				
			}

		case "imbalance":
			if err := connectKeyholder(); err != nil {
				fmt.Printf("Error: %s\n", err)
				break
			}
			pubKeys, err := keyholder.GetKeys()
			if err != nil {
				fmt.Printf("Error: %s\n", err)
				break
			}
			var total int64
			for i, pubKey := range pubKeys {
				imbalance, _, err := keyholder.GetImbalance(pubKey)
				if err != nil {
					fmt.Printf("Error: %s\n", err)
					break
				}
				amount := imbalance
				fmt.Printf("%4d: %s %+d\n",
					i+1,
					base64.StdEncoding.EncodeToString(pubKey[:]),
					amount)
				total += imbalance
			}
			amount := total
			fmt.Printf("%s: %+d\n", aurora.Bold("Total"), amount)

		case "send":
			if err := connectKeyholder(); err != nil {
				fmt.Printf("Error: %s\n", err)
				break
			}
			id, err := sendRepresentation(keyholder)
			if err != nil {
				fmt.Printf("Error: %s\n", err)
				break
			}
			fmt.Printf("Representation %s sent\n", id)

		case "txstatus":
			if err := connectKeyholder(); err != nil {
				fmt.Printf("Error: %s\n", err)
				break
			}
			txID, err := promptForRepresentationID("ID", 2, bufio.NewReader(os.Stdin))
			if err != nil {
				fmt.Printf("Error: %s\n", err)
				break
			}
			fmt.Println("")
			tx, _, height, err := keyholder.GetRepresentation(txID)
			if err != nil {
				fmt.Printf("Error: %s\n", err)
				break
			}
			if tx == nil {
				fmt.Printf("Representation %s not found in the plotthread at this time.\n",
					txID)
				fmt.Println("It may be waiting for confirmation.")
				break
			}
			showRepresentation(keyholder, tx, height)

		case "show":
			if err := connectKeyholder(); err != nil {
				fmt.Printf("Error: %s\n", err)
				break
			}
			tx, left := func() (*Representation, int) {
				newTxsLock.Lock()
				defer newTxsLock.Unlock()
				if len(newTxs) == 0 {
					return nil, 0
				}
				tx := newTxs[0]
				newTxs = newTxs[1:]
				return tx, len(newTxs)
			}()
			if tx != nil {
				showRepresentation(keyholder, tx, 0)
				if left > 0 {
					fmt.Printf("\n%d new representation(s) left to display. Type %s to continue.\n",
						left, aurora.Bold(aurora.Green("show")))
				}
			} else {
				fmt.Printf("No new representations to display\n")
			}

		case "clearnew":
			func() {
				newTxsLock.Lock()
				defer newTxsLock.Unlock()
				newTxs = nil
			}()

		case "conf":
			if err := connectKeyholder(); err != nil {
				fmt.Printf("Error: %s\n", err)
				break
			}
			tx, left := func() (*representationWithHeight, int) {
				newConfsLock.Lock()
				defer newConfsLock.Unlock()
				if len(newConfs) == 0 {
					return nil, 0
				}
				tx := newConfs[0]
				newConfs = newConfs[1:]
				return tx, len(newConfs)
			}()
			if tx != nil {
				showRepresentation(keyholder, tx.tx, tx.height)
				if left > 0 {
					fmt.Printf("\n%d new confirmations(s) left to display. Type %s to continue.\n",
						left, aurora.Bold(aurora.Green("conf")))
				}
			} else {
				fmt.Printf("No new confirmations to display\n")
			}

		case "clearconf":
			func() {
				newConfsLock.Lock()
				defer newConfsLock.Unlock()
				newConfs = nil
			}()

		case "rewards":
			if err := connectKeyholder(); err != nil {
				fmt.Printf("Error: %s\n", err)
				break
			}
			pubKeys, err := keyholder.GetKeys()
			if err != nil {
				fmt.Printf("Error: %s\n", err)
				break
			}
			_, tipHeader, err := keyholder.GetTipHeader()
			if err != nil {
				fmt.Printf("Error: %s\n", err)
				break
			}
			var total int64
			lastHeight := tipHeader.Height - PLOTROOT_MATURITY
		gpkt:
			for i, pubKey := range pubKeys {
				var rewards, startHeight int64 = 0, lastHeight + 1
				var startIndex int = 0
				for {
					_, stopHeight, stopIndex, fbs, err := keyholder.GetPublicKeyRepresentations(
						pubKey, startHeight, tipHeader.Height+1, startIndex, 32)
					if err != nil {
						fmt.Printf("Error: %s\n", err)
						break gpkt
					}
					var numTx int
					startHeight, startIndex = stopHeight, stopIndex+1
					for _, fb := range fbs {
						for _, tx := range fb.Representations {
							numTx++
							if tx.IsPlotroot() {
								rewards += 1
							}
						}
					}
					if numTx < 32 {
						break
					}
				}
				amount := rewards
				fmt.Printf("%4d: %s %+d\n",
					i+1,
					base64.StdEncoding.EncodeToString(pubKey[:]),
					amount)
				total += rewards
			}
			amount := total
			fmt.Printf("%s: %+d\n", aurora.Bold("Total"), amount)

		case "verify":
			pubKeys, err := keyholder.GetKeys()
			if err != nil {
				fmt.Printf("Error: %s\n", err)
				break
			}
			var verified, corrupt int
			for i, pubKey := range pubKeys {
				if err := keyholder.VerifyKey(pubKey); err != nil {
					corrupt++
					fmt.Printf("%4d: %s %s\n",
						i+1, base64.StdEncoding.EncodeToString(pubKey[:]),
						aurora.Bold(aurora.Red(err.Error())))
				} else {
					verified++
					fmt.Printf("%4d: %s %s\n",
						i+1, base64.StdEncoding.EncodeToString(pubKey[:]),
						aurora.Bold(aurora.Green("Verified")))
				}
			}
			fmt.Printf("%d key(s) verified and %d key(s) potentially corrupt\n",
				verified, corrupt)

		case "export":
			fmt.Println(aurora.BrightRed("WARNING"), aurora.Bold(": Anyone with access to a keyholder's "+
				"private key(s) has full control of the funds in the keyholder."))
			confirm, err := promptForConfirmation("Are you sure you wish to proceed?", false,
				bufio.NewReader(os.Stdin))
			if err != nil {
				fmt.Printf("Error: %s\n", err)
				break
			}
			if !confirm {
				fmt.Println("Aborting export")
				break
			}
			pubKeys, err := keyholder.GetKeys()
			if err != nil {
				fmt.Printf("Error: %s\n", err)
				break
			}
			if len(pubKeys) == 0 {
				fmt.Printf("No private keys found\n")
				break
			}
			filename, err := promptForString("Filename", "export.txt", bufio.NewReader(os.Stdin))
			if err != nil {
				fmt.Printf("Error: #{err}\n")
				break
			}
			f, err := os.Create(filename)
			if err != nil {
				fmt.Printf("Error: %s\n", err)
				break
			}
			count := 0
			for _, pubKey := range pubKeys {
				private, err := keyholder.GetPrivateKey(pubKey)
				if err != nil {
					fmt.Printf("Couldn't get private key for public key: %s; omitting from export\n", pubKey)
					continue
				}
				pair := fmt.Sprintf("%s,%s\n",
					base64.StdEncoding.EncodeToString(pubKey[:]),
					base64.StdEncoding.EncodeToString(private[:]))
				f.WriteString(pair)
				count++
			}
			f.Close()
			fmt.Printf("%d keyholder key pairs saved to '%s'\n", count, aurora.Bold(filename))

		case "import":
			fmt.Println("Files should have one address per line, in the format: ",
				aurora.Bold("PUBLIC_KEY,PRIVATE_KEY"))
			fmt.Println("Files generated by the ", aurora.Bold("export"), " command are "+
				"automatically formatted in this way.")
			filename, err := promptForString("Filename", "export.txt", bufio.NewReader(os.Stdin))
			if err != nil {
				fmt.Printf("Error: #{err}\n")
				break
			}
			file, err := os.Open(filename)
			if err != nil {
				fmt.Printf("Error opening file: #{err}\n")
				break
			}
			var skipped = 0
			var pubKeys []ed25519.PublicKey
			scanner := bufio.NewScanner(file)
			for scanner.Scan() {
				key := strings.Split(scanner.Text(), ",")
				if len(key) != 2 {
					fmt.Println("Error found: incorrectly formatted line")
					skipped++
					continue
				}
				pubKeyBytes, err := base64.StdEncoding.DecodeString(key[0])
				if err != nil {
					fmt.Println("Error with public key:", err)
					skipped++
					continue
				}
				pubKey := ed25519.PublicKey(pubKeyBytes)
				privKeyBytes, err := base64.StdEncoding.DecodeString(key[1])
				if err != nil {
					fmt.Println("Error with private key:", err)
					skipped++
					continue
				}
				privKey := ed25519.PrivateKey(privKeyBytes)
				// add key to database
				if err := keyholder.AddKey(pubKey, privKey); err != nil {
					fmt.Println("Error adding key pair to database:", err)
					skipped++
					continue
				}
				pubKeys = append(pubKeys, pubKey)
			}
			for i, pubKey := range pubKeys {
				fmt.Printf("%4d: %s\n", i+1, base64.StdEncoding.EncodeToString(pubKey[:]))
			}
			fmt.Printf("Successfully added %d key(s); %d line(s) skipped.\n", len(pubKeys), skipped)

		case "quit":
			keyholder.Shutdown()
			return
		}

		fmt.Println("")
		cmdLock.Unlock()
	}
}

// Prompt for representation details and request the keyholder to send it
func sendRepresentation(keyholder *Keyholder) (RepresentationID, error) {

	reader := bufio.NewReader(os.Stdin)

	// prompt for from
	from, err := promptForPublicKey("From", 6, reader)
	if err != nil {
		return RepresentationID{}, err
	}

	// prompt for to
	to, err := promptForPublicKey("To", 6, reader)
	if err != nil {
		return RepresentationID{}, err
	}

	// prompt for memo
	fmt.Printf("%6v: ", aurora.Bold("Memo"))
	text, err := reader.ReadString('\n')
	if err != nil {
		return RepresentationID{}, err
	}
	memo := strings.TrimSpace(text)
	if len(memo) > MAX_MEMO_LENGTH {
		return RepresentationID{}, fmt.Errorf("Maximum memo length (%d) exceeded (%d)",
			MAX_MEMO_LENGTH, len(memo))
	}

	// create and send send it. by default the representation expires if not scribed within 3 plots from now
	id, err := keyholder.Send(from, to, 0, 3, memo)
	if err != nil {
		return RepresentationID{}, err
	}
	return id, nil
}

func promptForPublicKey(prompt string, rightJustify int, reader *bufio.Reader) (ed25519.PublicKey, error) {
	fmt.Printf("%"+strconv.Itoa(rightJustify)+"v: ", aurora.Bold(prompt))
	text, err := reader.ReadString('\n')
	if err != nil {
		return nil, err
	}
	text = strings.TrimSpace(text)
	pubKeyBytes, err := base64.StdEncoding.DecodeString(text)
	if err != nil {
		return nil, err
	}
	if len(pubKeyBytes) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("Invalid public key")
	}
	return ed25519.PublicKey(pubKeyBytes), nil
}

func promptForNumber(prompt string, rightJustify int, reader *bufio.Reader) (int, error) {
	fmt.Printf("%"+strconv.Itoa(rightJustify)+"v: ", aurora.Bold(prompt))
	text, err := reader.ReadString('\n')
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(text))
}

func promptForConfirmation(prompt string, defaultResponse bool, reader *bufio.Reader) (bool, error) {
	defaultPrompt := " [y/N]"
	if defaultResponse {
		defaultPrompt = " [Y/n]"
	}
	fmt.Printf("%v: ", aurora.Bold(prompt+defaultPrompt))
	text, err := reader.ReadString('\n')
	if err != nil {
		return false, err
	}
	text = strings.ToLower(strings.TrimSpace(text))
	switch text {
	case "y", "yes":
		return true, nil
	case "n", "no":
		return false, nil
	}
	return defaultResponse, nil
}

func promptForString(prompt, defaultResponse string, reader *bufio.Reader) (string, error) {
	if defaultResponse != "" {
		prompt = prompt + " [" + defaultResponse + "]"
	}
	fmt.Printf("%v: ", aurora.Bold(prompt))
	response, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	response = strings.TrimSpace(response)
	if response == "" {
		return defaultResponse, nil
	}
	return response, nil
}

func promptForRepresentationID(prompt string, rightJustify int, reader *bufio.Reader) (RepresentationID, error) {
	fmt.Printf("%"+strconv.Itoa(rightJustify)+"v: ", aurora.Bold(prompt))
	text, err := reader.ReadString('\n')
	if err != nil {
		return RepresentationID{}, err
	}
	text = strings.TrimSpace(text)
	if len(text) != 2*(len(RepresentationID{})) {
		return RepresentationID{}, fmt.Errorf("Invalid representation ID")
	}
	idBytes, err := hex.DecodeString(text)
	if err != nil {
		return RepresentationID{}, err
	}
	if len(idBytes) != len(RepresentationID{}) {
		return RepresentationID{}, fmt.Errorf("Invalid representation ID")
	}
	var id RepresentationID
	copy(id[:], idBytes)
	return id, nil
}

func showRepresentation(w *Keyholder, tx *Representation, height int64) {
	when := time.Unix(tx.Time, 0)
	id, _ := tx.ID()
	fmt.Printf("%7v: %s\n", aurora.Bold("ID"), id)
	fmt.Printf("%7v: %d\n", aurora.Bold("Series"), tx.Series)
	fmt.Printf("%7v: %s\n", aurora.Bold("Time"), when)
	if tx.From != nil {
		fmt.Printf("%7v: %s\n", aurora.Bold("From"), base64.StdEncoding.EncodeToString(tx.From))
	}
	fmt.Printf("%7v: %s\n", aurora.Bold("To"), base64.StdEncoding.EncodeToString(tx.To))
	if len(tx.Memo) > 0 {
		fmt.Printf("%7v: %s\n", aurora.Bold("Memo"), tx.Memo)
	}

	_, header, _ := w.GetTipHeader()
	if height <= 0 {
		if tx.Matures > 0 {
			fmt.Printf("%7v: cannot be scribed until height: %d, current height: %d\n",
				aurora.Bold("Matures"), tx.Matures, header.Height)
		}
		if tx.Expires > 0 {
			fmt.Printf("%7v: cannot be scribed after height: %d, current height: %d\n",
				aurora.Bold("Expires"), tx.Expires, header.Height)
		}
		return
	}

	fmt.Printf("%7v: confirmed at height %d, %d confirmation(s)\n",
		aurora.Bold("Status"), height, (header.Height-height)+1)
}

// Catch filter false-positives
func representationIsRelevant(keyholder *Keyholder, tx *Representation) (bool, error) {
	pubKeys, err := keyholder.GetKeys()
	if err != nil {
		return false, err
	}
	for _, pubKey := range pubKeys {
		if tx.Contains(pubKey) {
			return true, nil
		}
	}
	return false, nil
}

// secure passphrase prompt helper
func promptForPassphrase() string {
	var passphrase string
	for {
		q := "Enter"
		if len(passphrase) != 0 {
			q = "Confirm"
		}
		fmt.Printf("\n%s passphrase: ", q)
		ppBytes, err := terminal.ReadPassword(int(syscall.Stdin))
		if err != nil {
			log.Fatal(err)
		}
		if len(passphrase) != 0 {
			if passphrase != string(ppBytes) {
				passphrase = ""
				fmt.Printf("\nPassphrase mismatch\n")
				continue
			}
			break
		}
		passphrase = string(ppBytes)
	}
	fmt.Printf("\n\n")
	return passphrase
}

type representationWithHeight struct {
	tx     *Representation
	height int64
}

// From: https://groups.google.com/forum/#!topic/golang-nuts/ITZV08gAugI
func roundFloat(x float64, prec int) float64 {
	var rounder float64
	pow := math.Pow(10, float64(prec))
	intermed := x * pow
	_, frac := math.Modf(intermed)
	intermed += .5
	x = .5
	if frac < 0.0 {
		x = -.5
		intermed -= 1
	}
	if frac >= x {
		rounder = math.Ceil(intermed)
	} else {
		rounder = math.Floor(intermed)
	}

	return rounder / pow
}
