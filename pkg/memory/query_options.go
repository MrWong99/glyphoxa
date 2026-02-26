package memory

// RelQueryParams holds the resolved parameters from a slice of [RelQueryOpt].
type RelQueryParams struct {
	RelTypes     []string
	DirectionIn  bool
	DirectionOut bool
	Limit        int
}

// ApplyRelQueryOpts applies a slice of [RelQueryOpt] functional options and
// returns the resolved query parameters as a [RelQueryParams]. This helper
// allows external packages (such as storage backends) to read the option values
// without needing to access the unexported [relQueryOptions] type directly.
func ApplyRelQueryOpts(opts []RelQueryOpt) RelQueryParams {
	o := &relQueryOptions{}
	for _, opt := range opts {
		opt(o)
	}
	return RelQueryParams{
		RelTypes:     o.relTypes,
		DirectionIn:  o.directionIn,
		DirectionOut: o.directionOut,
		Limit:        o.limit,
	}
}

// TraversalParams holds the resolved parameters from a slice of [TraversalOpt].
type TraversalParams struct {
	RelTypes  []string
	NodeTypes []string
	MaxNodes  int
}

// ApplyTraversalOpts applies a slice of [TraversalOpt] functional options and
// returns the resolved traversal parameters as a [TraversalParams]. This helper
// allows external packages to read the option values without accessing the
// unexported [traversalOptions] type.
func ApplyTraversalOpts(opts []TraversalOpt) TraversalParams {
	o := &traversalOptions{}
	for _, opt := range opts {
		opt(o)
	}
	return TraversalParams{
		RelTypes:  o.relTypes,
		NodeTypes: o.nodeTypes,
		MaxNodes:  o.maxNodes,
	}
}
