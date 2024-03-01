package plotthread

// RepresentationQueue is an interface to a queue of representations to be confirmed.
type RepresentationQueue interface {
	// Add adds the representation to the queue. Returns true if the representation was added to the queue on this call.
	Add(id RepresentationID, tx *Representation) (bool, error)

	// AddBatch adds a batch of representations to the queue (a plot has been disconnected.)
	// "height" is the plot thread height after this disconnection.
	AddBatch(ids []RepresentationID, txs []*Representation, height int64) error

	// RemoveBatch removes a batch of representations from the queue (a plot has been connected.)
	// "height" is the plot thread height after this connection.
	// "more" indicates if more connections are coming.
	RemoveBatch(ids []RepresentationID, height int64, more bool) error

	// Get returns representations in the queue for the scriber.
	Get(limit int) []*Representation

	// Exists returns true if the given representation is in the queue.
	Exists(id RepresentationID) bool

	// ExistsSigned returns true if the given representation is in the queue and contains the given signature.
	ExistsSigned(id RepresentationID, signature Signature) bool

	// Len returns the queue length.
	Len() int
}
