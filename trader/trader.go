package trader

import (
	"log"
	"sort"
	"time"

	"github.com/lightyeario/kelp/api"
	"github.com/lightyeario/kelp/model"
	"github.com/lightyeario/kelp/plugins"
	"github.com/lightyeario/kelp/support/utils"
	"github.com/stellar/go/build"
	"github.com/stellar/go/clients/horizon"
)

const maxLumenTrust float64 = 100000000000

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
	t.currentState = t.strat.InitializeState(t.api, t.assetBase, t.assetQuote, t.tradingAccount)

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
	var e error
	t.load()
	t.loadExistingOffers()

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

	e = t.strat.PostUpdate(t.history, t.currentState)
	if e != nil {
		log.Println(e)
		t.deleteAllOffers()
		return
	}
}

func (t *Trader) load() {
	// load the maximum amounts we can offer for each asset
	account, e := t.api.LoadAccount(t.tradingAccount)
	if e != nil {
		log.Println(e)
		return
	}

	var maxA float64
	var maxB float64
	var trustA float64
	var trustB float64
	for _, balance := range account.Balances {
		if utils.AssetsEqual(balance.Asset, t.assetBase) {
			maxA = utils.AmountStringAsFloat(balance.Balance)
			if balance.Asset.Type == utils.Native {
				trustA = maxLumenTrust
			} else {
				trustA = utils.AmountStringAsFloat(balance.Limit)
			}
			log.Printf("maxA=%.7f,trustA=%.7f\n", maxA, trustA)
		} else if utils.AssetsEqual(balance.Asset, t.assetQuote) {
			maxB = utils.AmountStringAsFloat(balance.Balance)
			if balance.Asset.Type == utils.Native {
				trustB = maxLumenTrust
			} else {
				trustB = utils.AmountStringAsFloat(balance.Limit)
			}
			log.Printf("maxB=%.7f,trustB=%.7f\n", maxB, trustB)
		}
	}
	t.maxAssetA = maxA
	t.maxAssetB = maxB
	t.trustAssetA = trustA
	t.trustAssetB = trustB
}

func (t *Trader) loadExistingOffers() {
	offers, e := utils.LoadAllOffers(t.tradingAccount, t.api)
	if e != nil {
		log.Println(e)
		return
	}
	t.sellingAOffers, t.buyingAOffers = utils.FilterOffers(offers, t.assetBase, t.assetQuote)

	sort.Sort(utils.ByPrice(t.buyingAOffers))
	sort.Sort(utils.ByPrice(t.sellingAOffers)) // don't need to reverse since the prices are inverse
}
