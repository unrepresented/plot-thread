package plotthread

import (
	"bytes"
	"encoding/binary"
	"fmt"

	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/opt"
	"github.com/syndtr/goleveldb/leveldb/util"
	"golang.org/x/crypto/ed25519"
)

// LedgerDisk is an on-disk implemenation of the Ledger interface using LevelDB.
type LedgerDisk struct {
	db         *leveldb.DB
	plotStore PlotStorage
	prune      bool // prune historic representation and public key representation indices
}

// NewLedgerDisk returns a new instance of LedgerDisk.
func NewLedgerDisk(dbPath string, readOnly, prune bool, plotStore PlotStorage) (*LedgerDisk, error) {
	opts := opt.Options{ReadOnly: readOnly}
	db, err := leveldb.OpenFile(dbPath, &opts)
	if err != nil {
		return nil, err
	}
	return &LedgerDisk{db: db, plotStore: plotStore, prune: prune}, nil
}

// GetThreadTip returns the ID and the height of the plot at the current tip of the main thread.
func (l LedgerDisk) GetThreadTip() (*PlotID, int64, error) {
	return getThreadTip(l.db)
}

// Sometimes we call this with *leveldb.DB or *leveldb.Snapshot
func getThreadTip(db leveldb.Reader) (*PlotID, int64, error) {
	// compute db key
	key, err := computeThreadTipKey()
	if err != nil {
		return nil, 0, err
	}

	// fetch the id
	ctBytes, err := db.Get(key, nil)
	if err == leveldb.ErrNotFound {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}

	// decode the tip
	id, height, err := decodeThreadTip(ctBytes)
	if err != nil {
		return nil, 0, err
	}

	return id, height, nil
}

// GetPlotIDForHeight returns the ID of the plot at the given plot thread height.
func (l LedgerDisk) GetPlotIDForHeight(height int64) (*PlotID, error) {
	return getPlotIDForHeight(height, l.db)
}

// Sometimes we call this with *leveldb.DB or *leveldb.Snapshot
func getPlotIDForHeight(height int64, db leveldb.Reader) (*PlotID, error) {
	// compute db key
	key, err := computePlotHeightIndexKey(height)
	if err != nil {
		return nil, err
	}

	// fetch the id
	idBytes, err := db.Get(key, nil)
	if err == leveldb.ErrNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	// return it
	id := new(PlotID)
	copy(id[:], idBytes)
	return id, nil
}

// SetBranchType sets the branch type for the given plot.
func (l LedgerDisk) SetBranchType(id PlotID, branchType BranchType) error {
	// compute db key
	key, err := computeBranchTypeKey(id)
	if err != nil {
		return err
	}

	// write type
	wo := opt.WriteOptions{Sync: true}
	return l.db.Put(key, []byte{byte(branchType)}, &wo)
}

// GetBranchType returns the branch type for the given plot.
func (l LedgerDisk) GetBranchType(id PlotID) (BranchType, error) {
	// compute db key
	key, err := computeBranchTypeKey(id)
	if err != nil {
		return UNKNOWN, err
	}

	// fetch type
	branchType, err := l.db.Get(key, nil)
	if err == leveldb.ErrNotFound {
		return UNKNOWN, nil
	}
	if err != nil {
		return UNKNOWN, err
	}
	return BranchType(branchType[0]), nil
}

