package plotthread

import "golang.org/x/crypto/ed25519"

// Protocol is the name of this version of the plotthread peer protocol.
const Protocol = "plotthread.1"

// Message is a message frame for all messages in the plotthread.1 protocol.
type Message struct {
	Type string      `json:"type"`
	Body interface{} `json:"body,omitempty"`
}

// InvPlotMessage is used to communicate plots available for download.
// Type: "inv_plot".
type InvPlotMessage struct {
	PlotIDs []PlotID `json:"plot_ids"`
}

// GetPlotMessage is used to request a plot for download.
// Type: "get_plot".
type GetPlotMessage struct {
	PlotID PlotID `json:"plot_id"`
}

// GetPlotByHeightMessage is used to request a plot for download.
// Type: "get_plot_by_height".
type GetPlotByHeightMessage struct {
	Height int64 `json:"height"`
}

// PlotMessage is used to send a peer a complete plot.
// Type: "plot".
type PlotMessage struct {
	PlotID *PlotID `json:"plot_id,omitempty"`
	Plot   *Plot   `json:"plot,omitempty"`
}

// GetPlotHeaderMessage is used to request a plot header.
// Type: "get_plot_header".
type GetPlotHeaderMessage struct {
	PlotID PlotID `json:"plot_id"`
}

// GetPlotHeaderByHeightMessage is used to request a plot header.
// Type: "get_plot_header_by_height".
type GetPlotHeaderByHeightMessage struct {
	Height int64 `json:"height"`
}

// PlotHeaderMessage is used to send a peer a plot's header.
// Type: "plot_header".
type PlotHeaderMessage struct {
	PlotID     *PlotID     `json:"plot_id,omitempty"`
	PlotHeader *PlotHeader `json:"header,omitempty"`
}

// FindCommonAncestorMessage is used to find a common ancestor with a peer.
// Type: "find_common_ancestor".
type FindCommonAncestorMessage struct {
	PlotIDs []PlotID `json:"plot_ids"`
}

// GetProfile requests a public key's profile
// Type: "get_profile".
type GetProfileMessage struct {
	PublicKey ed25519.PublicKey `json:"public_key"`
}

// ProfileMessage is used to send a public key's profile to a peer.
// Type: "profile".
type ProfileMessage struct {
	PublicKey     ed25519.PublicKey         `json:"public_key"`
	Ranking   	  float64       			`json:"ranking"`
	Imbalance 	  int64                     `json:"imbalance"`
	PlusCode 	  string                    `json:"plus_code"`
	PlotID        PlotID                    `json:"plot_id,omitempty"`
	Height        int64                     `json:"height,omitempty"`
	Error         string                    `json:"error,omitempty"`
}

// GetGraph requests a public key's plot graph
// Type: "get_graph".
type GetGraphMessage struct {
	PublicKey ed25519.PublicKey `json:"public_key"`
}

// PlotGraphMessage is used to send a public key's plot graph representations to a peer.
// Type: "graph".
type GraphMessage struct {
	PlotID   PlotID             `json:"plot_id,omitempty"`
	Height    int64             `json:"height,omitempty"`
	PublicKey ed25519.PublicKey `json:"public_key"`
	Graph   string       		`json:"graph"`
}

// GetRankingMessage requests a public key's representivity ranking.
// Type: "get_ranking".
type GetRankingMessage struct {
	PublicKey ed25519.PublicKey `json:"public_key"`
}

// RankingMessage is used to send a public key's representivity ranking to a peer.
// Type: "ranking".
type RankingMessage struct {
	PlotID   PlotID             `json:"plot_id,omitempty"`
	Height    int64             `json:"height,omitempty"`
	PublicKey ed25519.PublicKey `json:"public_key"`
	Ranking   	  float64       `json:"ranking"`
	Error     string            `json:"error,omitempty"`
}

// GetRankingsMessage requests a set of public key rankings.
// Type: "get_rankings".
type GetRankingsMessage struct {
	PublicKeys []ed25519.PublicKey `json:"public_keys"`
}

// RankingsMessage is used to send public key rankings to a peer.
// Type: "rankings".
type RankingsMessage struct {
	PlotID   PlotID             `json:"plot_id,omitempty"`
	Height   int64              `json:"height,omitempty"`
	Rankings []PublicKeyRanking `json:"rankings,omitempty"`
	Error    string             `json:"error,omitempty"`
}

// PublicKeyRanking is an entry in the RankingsMessage's Rankings field.
type PublicKeyRanking struct {
	PublicKey string 			`json:"public_key"`
	Ranking   float64           `json:"ranking"`
}

// GetImbalanceMessage requests a public key's imbalance.
// Type: "get_imbalance".
type GetImbalanceMessage struct {
	PublicKey ed25519.PublicKey `json:"public_key"`
}

// ImbalanceMessage is used to send a public key's imbalance to a peer.
// Type: "imbalance".
type ImbalanceMessage struct {
	PlotID    *PlotID           `json:"plot_id,omitempty"`
	Height    int64             `json:"height,omitempty"`
	PublicKey ed25519.PublicKey `json:"public_key"`
	Imbalance int64             `json:"imbalance"`
	Error     string            `json:"error,omitempty"`
}

// GetImbalancesMessage requests a set of public key imbalances.
// Type: "get_imbalances".
type GetImbalancesMessage struct {
	PublicKeys []ed25519.PublicKey `json:"public_keys"`
}

// ImbalancesMessage is used to send public key imbalances to a peer.
// Type: "imbalances".
type ImbalancesMessage struct {
	PlotID  *PlotID                 `json:"plot_id,omitempty"`
	Height   int64                  `json:"height,omitempty"`
	Imbalances []PublicKeyImbalance `json:"imbalances,omitempty"`
	Error    string                 `json:"error,omitempty"`
}

