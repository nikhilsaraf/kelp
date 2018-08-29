package plugins

import (
	"fmt"
	"log"
	"sort"

	"github.com/lightyeario/kelp/api"
	"github.com/lightyeario/kelp/support/utils"
	"github.com/stellar/go/clients/horizon"
)

const maxLumenTrust float64 = 100000000000

type basicState struct {
	api            *horizon.Client
	assetBase      horizon.Asset
	assetQuote     horizon.Asset
	tradingAccount string

	// uninitialized
	startData *data
	endData   *data
}

type data struct {
	maxAssetA      float64
	maxAssetB      float64
	trustAssetA    float64
	trustAssetB    float64
	buyingAOffers  []horizon.Offer // quoted A/B
	sellingAOffers []horizon.Offer // quoted B/A
}

// MakeBasicState is the initialization function to be used for the basic state
func MakeBasicState(
	api *horizon.Client,
	assetBase horizon.Asset,
	assetQuote horizon.Asset,
	tradingAccount string,
) (api.State, error) {
	s := &basicState{
		api:            api,
		assetBase:      assetBase,
		assetQuote:     assetQuote,
		tradingAccount: tradingAccount,
	}
	return s, nil
}

var _ api.State = &basicState{}

// PreUpdate implementation
func (s *basicState) PreUpdate() error {
	var e error
	s.startData, e = s.load()
	return e
}

// PostUpdate implementation
func (s *basicState) PostUpdate() error {
	var e error
	s.endData, e = s.load()
	return e
}

func (s *basicState) load() (*data, error) {
	// load the maximum amounts we can offer for each asset
	account, e := s.api.LoadAccount(s.tradingAccount)
	if e != nil {
		return nil, fmt.Errorf("error loading account in basicState's PreUpdate: %s", e)
	}
	d := &data{}

	// load asset data
	var maxA float64
	var maxB float64
	var trustA float64
	var trustB float64
	for _, balance := range account.Balances {
		if utils.AssetsEqual(balance.Asset, s.assetBase) {
			maxA = utils.AmountStringAsFloat(balance.Balance)
			if balance.Asset.Type == utils.Native {
				trustA = maxLumenTrust
			} else {
				trustA = utils.AmountStringAsFloat(balance.Limit)
			}
			log.Printf("maxA=%.7f,trustA=%.7f\n", maxA, trustA)
		} else if utils.AssetsEqual(balance.Asset, s.assetQuote) {
			maxB = utils.AmountStringAsFloat(balance.Balance)
			if balance.Asset.Type == utils.Native {
				trustB = maxLumenTrust
			} else {
				trustB = utils.AmountStringAsFloat(balance.Limit)
			}
			log.Printf("maxB=%.7f,trustB=%.7f\n", maxB, trustB)
		}
	}
	d.maxAssetA = maxA
	d.maxAssetB = maxB
	d.trustAssetA = trustA
	d.trustAssetB = trustB

	// load existing offers
	offers, e := utils.LoadAllOffers(s.tradingAccount, s.api)
	if e != nil {
		return nil, e
	}
	d.sellingAOffers, d.buyingAOffers = utils.FilterOffers(offers, s.assetBase, s.assetQuote)

	sort.Sort(utils.ByPrice(d.sellingAOffers)) // don't need to reverse here since the prices are inverse
	sort.Sort(utils.ByPrice(d.buyingAOffers))
	return d, nil
}