// ConnectPlot connects a plot to the tip of the plot thread and applies the representations to the ledger.
func (l LedgerDisk) ConnectPlot(id PlotID, plot *Plot) ([]RepresentationID, error) {
	// sanity check
	tipID, _, err := l.GetThreadTip()
	if err != nil {
		return nil, err
	}
	if tipID != nil && *tipID != plot.Header.Previous {
		return nil, fmt.Errorf("Being asked to connect %s but previous %s does not match tip %s",
			id, plot.Header.Previous, *tipID)
	}

	// apply all resulting writes atomically
	batch := new(leveldb.Batch)

	imbalanceCache := NewImbalanceCache(l)
	txIDs := make([]RepresentationID, len(plot.Representations))

	for i, tx := range plot.Representations {
		txID, err := tx.ID()
		if err != nil {
			return nil, err
		}
		txIDs[i] = txID

		// verify the representation hasn't been processed already.
		// note that we can safely prune indices for representations older than the previous series
		key, err := computeRepresentationIndexKey(txID)
		if err != nil {
			return nil, err
		}
		ok, err := l.db.Has(key, nil)
		if err != nil {
			return nil, err
		}
		if ok {
			return nil, fmt.Errorf("Representation %s already processed", txID)
		}

		// set the representation index now
		indexBytes, err := encodeRepresentationIndex(plot.Header.Height, i)
		if err != nil {
			return nil, err
		}
		batch.Put(key, indexBytes)

		txToApply := tx

		if tx.IsPlotroot() {
			// don't apply a plotroot to a imbalance until it's x plots deep.
			// during honest reorgs normal representations usually get into the new most-work branch
			// but plotroots vanish. this mitigates the impact on UX when reorgs occur and representations
			// depend on plotroots.
			txToApply = nil

			if plot.Header.Height-PLOTROOT_MATURITY >= 0 {
				// mature the plotroot from 100 plots ago now
				oldID, err := l.GetPlotIDForHeight(plot.Header.Height - PLOTROOT_MATURITY)
				if err != nil {
					return nil, err
				}
				if oldID == nil {
					return nil, fmt.Errorf("Missing plot at height %d\n",
						plot.Header.Height-PLOTROOT_MATURITY)
				}

				// we could store the last 100 plotroots on our own in memory if we end up needing to
				oldTx, _, err := l.plotStore.GetRepresentation(*oldID, 0)
				if err != nil {
					return nil, err
				}
				if oldTx == nil {
					return nil, fmt.Errorf("Missing plotroot from plot %s\n", *oldID)
				}

				// apply it to the recipient's imbalance
				txToApply = oldTx
			}
		}

		if txToApply != nil {
			// check sender imbalance and update sender and receiver imbalances
			ok, err := imbalanceCache.Apply(txToApply)
			if err != nil {
				return nil, err
			}
			if !ok {
				txID, _ := txToApply.ID()
				return nil, fmt.Errorf("Sender has insufficient imbalance in representation %s", txID)
			}
		}

		// associate this representation with both parties
		if !tx.IsPlotroot() {
			key, err = computePubKeyRepresentationIndexKey(tx.From, &plot.Header.Height, &i)
			if err != nil {
				return nil, err
			}
			batch.Put(key, []byte{0x1})
		}
		key, err = computePubKeyRepresentationIndexKey(tx.To, &plot.Header.Height, &i)
		if err != nil {
			return nil, err
		}
		batch.Put(key, []byte{0x1})
	}

	// update recorded imbalances
	imbalances := imbalanceCache.Imbalances()
	for pubKeyBytes, imbalance := range imbalances {
		key, err := computePubKeyImbalanceKey(ed25519.PublicKey(pubKeyBytes[:]))
		if err != nil {
			return nil, err
		}
		if imbalance == 0 {
			batch.Delete(key)
		} else {
			imbalanceBytes, err := encodeNumber(imbalance)
			if err != nil {
				return nil, err
			}
			batch.Put(key, imbalanceBytes)
		}
	}

	// index the plot by height
	key, err := computePlotHeightIndexKey(plot.Header.Height)
	if err != nil {
		return nil, err
	}
	batch.Put(key, id[:])

	// set this plot on the main thread
	key, err = computeBranchTypeKey(id)
	if err != nil {
		return nil, err
	}
	batch.Put(key, []byte{byte(MAIN)})

	// set this plot as the new tip
	key, err = computeThreadTipKey()
	if err != nil {
		return nil, err
	}
	ctBytes, err := encodeThreadTip(id, plot.Header.Height)
	if err != nil {
		return nil, err
	}
	batch.Put(key, ctBytes)

	// prune historic representation and public key representation indices now
	if l.prune && plot.Header.Height >= 2*PLOTS_UNTIL_NEW_SERIES {
		if err := l.pruneIndices(plot.Header.Height-2*PLOTS_UNTIL_NEW_SERIES, batch); err != nil {
			return nil, err
		}
	}

	// perform the writes
	wo := opt.WriteOptions{Sync: true}
	if err := l.db.Write(batch, &wo); err != nil {
		return nil, err
	}

	return txIDs, nil
}

