package api

import (
	"github.com/stellar/go/clients/horizon"
)

// State contains the full context of the data and saved history
type State struct {
	Context   *DataContext
	Transient *Snapshot
	History   []Snapshots // descending order, newest values first where history[0] is the currentState
}

// DataKey is the key type for the data stored in a Snapshot
type DataKey uint8

// DataContext represents the context needed for basic operations of the bot and never changes throughout the lifecycle of the bot
type DataContext struct {
	Client         *horizon.Client
	AssetBase      horizon.Asset
	AssetQuote     horizon.Asset
	TradingAccount string
	Keys           []DataKey
}

// Snapshot represents the historical data at a single point in time
type Snapshot map[DataKey]Datum

// Snapshots wraps the data captured at the start and end of a bot's update lifecycle
type Snapshots struct {
	Start Snapshot
	End   Snapshot
}

// Datum is an interface representing a single unit of data that can be created and updated throughout a bot's update lifecycle.
// The Value here can be a complex data type if needed. You should try to group logical units into the same Datum, such as OHLC for example.
type Datum interface {
	DirectDependencies() []DataKey                       // lists the data that this datum is directly dependent on (example, EMA is dependent on OHLC)
	Load(context *DataContext, snapshot *Snapshot) error // reads or loads the data
}