// PublicKeyImbalance is an entry in the ImbalancesMessage's Imbalances field.
type PublicKeyImbalance struct {
	PublicKey ed25519.PublicKey   `json:"public_key"`
	Imbalance   int64             `json:"imbalance"`
}

// GetRepresentationMessage is used to request a confirmed representation.
// Type: "get_representation".
type GetRepresentationMessage struct {
	RepresentationID RepresentationID `json:"representation_id"`
}

// RepresentationMessage is used to send a peer a confirmed representation.
// Type: "representation"
type RepresentationMessage struct {
	PlotID       *PlotID              `json:"plot_id,omitempty"`
	Height        int64               `json:"height,omitempty"`
	RepresentationID RepresentationID `json:"representation_id"`
	Representation   *Representation  `json:"representation,omitempty"`
}

// TipHeaderMessage is used to send a peer the header for the tip plot in the plot thread.
// Type: "tip_header". It is sent in response to the empty "get_tip_header" message type.
type TipHeaderMessage struct {
	PlotID     *PlotID       `json:"plot_id,omitempty"`
	PlotHeader *PlotHeader   `json:"header,omitempty"`
	TimeSeen    int64        `json:"time_seen,omitempty"`
}

// PushRepresentationMessage is used to push a newly processed unconfirmed representation to peers.
// Type: "push_representation".
type PushRepresentationMessage struct {
	Representation *Representation `json:"representation"`
}

// PushRepresentationResultMessage is sent in response to a PushRepresentationMessage.
// Type: "push_representation_result".
type PushRepresentationResultMessage struct {
	RepresentationID RepresentationID `json:"representation_id"`
	Error         string              `json:"error,omitempty"`
}

// FilterLoadMessage is used to request that we load a filter which is used to
// filter representations returned to the peer based on interest.
// Type: "filter_load"
type FilterLoadMessage struct {
	Type   string `json:"type"`
	Filter []byte `json:"filter"`
}

// FilterAddMessage is used to request the addition of the given public keys to the current filter.
// The filter is created if it's not set.
// Type: "filter_add".
type FilterAddMessage struct {
	PublicKeys []ed25519.PublicKey `json:"public_keys"`
}

// FilterResultMessage indicates whether or not the filter request was successful.
// Type: "filter_result".
type FilterResultMessage struct {
	Error string `json:"error,omitempty"`
}

// FilterPlotMessage represents a pared down plot containing only representations relevant to the peer given their filter.
// Type: "filter_plot".
type FilterPlotMessage struct {
	PlotID      PlotID                `json:"plot_id"`
	Header       *PlotHeader          `json:"header"`
	Representations []*Representation `json:"representations"`
}

// FilterRepresentationQueueMessage returns a pared down view of the unconfirmed representation queue containing only
// representations relevant to the peer given their filter.
// Type: "filter_representation_queue".
type FilterRepresentationQueueMessage struct {
	Representations []*Representation `json:"representations"`
	Error        string               `json:"error,omitempty"`
}

// GetPublicKeyRepresentationsMessage requests representations associated with a given public key over a given
// height range of the plot thread.
// Type: "get_public_key_representations".
type GetPublicKeyRepresentationsMessage struct {
	PublicKey   ed25519.PublicKey `json:"public_key"`
	StartHeight int64             `json:"start_height"`
	StartIndex  int               `json:"start_index"`
	EndHeight   int64             `json:"end_height"`
	Limit       int               `json:"limit"`
}

// PublicKeyRepresentationsMessage is used to return a list of plot headers and the representations relevant to
// the public key over a given height range of the plot thread.
// Type: "public_key_representations".
type PublicKeyRepresentationsMessage struct {
	PublicKey    ed25519.PublicKey     `json:"public_key"`
	StartHeight  int64                 `json:"start_height"`
	StopHeight   int64                 `json:"stop_height"`
	StopIndex    int                   `json:"stop_index"`
	FilterPlots []*FilterPlotMessage   `json:"filter_plots"`
	Error        string                `json:"error,omitempty"`
}

// PeerAddressesMessage is used to communicate a list of potential peer addresses known by a peer.
// Type: "peer_addresses". Sent in response to the empty "get_peer_addresses" message type.
type PeerAddressesMessage struct {
	Addresses []string `json:"addresses"`
}

// GetWorkMessage is used by a scribing peer to request scribing work.
// Type: "get_work"
type GetWorkMessage struct {
	PublicKeys []ed25519.PublicKey `json:"public_keys"`
	Memo       string              `json:"memo,omitempty"`
}

// WorkMessage is used by a client to send work to perform to a scribing peer.
// The timestamp and nonce in the header can be manipulated by the scribing peer.
// It is the scribing peer's responsibility to ensure the timestamp is not set below
// the minimum timestamp and that the nonce does not exceed MAX_NUMBER (2^53-1).
// Type: "work"
type WorkMessage struct {
	WorkID  int32        `json:"work_id"`
	Header  *PlotHeader  `json:"header"`
	MinTime int64        `json:"min_time"`
	Error   string       `json:"error,omitempty"`
}

// SubmitWorkMessage is used by a scribing peer to submit a potential solution to the client.
// Type: "submit_work"
type SubmitWorkMessage struct {
	WorkID int32        `json:"work_id"`
	Header *PlotHeader  `json:"header"`
}

// SubmitWorkResultMessage is used to inform a scribing peer of the result of its work.
// Type: "submit_work_result"
type SubmitWorkResultMessage struct {
	WorkID int32  `json:"work_id"`
	Error  string `json:"error,omitempty"`
}