// DisconnectPlot disconnects a plot from the tip of the plot thread and undoes the effects of the representations on the ledger.
func (l LedgerDisk) DisconnectPlot(id PlotID, plot *Plot) ([]RepresentationID, error) {
	// sanity check
	tipID, _, err := l.GetThreadTip()
	if err != nil {
		return nil, err
	}
	if tipID == nil {
		return nil, fmt.Errorf("Being asked to disconnect %s but no tip is currently set",
			id)
	}
	if *tipID != id {
		return nil, fmt.Errorf("Being asked to disconnect %s but it does not match tip %s",
			id, *tipID)
	}

	// apply all resulting writes atomically
	batch := new(leveldb.Batch)

	imbalanceCache := NewImbalanceCache(l)
	txIDs := make([]RepresentationID, len(plot.Representations))

	// disconnect representations in reverse order
	for i := len(plot.Representations) - 1; i >= 0; i-- {
		tx := plot.Representations[i]
		txID, err := tx.ID()
		if err != nil {
			return nil, err
		}
		// save the id
		txIDs[i] = txID

		// mark the representation unprocessed now (delete its index)
		key, err := computeRepresentationIndexKey(txID)
		if err != nil {
			return nil, err
		}
		batch.Delete(key)

		txToUndo := tx
		if tx.IsPlotroot() {
			// plotroot doesn't affect recipient imbalance for x more plots
			txToUndo = nil

			if plot.Header.Height-PLOTROOT_MATURITY >= 0 {
				// undo the effect of the plotroot from x plots ago now
				oldID, err := l.GetPlotIDForHeight(plot.Header.Height - PLOTROOT_MATURITY)
				if err != nil {
					return nil, err
				}
				if oldID == nil {
					return nil, fmt.Errorf("Missing plot at height %d\n",
						plot.Header.Height-PLOTROOT_MATURITY)
				}
				oldTx, _, err := l.plotStore.GetRepresentation(*oldID, 0)
				if err != nil {
					return nil, err
				}
				if oldTx == nil {
					return nil, fmt.Errorf("Missing plotroot from plot %s\n", *oldID)
				}

				// undo its effect on the recipient's imbalance
				txToUndo = oldTx
			}
		}

		if txToUndo != nil {
			// credit sender and debit recipient
			err = imbalanceCache.Undo(txToUndo)
			if err != nil {
				return nil, err
			}
		}

		// unassociate this representation with both parties
		if !tx.IsPlotroot() {
			key, err = computePubKeyRepresentationIndexKey(tx.From, &plot.Header.Height, &i)
			if err != nil {
				return nil, err
			}
			batch.Delete(key)
		}
		key, err = computePubKeyRepresentationIndexKey(tx.To, &plot.Header.Height, &i)
		if err != nil {
			return nil, err
		}
		batch.Delete(key)
	}

	// update recorded imbalances
	imbalances := imbalanceCache.Imbalances()
	for pubKeyBytes, imbalance := range imbalances {
		key, err := computePubKeyImbalanceKey(ed25519.PublicKey(pubKeyBytes[:]))
		if err != nil {
			return nil, err
		}
		if imbalance == 0 {
			batch.Delete(key)
		} else {
			imbalanceBytes, err := encodeNumber(imbalance)
			if err != nil {
				return nil, err
			}
			batch.Put(key, imbalanceBytes)
		}
	}

	// remove this plot's index by height
	key, err := computePlotHeightIndexKey(plot.Header.Height)
	if err != nil {
		return nil, err
	}
	batch.Delete(key)

	// set this plot on a side thread
	key, err = computeBranchTypeKey(id)
	if err != nil {
		return nil, err
	}
	batch.Put(key, []byte{byte(SIDE)})

	// set the previous plot as the thread tip
	key, err = computeThreadTipKey()
	if err != nil {
		return nil, err
	}
	ctBytes, err := encodeThreadTip(plot.Header.Previous, plot.Header.Height-1)
	if err != nil {
		return nil, err
	}
	batch.Put(key, ctBytes)

	// restore historic indices now
	if l.prune && plot.Header.Height >= 2*PLOTS_UNTIL_NEW_SERIES {
		if err := l.restoreIndices(plot.Header.Height-2*PLOTS_UNTIL_NEW_SERIES, batch); err != nil {
			return nil, err
		}
	}

	// perform the writes
	wo := opt.WriteOptions{Sync: true}
	if err := l.db.Write(batch, &wo); err != nil {
		return nil, err
	}

	return txIDs, nil
}

