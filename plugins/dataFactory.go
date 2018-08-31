package plugins

import (
	"sort"

	"github.com/lightyeario/kelp/api"
	"github.com/lightyeario/kelp/support/utils"
	"github.com/stellar/go/clients/horizon"
)

// Constants for the keys to InitializedData
const (
	DataKeyOffers api.DataKey = iota
)

// InitializedData holds the initialized data objects for the full repository of data fields supported
var InitializedData = map[api.DataKey]api.Datum{
	DataKeyOffers: defaultDatumOffers,
}

// MakeDataKeysDag will return an ordered list of data keys including dependencies of the keys provided.
// The ordering of the returned keys guarantees that the dependencies of every data type has been loaded before itself.
// This is basically a depth first search
func MakeDataKeysDag(input []api.DataKey) []api.DataKey {
	return dagHelper(input, map[api.DataKey]bool{})
}

// dagHelper is a recursive helper to MakeDataKeysDag
func dagHelper(input []api.DataKey, traversed map[api.DataKey]bool) []api.DataKey {
	if len(input) == 0 {
		return []api.DataKey{}
	}

	output := []api.DataKey{}
	for _, key := range input {
		if _, ok := traversed[key]; ok {
			continue
		}

		traversed[key] = true
		initializedDatum := InitializedData[key]
		output = append(output, dagHelper(initializedDatum.DirectDependencies(), traversed)...)
		output = append(output, key)
	}
	return output
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
func (d *DatumOffers) Load(context *api.DataContext, snapshot map[api.DataKey]api.Datum) error {
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
