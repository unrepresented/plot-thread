package plotthread

import (
	"bytes"
	"container/list"
	"encoding/base64"
	"fmt"
	"sync"
)

// RepresentationQueueMemory is an in-memory FIFO implementation of the RepresentationQueue interface.
type RepresentationQueueMemory struct {
	txMap        map[RepresentationID]*list.Element
	txQueue      *list.List
	imbalanceCache *ImbalanceCache
	lock         sync.RWMutex
}

// NewRepresentationQueueMemory returns a new NewRepresentationQueueMemory instance.
func NewRepresentationQueueMemory(ledger Ledger) *RepresentationQueueMemory {

	return &RepresentationQueueMemory{
		txMap:        make(map[RepresentationID]*list.Element),
		txQueue:      list.New(),
		imbalanceCache: NewImbalanceCache(ledger),
	}
}

// Add adds the representation to the queue. Returns true if the representation was added to the queue on this call.
func (t *RepresentationQueueMemory) Add(id RepresentationID, tx *Representation) (bool, error) {
	t.lock.Lock()
	defer t.lock.Unlock()
	if _, ok := t.txMap[id]; ok {
		// already exists
		return false, nil
	}

	// check sender imbalance and update sender and receiver imbalances
	ok, err := t.imbalanceCache.Apply(tx)
	if err != nil {
		return false, err
	}
	if !ok {
		// insufficient sender imbalance
		return false, fmt.Errorf("Representation %s sender %s has insufficient imbalance",
			id, base64.StdEncoding.EncodeToString(tx.From[:]))
	}

	// add to the back of the queue
	e := t.txQueue.PushBack(tx)
	t.txMap[id] = e
	return true, nil
}

// AddBatch adds a batch of representations to the queue (a plot has been disconnected.)
// "height" is the plot thread height after this disconnection.
func (t *RepresentationQueueMemory) AddBatch(ids []RepresentationID, txs []*Representation, height int64) error {
	t.lock.Lock()
	defer t.lock.Unlock()

	// add to front in reverse order.
	// we want formerly confirmed representations to have the highest
	// priority for getting into the next plot.
	for i := len(txs) - 1; i >= 0; i-- {
		if e, ok := t.txMap[ids[i]]; ok {
			// remove it from its current position
			t.txQueue.Remove(e)
		}
		e := t.txQueue.PushFront(txs[i])
		t.txMap[ids[i]] = e
	}

	// we don't want to invalidate anything based on maturity/expiration/imbalance yet.
	// if we're disconnecting a plot we're going to be connecting some shortly.
	return nil
}

// RemoveBatch removes a batch of representations from the queue (a plot has been connected.)
// "height" is the plot thread height after this connection.
// "more" indicates if more connections are coming.
func (t *RepresentationQueueMemory) RemoveBatch(ids []RepresentationID, height int64, more bool) error {
	t.lock.Lock()
	defer t.lock.Unlock()
	for _, id := range ids {
		e, ok := t.txMap[id]
		if !ok {
			// not in the queue
			continue
		}
		// remove it
		t.txQueue.Remove(e)
		delete(t.txMap, id)
	}

	if more {
		// we don't want to invalidate anything based on series/maturity/expiration/imbalance
		// until we're done connecting all of the plots we intend to
		return nil
	}

	return t.reprocessQueue(height)
}

// Rebuild the imbalance cache and remove representations now in violation
func (t *RepresentationQueueMemory) reprocessQueue(height int64) error {
	// invalidate the cache
	t.imbalanceCache.Reset()

	// remove invalidated representations from the queue
	tmpQueue := list.New()
	tmpQueue.PushBackList(t.txQueue)
	for e := tmpQueue.Front(); e != nil; e = e.Next() {
		tx := e.Value.(*Representation)
		// check that the series would still be valid
		if !checkRepresentationSeries(tx, height+1) ||
			// check maturity and expiration if included in the next plot
			!tx.IsMature(height+1) || tx.IsExpired(height+1) {
			// representation has been invalidated. remove and continue
			id, err := tx.ID()
			if err != nil {
				return err
			}
			e := t.txMap[id]
			t.txQueue.Remove(e)
			delete(t.txMap, id)
			continue
		}

		// check imbalance
		ok, err := t.imbalanceCache.Apply(tx)
		if err != nil {
			return err
		}
		if !ok {
			// representation has been invalidated. remove and continue
			id, err := tx.ID()
			if err != nil {
				return err
			}
			e := t.txMap[id]
			t.txQueue.Remove(e)
			delete(t.txMap, id)
			continue
		}
	}
	return nil
}

// Get returns representations in the queue for the scriber.
func (t *RepresentationQueueMemory) Get(limit int) []*Representation {
	var txs []*Representation
	t.lock.RLock()
	defer t.lock.RUnlock()
	if limit == 0 || t.txQueue.Len() < limit {
		txs = make([]*Representation, t.txQueue.Len())
	} else {
		txs = make([]*Representation, limit)
	}
	i := 0
	for e := t.txQueue.Front(); e != nil; e = e.Next() {
		txs[i] = e.Value.(*Representation)
		i++
		if i == limit {
			break
		}
	}
	return txs
}

// Exists returns true if the given representation is in the queue.
func (t *RepresentationQueueMemory) Exists(id RepresentationID) bool {
	t.lock.RLock()
	defer t.lock.RUnlock()
	_, ok := t.txMap[id]
	return ok
}

// ExistsSigned returns true if the given representation is in the queue and contains the given signature.
func (t *RepresentationQueueMemory) ExistsSigned(id RepresentationID, signature Signature) bool {
	t.lock.RLock()
	defer t.lock.RUnlock()
	if e, ok := t.txMap[id]; ok {
		tx := e.Value.(*Representation)
		return bytes.Equal(tx.Signature, signature)
	}
	return false
}

// Len returns the queue length.
func (t *RepresentationQueueMemory) Len() int {
	t.lock.RLock()
	defer t.lock.RUnlock()
	return t.txQueue.Len()
}