// Prune representation and public key representation indices created by the plot at the given height
func (l LedgerDisk) pruneIndices(height int64, batch *leveldb.Batch) error {
	// get the ID
	id, err := l.GetPlotIDForHeight(height)
	if err != nil {
		return err
	}
	if id == nil {
		return fmt.Errorf("Missing plot ID for height %d\n", height)
	}

	// fetch the plot
	plot, err := l.plotStore.GetPlot(*id)
	if err != nil {
		return err
	}
	if plot == nil {
		return fmt.Errorf("Missing plot %s\n", *id)
	}

	for i, tx := range plot.Representations {
		txID, err := tx.ID()
		if err != nil {
			return err
		}

		// prune representation index
		key, err := computeRepresentationIndexKey(txID)
		if err != nil {
			return err
		}
		batch.Delete(key)

		// prune public key representation indices
		if !tx.IsPlotroot() {
			key, err = computePubKeyRepresentationIndexKey(tx.From, &plot.Header.Height, &i)
			if err != nil {
				return err
			}
			batch.Delete(key)
		}
		key, err = computePubKeyRepresentationIndexKey(tx.To, &plot.Header.Height, &i)
		if err != nil {
			return err
		}
		batch.Delete(key)
	}

	return nil
}

// Restore representation and public key representation indices created by the plot at the given height
func (l LedgerDisk) restoreIndices(height int64, batch *leveldb.Batch) error {
	// get the ID
	id, err := l.GetPlotIDForHeight(height)
	if err != nil {
		return err
	}
	if id == nil {
		return fmt.Errorf("Missing plot ID for height %d\n", height)
	}

	// fetch the plot
	plot, err := l.plotStore.GetPlot(*id)
	if err != nil {
		return err
	}
	if plot == nil {
		return fmt.Errorf("Missing plot %s\n", *id)
	}

	for i, tx := range plot.Representations {
		txID, err := tx.ID()
		if err != nil {
			return err
		}

		// restore representation index
		key, err := computeRepresentationIndexKey(txID)
		if err != nil {
			return err
		}
		indexBytes, err := encodeRepresentationIndex(plot.Header.Height, i)
		if err != nil {
			return err
		}
		batch.Put(key, indexBytes)

		// restore public key representation indices
		if !tx.IsPlotroot() {
			key, err = computePubKeyRepresentationIndexKey(tx.From, &plot.Header.Height, &i)
			if err != nil {
				return err
			}
			batch.Put(key, []byte{0x1})
		}
		key, err = computePubKeyRepresentationIndexKey(tx.To, &plot.Header.Height, &i)
		if err != nil {
			return err
		}
		batch.Put(key, []byte{0x1})
	}

	return nil
}

