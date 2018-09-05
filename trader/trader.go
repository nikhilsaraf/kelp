package trader

import (
	"log"
	"time"

	"github.com/lightyeario/kelp/api"
	"github.com/lightyeario/kelp/plugins"
	"github.com/stellar/go/build"
	"github.com/stellar/go/clients/horizon"
)

// these data keys are needed by the trader bot
var defaultDataKeys = []api.DataKey{
	plugins.DataKeyOffers,
	plugins.DataKeyBalances,
}

// Trader represents a market making bot, which is composed of various parts include the strategy and various APIs.
type Trader struct {
	api                 *horizon.Client
	assetBase           horizon.Asset
	assetQuote          horizon.Asset
	tradingAccount      string
	sdex                *plugins.SDEX
	strat               api.Strategy // the instance of this bot is bound to this strategy
	tickIntervalSeconds int32
	state               *api.State
}

// MakeBot is the factory method for the Trader struct
func MakeBot(
	client *horizon.Client,
	assetBase horizon.Asset,
	assetQuote horizon.Asset,
	tradingAccount string,
	sdex *plugins.SDEX,
	strat api.Strategy,
	tickIntervalSeconds int32,
) *Trader {
	state := &api.State{
		Context: &api.DataContext{
			Client:         client,
			AssetBase:      assetBase,
			AssetQuote:     assetQuote,
			TradingAccount: tradingAccount,
			Keys:           plugins.MakeDataKeysDag(append(defaultDataKeys, strat.DataKeys())),
		},
		Transient: api.DataTransient{},
		History:   []api.Snapshots{},
	}

	return &Trader{
		api:                 client,
		assetBase:           assetBase,
		assetQuote:          assetQuote,
		tradingAccount:      tradingAccount,
		sdex:                sdex,
		strat:               strat,
		tickIntervalSeconds: tickIntervalSeconds,
		state:               state,
	}
}

// Start starts the bot with the injected strategy
func (t *Trader) Start() {
	t.state.History = []api.Snapshots{}
	for {
		log.Println("----------------------------------------------------------------------------------------------------")
		t.update()
		log.Printf("sleeping for %d seconds...\n", t.tickIntervalSeconds)
		time.Sleep(time.Duration(t.tickIntervalSeconds) * time.Second)
	}
}

// deletes all offers for the bot (not all offers on the account)
func (t *Trader) deleteAllOffers() {
	dOps := []build.TransactionMutator{}

	dOps = append(dOps, t.sdex.DeleteAllOffers(t.state.Transient.SellingAOffers)...)
	t.state.Transient.SellingAOffers = []horizon.Offer{}
	dOps = append(dOps, t.sdex.DeleteAllOffers(t.state.Transient.BuyingAOffers)...)
	t.state.Transient.BuyingAOffers = []horizon.Offer{}

	log.Printf("created %d operations to delete offers\n", len(dOps))
	if len(dOps) > 0 {
		e := t.sdex.SubmitOps(dOps)
		if e != nil {
			log.Println(e)
			return
		}
	}
}

// time to update the order book and possibly readjust the offers
func (t *Trader) update() {
	// add a new snapshots element to the history
	t.state.History = append([]api.Snapshots{}, t.state.History...)

	// take the starting snapshot
	e := t.snapshot(t.state.History[0].Start)
	if e != nil {
		t.deleteAllOffers()
		return
	}

	// strategy has a chance to set any state it needs
	e = t.strat.PreUpdate(t.state)
	if e != nil {
		log.Println(e)
		t.deleteAllOffers()
		return
	}

	// delete excess offers
	var pruneOps []build.TransactionMutator
	pruneOps, t.state.Transient.BuyingAOffers, t.state.Transient.SellingAOffers = t.strat.PruneExistingOffers(t.state)
	log.Printf("created %d operations to prune excess offers\n", len(pruneOps))
	if len(pruneOps) > 0 {
		e = t.sdex.SubmitOps(pruneOps)
		if e != nil {
			log.Println(e)
			t.deleteAllOffers()
			return
		}
	}

	// reset cached xlm exposure here so we only compute it once per update
	// TODO 2 - calculate this here and pass it in
	t.sdex.ResetCachedXlmExposure()
	ops, e := t.strat.UpdateWithOps(t.state)
	if e != nil {
		log.Println(e)
		t.deleteAllOffers()
		return
	}

	log.Printf("created %d operations to update existing offers\n", len(ops))
	if len(ops) > 0 {
		e = t.sdex.SubmitOps(ops)
		if e != nil {
			log.Println(e)
			t.deleteAllOffers()
			return
		}
	}

	// take the end snapshot
	e = t.snapshot(t.state.History[0].End)
	if e != nil {
		t.deleteAllOffers()
		return
	}
	e = t.strat.PostUpdate(t.state)
	if e != nil {
		log.Println(e)
		t.deleteAllOffers()
		return
	}
	t.pruneHistory()
}

// snapshot takes the snapshot into the passed in map
func (t *Trader) snapshot(snapshot map[api.DataKey]api.Datum) error {
	for _, k := range t.state.Context.Keys {
		initializedDatum := plugins.InitializedData[k]
		// loading a datum would need the context to perform the load and the snapshot data to get anything it depends on
		e := initializedDatum.Load(t.state.Context, snapshot)
		if e != nil {
			return e
		}
		snapshot[k] = initializedDatum
	}
	return nil
}

// pruneHistory prunes any excess historical values
func (t *Trader) pruneHistory() {
	if t.strat.MaxHistory() > int64(len(t.state.History)) {
		t.state.History = t.state.History[:t.strat.MaxHistory()]
	}
}
