package plugins

import (
	"fmt"
	"log"
	"sort"

	"github.com/lightyeario/kelp/api"
	"github.com/lightyeario/kelp/support/utils"
	"github.com/stellar/go/clients/horizon"
)

// Constants for the keys to InitializedData
const (
	DataKeyOffers api.DataKey = iota
	DataKeyBalances
)
const maxLumenTrust float64 = 100000000000

// InitializedData holds the initialized data objects for the full repository of data fields supported
var InitializedData = map[api.DataKey]api.Datum{
	DataKeyOffers:   defaultDatumOffers,
	DataKeyBalances: defaultDatumBalances,
}

// MakeDataDependenciesDag will return an ordered list of data keys including dependencies of the keys provided.
// The ordering of the returned keys guarantees that the dependencies of every data type has been loaded before itself.
// This is basically a depth first search
func MakeDataDependenciesDag(input []api.DataKey) []api.DataKey {
	return dagHelper(input, map[api.DataKey]bool{}, InitializedData)
}

// dagHelper is a recursive helper to MakeDataDependenciesDag
func dagHelper(input []api.DataKey, traversed map[api.DataKey]bool, dataMap map[api.DataKey]api.Datum) []api.DataKey {
	if len(input) == 0 {
		return []api.DataKey{}
	}

	output := []api.DataKey{}
	for _, key := range input {
		if _, ok := traversed[key]; ok {
			continue
		}

		traversed[key] = true
		output = append(output, dagHelper(dataMap[key].DirectDependencies(), traversed, dataMap)...)
		output = append(output, key)
	}
	return output
}

// CopySnapshot returns a non-nil copy of the snapshot passed in
func CopySnapshot(snapshot api.Snapshot) *api.Snapshot {
	snapshotCopy := api.Snapshot{}
	for k, v := range snapshot {
		snapshotCopy[k] = v
	}
	return &snapshotCopy
}

/****************************** DATA TYPES BELOW THIS LINE ******************************/

// DatumOffers provides the offers on the account broken up by buying and selling offers
type DatumOffers struct {
	BuyingAOffers  []horizon.Offer // quoted A/B
	SellingAOffers []horizon.Offer // quoted B/A
}

var defaultDatumOffers api.Datum = &DatumOffers{}

// DirectDependencies impl.
func (d *DatumOffers) DirectDependencies() []api.DataKey {
	return []api.DataKey{}
}

// Load loads the offers for the given account
func (d *DatumOffers) Load(context *api.DataContext, snapshot *api.Snapshot) error {
	offers, e := utils.LoadAllOffers(context.TradingAccount, context.Client)
	if e != nil {
		return e
	}
	sellingOffers, buyingOffers := utils.FilterOffers(offers, context.AssetBase, context.AssetQuote)

	sort.Sort(utils.ByPrice(sellingOffers)) // don't need to reverse here since the prices are inverse
	sort.Sort(utils.ByPrice(buyingOffers))

	d.SellingAOffers = sellingOffers
	d.BuyingAOffers = buyingOffers
	return nil
}

// DatumBalances contains the balances on an account
type DatumBalances struct {
	Balance map[horizon.Asset]float64
	Trust   map[horizon.Asset]float64
}

var defaultDatumBalances api.Datum = &DatumBalances{}

// DirectDependencies impl.
func (d *DatumBalances) DirectDependencies() []api.DataKey {
	return []api.DataKey{DataKeyOffers}
}

// Load loads the maximum amounts we can offer for each asset along with trust limits
func (d *DatumBalances) Load(context *api.DataContext, snapshot *api.Snapshot) error {
	account, e := context.Client.LoadAccount(context.TradingAccount)
	if e != nil {
		return fmt.Errorf("error loading account: %s", e)
	}

	// load asset data
	var maxA float64
	var maxB float64
	var trustA float64
	var trustB float64
	for _, balance := range account.Balances {
		if utils.AssetsEqual(balance.Asset, context.AssetBase) {
			maxA = utils.AmountStringAsFloat(balance.Balance)
			if balance.Asset.Type == utils.Native {
				trustA = maxLumenTrust
			} else {
				trustA = utils.AmountStringAsFloat(balance.Limit)
			}
			log.Printf("maxA=%.7f,trustA=%.7f\n", maxA, trustA)
		} else if utils.AssetsEqual(balance.Asset, context.AssetQuote) {
			maxB = utils.AmountStringAsFloat(balance.Balance)
			if balance.Asset.Type == utils.Native {
				trustB = maxLumenTrust
			} else {
				trustB = utils.AmountStringAsFloat(balance.Limit)
			}
			log.Printf("maxB=%.7f,trustB=%.7f\n", maxB, trustB)
		}
	}
	d.Balance = map[horizon.Asset]float64{
		context.AssetBase:  maxA,
		context.AssetQuote: maxB,
	}
	d.Trust = map[horizon.Asset]float64{
		context.AssetBase:  trustA,
		context.AssetQuote: trustB,
	}
	return nil
}