// GetPublicKeyImbalance returns the current imbalance of a given public key.
func (l LedgerDisk) GetPublicKeyImbalance(pubKey ed25519.PublicKey) (int64, error) {
	// compute db key
	key, err := computePubKeyImbalanceKey(pubKey)
	if err != nil {
		return 0, err
	}

	// fetch imbalance
	imbalanceBytes, err := l.db.Get(key, nil)
	if err == leveldb.ErrNotFound {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}

	// decode and return it
	var imbalance int64
	buf := bytes.NewReader(imbalanceBytes)
	binary.Read(buf, binary.BigEndian, &imbalance)
	return imbalance, nil
}

// GetPublicKeyImbalances returns the current imbalance of the given public keys
// along with plot ID and height of the corresponding main thread tip.
func (l LedgerDisk) GetPublicKeyImbalances(pubKeys []ed25519.PublicKey) (
	map[[ed25519.PublicKeySize]byte]int64, *PlotID, int64, error) {

	// get a consistent view across all queries
	snapshot, err := l.db.GetSnapshot()
	if err != nil {
		return nil, nil, 0, err
	}
	defer snapshot.Release()

	// get the thread tip
	tipID, tipHeight, err := getThreadTip(snapshot)
	if err != nil {
		return nil, nil, 0, err
	}

	imbalances := make(map[[ed25519.PublicKeySize]byte]int64)

	for _, pubKey := range pubKeys {
		// compute imbalance db key
		key, err := computePubKeyImbalanceKey(pubKey)
		if err != nil {
			return nil, nil, 0, err
		}

		var pk [ed25519.PublicKeySize]byte
		copy(pk[:], pubKey)

		// fetch imbalance
		imbalanceBytes, err := snapshot.Get(key, nil)
		if err == leveldb.ErrNotFound {
			imbalances[pk] = 0
			continue
		}
		if err != nil {
			return nil, nil, 0, err
		}

		// decode it
		var imbalance int64
		buf := bytes.NewReader(imbalanceBytes)
		binary.Read(buf, binary.BigEndian, &imbalance)

		// save it
		imbalances[pk] = imbalance
	}

	return imbalances, tipID, tipHeight, nil
}

