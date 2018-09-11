package plugins

import (
	"github.com/lightyeario/kelp/api"
	"github.com/stellar/go/clients/horizon"
)

// makeDeleteStrategy is a factory method
func makeDeleteStrategy(
	sdex *SDEX,
	assetBase *horizon.Asset,
	assetQuote *horizon.Asset,
) api.Strategy {
	return makeComposeStrategy(
		assetBase,
		assetQuote,
		makeDeleteSideStrategy(sdex, assetQuote, assetBase, true), // switch sides of base/quote here for the buy side
		makeDeleteSideStrategy(sdex, assetBase, assetQuote, false),
	)
}
