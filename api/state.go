package api

import (
	"github.com/stellar/go/clients/horizon"
)

// State contains the full context of the data and saved history
type State struct {
	Context   DataContext
	Transient DataTransient
	History   []Snapshot // descending order, newest values first where history[0] is the currentState
}

// DataContext represents the context needed for basic operations of the bot and never changes throughout the lifecycle of the bot
type DataContext struct {
	Client         *horizon.Client
	AssetBase      horizon.Asset
	AssetQuote     horizon.Asset
	TradingAccount string
	Keys           []string
}

// DataTransient represents the transient data that is used by every strategy which changes on every iteration of the bot's update lifecycle
type DataTransient struct {
	MaxAssetA      float64 // base
	MaxAssetB      float64 // quote
	TrustAssetA    float64
	TrustAssetB    float64
	BuyingAOffers  []horizon.Offer // quoted A/B
	SellingAOffers []horizon.Offer // quoted B/A
}

// Snapshot wraps the data captured at the start and end of a bot's update lifecycle
type Snapshot struct {
	Start map[string]Datum
	End   map[string]Datum
}

// Datum is an interface representing a single unit of data that can be created and updated throughout a bot's update lifecycle.
// The Value here can be a complex data type if needed. You should try to group logical units into the same Datum, such as OHLC for example.
type Datum interface {
	Load() error // reads or loads the data
}
