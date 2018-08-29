package trader

import (
	"log"
	"time"

	"github.com/lightyeario/kelp/api"
	"github.com/lightyeario/kelp/model"
	"github.com/lightyeario/kelp/plugins"
	"github.com/stellar/go/build"
	"github.com/stellar/go/clients/horizon"
)

// Trader represents a market making bot, which is composed of various parts include the strategy and various APIs.
type Trader struct {
	api                 *horizon.Client
	assetBase           horizon.Asset
	assetQuote          horizon.Asset
	tradingAccount      string
	sdex                *plugins.SDEX
	strat               api.Strategy // the instance of this bot is bound to this strategy
	tickIntervalSeconds int32
	dataKey             *model.BotKey

	// uninitialized
	history      []api.State
	currentState api.State

	maxAssetA      float64
	maxAssetB      float64
	trustAssetA    float64
	trustAssetB    float64
	buyingAOffers  []horizon.Offer // quoted A/B
	sellingAOffers []horizon.Offer // quoted B/A
}

// MakeBot is the factory method for the Trader struct
func MakeBot(
	api *horizon.Client,
	assetBase horizon.Asset,
	assetQuote horizon.Asset,
	tradingAccount string,
	sdex *plugins.SDEX,
	strat api.Strategy,
	tickIntervalSeconds int32,
	dataKey *model.BotKey,
) *Trader {
	return &Trader{
		api:                 api,
		assetBase:           assetBase,
		assetQuote:          assetQuote,
		tradingAccount:      tradingAccount,
		sdex:                sdex,
		strat:               strat,
		tickIntervalSeconds: tickIntervalSeconds,
		dataKey:             dataKey,
	}
}

// Start starts the bot with the injected strategy
func (t *Trader) Start() {
	t.history = []api.State{}
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

	dOps = append(dOps, t.sdex.DeleteAllOffers(t.sellingAOffers)...)
	t.sellingAOffers = []horizon.Offer{}
	dOps = append(dOps, t.sdex.DeleteAllOffers(t.buyingAOffers)...)
	t.buyingAOffers = []horizon.Offer{}

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
	t.currentState = t.strat.InitializeState(t.api, t.assetBase, t.assetQuote, t.tradingAccount)

	e := t.currentState.PreUpdate()
	if e != nil {
		log.Println(e)
		t.deleteAllOffers()
		return
	}

	// strategy has a chance to set any state it needs
	e = t.strat.PreUpdate(t.history, t.currentState, t.maxAssetA, t.maxAssetB, t.trustAssetA, t.trustAssetB, t.buyingAOffers, t.sellingAOffers)
	if e != nil {
		log.Println(e)
		t.deleteAllOffers()
		return
	}

	// delete excess offers
	var pruneOps []build.TransactionMutator
	pruneOps, t.buyingAOffers, t.sellingAOffers = t.strat.PruneExistingOffers(t.history, t.currentState, t.buyingAOffers, t.sellingAOffers)
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
	ops, e := t.strat.UpdateWithOps(t.history, t.currentState, t.buyingAOffers, t.sellingAOffers)
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

	e = t.currentState.PostUpdate()
	if e != nil {
		log.Println(e)
		t.deleteAllOffers()
		return
	}
	e = t.strat.PostUpdate(t.history, t.currentState)
	if e != nil {
		log.Println(e)
		t.deleteAllOffers()
		return
	}

	// prepend current state to history here
	t.history = []api.State{t.currentState}
	t.history = append(t.history, t.history[:t.strat.MaxHistory()-1]...)
	t.currentState = nil
}