// GetRepresentationIndex returns the index of a processed representation.
func (l LedgerDisk) GetRepresentationIndex(id RepresentationID) (*PlotID, int, error) {
	// compute the db key
	key, err := computeRepresentationIndexKey(id)
	if err != nil {
		return nil, 0, err
	}

	// we want a consistent view during our two queries as height can change
	snapshot, err := l.db.GetSnapshot()
	if err != nil {
		return nil, 0, err
	}
	defer snapshot.Release()

	// fetch and decode the index
	indexBytes, err := snapshot.Get(key, nil)
	if err == leveldb.ErrNotFound {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	height, index, err := decodeRepresentationIndex(indexBytes)
	if err != nil {
		return nil, 0, err
	}

	// map height to plot id
	plotID, err := getPlotIDForHeight(height, snapshot)
	if err != nil {
		return nil, 0, err
	}

	// return it
	return plotID, index, nil
}

// GetPublicKeyRepresentationIndicesRange returns representation indices involving a given public key
// over a range of heights. If startHeight > endHeight this iterates in reverse.
func (l LedgerDisk) GetPublicKeyRepresentationIndicesRange(
	pubKey ed25519.PublicKey, startHeight, endHeight int64, startIndex, limit int) (
	[]PlotID, []int, int64, int, error) {

	if endHeight >= startHeight {
		// forward
		return l.getPublicKeyRepresentationIndicesRangeForward(
			pubKey, startHeight, endHeight, startIndex, limit)
	}

	// reverse
	return l.getPublicKeyRepresentationIndicesRangeReverse(
		pubKey, startHeight, endHeight, startIndex, limit)
}

// Iterate through representation history going forward
func (l LedgerDisk) getPublicKeyRepresentationIndicesRangeForward(
	pubKey ed25519.PublicKey, startHeight, endHeight int64, startIndex, limit int) (
	ids []PlotID, indices []int, lastHeight int64, lastIndex int, err error) {
	startKey, err := computePubKeyRepresentationIndexKey(pubKey, &startHeight, &startIndex)
	if err != nil {
		return
	}

	endHeight += 1 // make it inclusive
	endKey, err := computePubKeyRepresentationIndexKey(pubKey, &endHeight, nil)
	if err != nil {
		return
	}

	heightMap := make(map[int64]*PlotID)

	// we want a consistent view of this. heights can change out from under us otherwise
	snapshot, err := l.db.GetSnapshot()
	if err != nil {
		return
	}
	defer snapshot.Release()

	iter := snapshot.NewIterator(&util.Range{Start: startKey, Limit: endKey}, nil)
	for iter.Next() {
		_, lastHeight, lastIndex, err = decodePubKeyRepresentationIndexKey(iter.Key())
		if err != nil {
			iter.Release()
			return nil, nil, 0, 0, err
		}

		// lookup the plot id
		id, ok := heightMap[lastHeight]
		if !ok {
			var err error
			id, err = getPlotIDForHeight(lastHeight, snapshot)
			if err != nil {
				iter.Release()
				return nil, nil, 0, 0, err
			}
			if id == nil {
				iter.Release()
				return nil, nil, 0, 0, fmt.Errorf(
					"No plot found at height %d", lastHeight)
			}
			heightMap[lastHeight] = id
		}

		ids = append(ids, *id)
		indices = append(indices, lastIndex)
		if limit != 0 && len(indices) == limit {
			break
		}
	}
	iter.Release()
	if err := iter.Error(); err != nil {
		return nil, nil, 0, 0, err
	}
	return
}

// Iterate through representation history in reverse
func (l LedgerDisk) getPublicKeyRepresentationIndicesRangeReverse(
	pubKey ed25519.PublicKey, startHeight, endHeight int64, startIndex, limit int) (
	ids []PlotID, indices []int, lastHeight int64, lastIndex int, err error) {
	endKey, err := computePubKeyRepresentationIndexKey(pubKey, &endHeight, nil)
	if err != nil {
		return
	}

	// make it inclusive
	startIndex += 1
	startKey, err := computePubKeyRepresentationIndexKey(pubKey, &startHeight, &startIndex)
	if err != nil {
		return
	}

	heightMap := make(map[int64]*PlotID)

	// we want a consistent view of this. heights can change out from under us otherwise
	snapshot, err := l.db.GetSnapshot()
	if err != nil {
		return
	}
	defer snapshot.Release()

	iter := snapshot.NewIterator(&util.Range{Start: endKey, Limit: startKey}, nil)
	for ok := iter.Last(); ok; ok = iter.Prev() {
		_, lastHeight, lastIndex, err = decodePubKeyRepresentationIndexKey(iter.Key())
		if err != nil {
			iter.Release()
			return nil, nil, 0, 0, err
		}

		// lookup the plot id
		id, ok := heightMap[lastHeight]
		if !ok {
			var err error
			id, err = getPlotIDForHeight(lastHeight, snapshot)
			if err != nil {
				iter.Release()
				return nil, nil, 0, 0, err
			}
			if id == nil {
				iter.Release()
				return nil, nil, 0, 0, fmt.Errorf(
					"No plot found at height %d", lastHeight)
			}
			heightMap[lastHeight] = id
		}

		ids = append(ids, *id)
		indices = append(indices, lastIndex)
		if limit != 0 && len(indices) == limit {
			break
		}
	}
	iter.Release()
	if err := iter.Error(); err != nil {
		return nil, nil, 0, 0, err
	}
	return
}

// Imbalance returns the total current ledger imbalance by summing the imbalance of all public keys.
// It's only used offline for verification purposes.
func (l LedgerDisk) Imbalance() (int64, error) {
	var total int64

	// compute the sum of all public key imbalances
	key, err := computePubKeyImbalanceKey(nil)
	if err != nil {
		return 0, err
	}
	iter := l.db.NewIterator(util.BytesPrefix(key), nil)
	for iter.Next() {
		var imbalance int64
		buf := bytes.NewReader(iter.Value())
		binary.Read(buf, binary.BigEndian, &imbalance)
		total += imbalance
	}
	iter.Release()
	if err := iter.Error(); err != nil {
		return 0, err
	}

	return total, nil
}

// GetPublicKeyImbalanceAt returns the public key imbalance at the given height.
// It's only used offline for historical and verification purposes.
// This is only accurate when the full plot thread is indexed (pruning disabled.)
func (l LedgerDisk) GetPublicKeyImbalanceAt(pubKey ed25519.PublicKey, height int64) (int64, error) {
	_, currentHeight, err := l.GetThreadTip()
	if err != nil {
		return 0, err
	}

	startKey, err := computePubKeyRepresentationIndexKey(pubKey, nil, nil)
	if err != nil {
		return 0, err
	}

	height += 1 // make it inclusive
	endKey, err := computePubKeyRepresentationIndexKey(pubKey, &height, nil)
	if err != nil {
		return 0, err
	}

	var imbalance int64
	iter := l.db.NewIterator(&util.Range{Start: startKey, Limit: endKey}, nil)
	for iter.Next() {
		_, height, index, err := decodePubKeyRepresentationIndexKey(iter.Key())
		if err != nil {
			iter.Release()
			return 0, err
		}

		if index == 0 && height > currentHeight-PLOTROOT_MATURITY {
			// plotroot isn't mature
			continue
		}

		id, err := l.GetPlotIDForHeight(height)
		if err != nil {
			iter.Release()
			return 0, err
		}
		if id == nil {
			iter.Release()
			return 0, fmt.Errorf("No plot found at height %d", height)
		}

		tx, _, err := l.plotStore.GetRepresentation(*id, index)
		if err != nil {
			iter.Release()
			return 0, err
		}
		if tx == nil {
			iter.Release()
			return 0, fmt.Errorf("No representation found in plot %s at index %d",
				*id, index)
		}

		if bytes.Equal(pubKey, tx.To) {
			imbalance += 1
		} else if bytes.Equal(pubKey, tx.From) {
			imbalance -= 1
		} else {
			iter.Release()
			txID, _ := tx.ID()
			return 0, fmt.Errorf("Representation %s doesn't involve the public key", txID)
		}
	}
	iter.Release()
	if err := iter.Error(); err != nil {
		return 0, err
	}
	return imbalance, nil
}

// Close is called to close any underlying storage.
func (l LedgerDisk) Close() error {
	return l.db.Close()
}

// leveldb schema

// T                    -> {bid}{height} (main thread tip)
// B{bid}               -> main|side|orphan (1 byte)
// h{height}            -> {bid}
// t{txid}              -> {height}{index} (prunable up to the previous series)
// k{pk}{height}{index} -> 1 (not strictly necessary. probably should make it optional by flag)
// b{pk}                -> {imbalance} (we always need all of this table)

const threadTipPrefix = 'T'

const branchTypePrefix = 'B'

const plotHeightIndexPrefix = 'h'

const representationIndexPrefix = 't'

const pubKeyRepresentationIndexPrefix = 'k'

const pubKeyImbalancePrefix = 'b'

func computeBranchTypeKey(id PlotID) ([]byte, error) {
	key := new(bytes.Buffer)
	if err := key.WriteByte(branchTypePrefix); err != nil {
		return nil, err
	}
	if err := binary.Write(key, binary.BigEndian, id[:]); err != nil {
		return nil, err
	}
	return key.Bytes(), nil
}

func computePlotHeightIndexKey(height int64) ([]byte, error) {
	key := new(bytes.Buffer)
	if err := key.WriteByte(plotHeightIndexPrefix); err != nil {
		return nil, err
	}
	if err := binary.Write(key, binary.BigEndian, height); err != nil {
		return nil, err
	}
	return key.Bytes(), nil
}

func computeThreadTipKey() ([]byte, error) {
	key := new(bytes.Buffer)
	if err := key.WriteByte(threadTipPrefix); err != nil {
		return nil, err
	}
	return key.Bytes(), nil
}

func computeRepresentationIndexKey(id RepresentationID) ([]byte, error) {
	key := new(bytes.Buffer)
	if err := key.WriteByte(representationIndexPrefix); err != nil {
		return nil, err
	}
	if err := binary.Write(key, binary.BigEndian, id[:]); err != nil {
		return nil, err
	}
	return key.Bytes(), nil
}

func computePubKeyRepresentationIndexKey(
	pubKey ed25519.PublicKey, height *int64, index *int) ([]byte, error) {
	key := new(bytes.Buffer)
	if err := key.WriteByte(pubKeyRepresentationIndexPrefix); err != nil {
		return nil, err
	}
	if err := binary.Write(key, binary.BigEndian, pubKey); err != nil {
		return nil, err
	}
	if height == nil {
		return key.Bytes(), nil
	}
	if err := binary.Write(key, binary.BigEndian, *height); err != nil {
		return nil, err
	}
	if index == nil {
		return key.Bytes(), nil
	}
	index32 := int32(*index)
	if err := binary.Write(key, binary.BigEndian, index32); err != nil {
		return nil, err
	}
	return key.Bytes(), nil
}

func decodePubKeyRepresentationIndexKey(key []byte) (ed25519.PublicKey, int64, int, error) {
	buf := bytes.NewBuffer(key)
	if _, err := buf.ReadByte(); err != nil {
		return nil, 0, 0, err
	}
	var pubKey [ed25519.PublicKeySize]byte
	if err := binary.Read(buf, binary.BigEndian, pubKey[:32]); err != nil {
		return nil, 0, 0, err
	}
	var height int64
	if err := binary.Read(buf, binary.BigEndian, &height); err != nil {
		return nil, 0, 0, err
	}
	var index int32
	if err := binary.Read(buf, binary.BigEndian, &index); err != nil {
		return nil, 0, 0, err
	}
	return ed25519.PublicKey(pubKey[:]), height, int(index), nil
}

func computePubKeyImbalanceKey(pubKey ed25519.PublicKey) ([]byte, error) {
	key := new(bytes.Buffer)
	if err := key.WriteByte(pubKeyImbalancePrefix); err != nil {
		return nil, err
	}
	if err := binary.Write(key, binary.BigEndian, pubKey); err != nil {
		return nil, err
	}
	return key.Bytes(), nil
}

func encodeThreadTip(id PlotID, height int64) ([]byte, error) {
	buf := new(bytes.Buffer)
	if err := binary.Write(buf, binary.BigEndian, id); err != nil {
		return nil, err
	}
	if err := binary.Write(buf, binary.BigEndian, height); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func decodeThreadTip(ctBytes []byte) (*PlotID, int64, error) {
	buf := bytes.NewBuffer(ctBytes)
	id := new(PlotID)
	if err := binary.Read(buf, binary.BigEndian, id); err != nil {
		return nil, 0, err
	}
	var height int64
	if err := binary.Read(buf, binary.BigEndian, &height); err != nil {
		return nil, 0, err
	}
	return id, height, nil
}

func encodeNumber(num int64) ([]byte, error) {
	buf := new(bytes.Buffer)
	if err := binary.Write(buf, binary.BigEndian, num); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func encodeRepresentationIndex(height int64, index int) ([]byte, error) {
	buf := new(bytes.Buffer)
	if err := binary.Write(buf, binary.BigEndian, height); err != nil {
		return nil, err
	}
	index32 := int32(index)
	if err := binary.Write(buf, binary.BigEndian, index32); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func decodeRepresentationIndex(indexBytes []byte) (int64, int, error) {
	buf := bytes.NewBuffer(indexBytes)
	var height int64
	if err := binary.Read(buf, binary.BigEndian, &height); err != nil {
		return 0, 0, err
	}
	var index int32
	if err := binary.Read(buf, binary.BigEndian, &index); err != nil {
		return 0, 0, err
	}
	return height, int(index), nil
}
