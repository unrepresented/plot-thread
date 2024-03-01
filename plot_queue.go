package plotthread

import (
	"container/list"
	"sync"
	"time"
)

// PlotQueue is a queue of plots to download.
type PlotQueue struct {
	plotMap   map[PlotID]*list.Element
	plotQueue *list.List
	lock       sync.RWMutex
}

// If a plot has been in the queue for more than 2 minutes it can be re-added with a new peer responsible for its download.
const maxQueueWait = 2 * time.Minute

type plotQueueEntry struct {
	id   PlotID
	who  string
	when time.Time
}

// NewPlotQueue returns a new instance of a PlotQueue.
func NewPlotQueue() *PlotQueue {
	return &PlotQueue{
		plotMap:   make(map[PlotID]*list.Element),
		plotQueue: list.New(),
	}
}

// Add adds the plot ID to the back of the queue and records the address of the peer who pushed it if it didn't exist in the queue.
// If it did exist and maxQueueWait has elapsed, the plot is left in its position but the peer responsible for download is updated.
func (b *PlotQueue) Add(id PlotID, who string) bool {
	b.lock.Lock()
	defer b.lock.Unlock()
	if e, ok := b.plotMap[id]; ok {
		entry := e.Value.(*plotQueueEntry)
		if time.Since(entry.when) < maxQueueWait {
			// it's still pending download
			return false
		}
		// it's expired. signal that it can be tried again and leave it in place
		entry.when = time.Now()
		// new peer owns its place in the queue
		entry.who = who
		return true
	}

	// add to the back of the queue
	entry := &plotQueueEntry{id: id, who: who, when: time.Now()}
	e := b.plotQueue.PushBack(entry)
	b.plotMap[id] = e
	return true
}

// Remove removes the plot ID from the queue only if the requester is who is currently responsible for its download.
func (b *PlotQueue) Remove(id PlotID, who string) bool {
	b.lock.Lock()
	defer b.lock.Unlock()
	if e, ok := b.plotMap[id]; ok {
		entry := e.Value.(*plotQueueEntry)
		if entry.who == who {
			b.plotQueue.Remove(e)
			delete(b.plotMap, entry.id)
			return true
		}
	}
	return false
}

// Exists returns true if the plot ID exists in the queue.
func (b *PlotQueue) Exists(id PlotID) bool {
	b.lock.RLock()
	defer b.lock.RUnlock()
	_, ok := b.plotMap[id]
	return ok
}

// Peek returns the ID of the plot at the front of the queue.
func (b *PlotQueue) Peek() (PlotID, bool) {
	b.lock.RLock()
	defer b.lock.RUnlock()
	if b.plotQueue.Len() == 0 {
		return PlotID{}, false
	}
	e := b.plotQueue.Front()
	entry := e.Value.(*plotQueueEntry)
	return entry.id, true
}

// Len returns the length of the queue.
func (b *PlotQueue) Len() int {
	b.lock.RLock()
	defer b.lock.RUnlock()
	return b.plotQueue.Len()
}
