package plotthread

import (
	"golang.org/x/crypto/ed25519"
)

// BranchType indicates the type of branch a particular plot resides on.
// Only plots currently on the main branch are considered confirmed and only
// representations in those plots affect public key imbalances.
// Values are: MAIN, SIDE, ORPHAN or UNKNOWN.
type BranchType int

const (
	MAIN = iota
	SIDE
	ORPHAN
	UNKNOWN
)

// Ledger is an interface to a ledger built from the most-work thread of plots.
// It manages and computes public key imbalances as well as representation and public key representation indices.
// It also maintains an index of the plot thread by height as well as branch information.
type Ledger interface {
	// GetThreadTip returns the ID and the height of the plot at the current tip of the main thread.
	GetThreadTip() (*PlotID, int64, error)

	// GetPlotIDForHeight returns the ID of the plot at the given plot thread height.
	GetPlotIDForHeight(height int64) (*PlotID, error)

	// SetBranchType sets the branch type for the given plot.
	SetBranchType(id PlotID, branchType BranchType) error

	// GetBranchType returns the branch type for the given plot.
	GetBranchType(id PlotID) (BranchType, error)

	// ConnectPlot connects a plot to the tip of the plot thread and applies the representations
	// to the ledger.
	ConnectPlot(id PlotID, plot *Plot) ([]RepresentationID, error)

	// DisconnectPlot disconnects a plot from the tip of the plot thread and undoes the effects
	// of the representations on the ledger.
	DisconnectPlot(id PlotID, plot *Plot) ([]RepresentationID, error)

	// GetPublicKeyImbalance returns the current imbalance of a given public key.
	GetPublicKeyImbalance(pubKey ed25519.PublicKey) (int64, error)

	// GetPublicKeyImbalances returns the current imbalance of the given public keys
	// along with plot ID and height of the corresponding main thread tip.
	GetPublicKeyImbalances(pubKeys []ed25519.PublicKey) (
		map[[ed25519.PublicKeySize]byte]int64, *PlotID, int64, error)

	// GetRepresentationIndex returns the index of a processed representation.
	GetRepresentationIndex(id RepresentationID) (*PlotID, int, error)

	// GetPublicKeyRepresentationIndicesRange returns representation indices involving a given public key
	// over a range of heights. If startHeight > endHeight this iterates in reverse.
	GetPublicKeyRepresentationIndicesRange(
		pubKey ed25519.PublicKey, startHeight, endHeight int64, startIndex, limit int) (
		[]PlotID, []int, int64, int, error)

	// Imbalance returns the total current ledger imbalance by summing the imbalance of all public keys.
	// It's only used offline for verification purposes.
	Imbalance() (int64, error)

	// GetPublicKeyImbalanceAt returns the public key imbalance at the given height.
	// It's only used offline for historical and verification purposes.
	// This is only accurate when the full plot thread is indexed (pruning disabled.)
	GetPublicKeyImbalanceAt(pubKey ed25519.PublicKey, height int64) (int64, error)
}
