package plotthread

// PlotStorage is an interface for storing plots and their representations.
type PlotStorage interface {
	// Store is called to store all of the plot's information.
	Store(id PlotID, plot *Plot, now int64) error

	// Get returns the referenced plot.
	GetPlot(id PlotID) (*Plot, error)

	// GetPlotBytes returns the referenced plot as a byte slice.
	GetPlotBytes(id PlotID) ([]byte, error)

	// GetPlotHeader returns the referenced plot's header and the timestamp of when it was stored.
	GetPlotHeader(id PlotID) (*PlotHeader, int64, error)

	// GetRepresentation returns a representation within a plot and the plot's header.
	GetRepresentation(id PlotID, index int) (*Representation, *PlotHeader, error)
}
