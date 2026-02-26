package memory

// ApplyRelQueryOpts applies a slice of [RelQueryOpt] functional options and
// returns the resolved query parameters. This helper allows external packages
// (such as storage backends) to read the option values without needing to
// access the unexported [relQueryOptions] type directly.
func ApplyRelQueryOpts(opts []RelQueryOpt) (relTypes []string, dirIn bool, dirOut bool, limit int) {
	o := &relQueryOptions{}
	for _, opt := range opts {
		opt(o)
	}
	return o.relTypes, o.directionIn, o.directionOut, o.limit
}

// ApplyTraversalOpts applies a slice of [TraversalOpt] functional options and
// returns the resolved traversal parameters. This helper allows external packages
// to read the option values without accessing the unexported [traversalOptions] type.
func ApplyTraversalOpts(opts []TraversalOpt) (relTypes []string, nodeTypes []string, maxNodes int) {
	o := &traversalOptions{}
	for _, opt := range opts {
		opt(o)
	}
	return o.relTypes, o.nodeTypes, o.maxNodes
}
