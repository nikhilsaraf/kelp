package trader

import (
	"fmt"
	"log"
	"sort"
	"time"

	"github.com/lightyeario/kelp/api"
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
		Context: api.DataContext{
			Client:         client,
			AssetBase:      assetBase,
			AssetQuote:     assetQuote,
			TradingAccount: tradingAccount,
			Keys:           strat.DataKeys(),
		},
		Transient: api.DataTransient{},
		History:   []api.Snapshot{},
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
	t.state.History = []api.Snapshot{}
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
	// add a new snapshot element to the history
	t.state.History = append([]api.Snapshot{}, t.state.History...)

	// take the starting snapshot
	e := t.loadData(t.state.History[0].Start)
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
	e = t.loadData(t.state.History[0].End)
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

func (t *Trader) loadData(m map[string]api.Datum) error {
	for _, k := range t.state.Context.Keys {
		if initializedDatum, ok := plugins.InitializedData[k]; ok {
			e := initializedDatum.Load()
			if e != nil {
				return e
			}
			m[k] = initializedDatum
		} else {
			return fmt.Errorf("error: could not find initialized datum for key %s", k)
		}
	}
	return nil
}

// pruneHistory prunes any excess historical values
func (t *Trader) pruneHistory() {
	if t.strat.MaxHistory() > int64(len(t.state.History)) {
		t.state.History = t.state.History[:t.strat.MaxHistory()]
	}
}

// loads the maximum amounts we can offer for each asset along with trust limits
func (t *Trader) loadBalances() error {
	account, e := t.state.Context.Client.LoadAccount(t.state.Context.TradingAccount)
	if e != nil {
		return fmt.Errorf("error loading account: %s", e)
	}

	// load asset data
	var maxA float64
	var maxB float64
	var trustA float64
	var trustB float64
	for _, balance := range account.Balances {
		if utils.AssetsEqual(balance.Asset, t.state.Context.AssetBase) {
			maxA = utils.AmountStringAsFloat(balance.Balance)
			if balance.Asset.Type == utils.Native {
				trustA = maxLumenTrust
			} else {
				trustA = utils.AmountStringAsFloat(balance.Limit)
			}
			log.Printf("maxA=%.7f,trustA=%.7f\n", maxA, trustA)
		} else if utils.AssetsEqual(balance.Asset, t.state.Context.AssetQuote) {
			maxB = utils.AmountStringAsFloat(balance.Balance)
			if balance.Asset.Type == utils.Native {
				trustB = maxLumenTrust
			} else {
				trustB = utils.AmountStringAsFloat(balance.Limit)
			}
			log.Printf("maxB=%.7f,trustB=%.7f\n", maxB, trustB)
		}
	}
	t.state.Transient.MaxAssetA = maxA
	t.state.Transient.MaxAssetB = maxB
	t.state.Transient.TrustAssetA = trustA
	t.state.Transient.TrustAssetB = trustB
	return nil
}

// loads existing offers
func (t *Trader) loadOffers() error {
	offers, e := utils.LoadAllOffers(t.state.Context.TradingAccount, t.state.Context.Client)
	if e != nil {
		return e
	}
	t.state.Transient.SellingAOffers, t.state.Transient.BuyingAOffers = utils.FilterOffers(offers, t.state.Context.AssetBase, t.state.Context.AssetQuote)

	sort.Sort(utils.ByPrice(t.state.Transient.SellingAOffers)) // don't need to reverse here since the prices are inverse
	sort.Sort(utils.ByPrice(t.state.Transient.BuyingAOffers))
	return nil
}
