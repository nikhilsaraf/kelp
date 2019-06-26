package kelpos

// Inputs is the inputs needed to start a trade command
type Inputs struct {
	BotConfigPath                 *string
	Strategy                      *string
	StratConfigPath               *string
	OperationalBuffer             *float64
	OperationalBufferNonNativePct *float64
	WithIPC                       *bool
	SimMode                       *bool
	LogPrefix                     *string
	FixedIterations               *uint64
	NoHeaders                     *bool
}
