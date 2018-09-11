package plugins

import (
	"fmt"
	"log"
	"math"

	"github.com/lightyeario/kelp/api"
	"github.com/lightyeario/kelp/model"
	"github.com/lightyeario/kelp/support/utils"
	"github.com/stellar/go/build"
	"github.com/stellar/go/clients/horizon"
)

// sellSideStrategy is a strategy to sell a specific currency on SDEX on a single side by reading prices from an exchange
type sellSideStrategy struct {
	sdex            *SDEX
	assetBase       *horizon.Asset
	assetQuote      *horizon.Asset
	levelsProvider  api.LevelProvider
	priceTolerance  float64
	amountTolerance float64
	isBuySide       bool

	// uninitialized
	currentLevels []api.Level // levels for current iteration
	maxAssetBase  float64
	maxAssetQuote float64
}

// ensure it implements SideStrategy
var _ api.SideStrategy = &sellSideStrategy{}

// makeSellSideStrategy is a factory method for sellSideStrategy
func makeSellSideStrategy(
	sdex *SDEX,
	assetBase *horizon.Asset,
	assetQuote *horizon.Asset,
	levelsProvider api.LevelProvider,
	priceTolerance float64,
	amountTolerance float64,
	isBuySide bool,
) api.SideStrategy {
	return &sellSideStrategy{
		sdex:            sdex,
		assetBase:       assetBase,
		assetQuote:      assetQuote,
		levelsProvider:  levelsProvider,
		priceTolerance:  priceTolerance,
		amountTolerance: amountTolerance,
		isBuySide:       isBuySide,
	}
}

// DataDependencies impl.
func (s *sellSideStrategy) DataDependencies() []api.DataKey {
	return []api.DataKey{DataKeyOffers, DataKeyBalances}
}

// MaxHistory impl.
func (s *sellSideStrategy) MaxHistory() int64 {
	return 0
}

// PruneExistingOffers impl
func (s *sellSideStrategy) PruneExistingOffers(state *api.State) ([]build.TransactionMutator, []horizon.Offer) {
	allOffers := *(*state.Transient)[DataKeyOffers].(*DatumOffers)
	var offers []horizon.Offer
	if s.isBuySide {
		offers = allOffers.BuyingAOffers
	} else {
		offers = allOffers.SellingAOffers
	}

	pruneOps := []build.TransactionMutator{}
	for i := len(s.currentLevels); i < len(offers); i++ {
		pOp := s.sdex.DeleteOffer(offers[i])
		pruneOps = append(pruneOps, &pOp)
	}
	if len(offers) > len(s.currentLevels) {
		offers = offers[:len(s.currentLevels)]
	}
	return pruneOps, offers
}

// PreUpdate impl
func (s *sellSideStrategy) PreUpdate(state *api.State) error {
	// pull data out of the transient state
	allBalances := *(*state.Transient)[DataKeyBalances].(*DatumBalances)
	if maxAssetBase, ok := allBalances.Balance[state.Context.AssetBase]; ok {
		s.maxAssetBase = maxAssetBase
	} else {
		return fmt.Errorf("framework error: balance for the base asset was not found in the Transient state")
	}
	if maxAssetQuote, ok := allBalances.Balance[state.Context.AssetQuote]; ok {
		s.maxAssetQuote = maxAssetQuote
	} else {
		return fmt.Errorf("framework error: balance for the quote asset was not found in the Transient state")
	}
	trustQuote, ok := allBalances.Trust[state.Context.AssetQuote]
	if !ok {
		return fmt.Errorf("framework error: trust value for the quote asset was not found in the Transient state")
	}

	// don't place orders if we have nothing to sell or if we cannot buy the asset in exchange
	nothingToSell := s.maxAssetBase == 0
	lineFull := s.maxAssetQuote == trustQuote
	if nothingToSell || lineFull {
		s.currentLevels = []api.Level{}
		log.Printf("no capacity to place sell orders (nothingToSell = %v, lineFull = %v)\n", nothingToSell, lineFull)
		return nil
	}

	// load currentLevels only once here
	var e error
	s.currentLevels, e = s.levelsProvider.GetLevels(s.maxAssetBase, s.maxAssetQuote)
	if e != nil {
		log.Printf("levels couldn't be loaded: %s\n", e)
		return e
	}
	return nil
}

// UpdateWithOps impl
func (s *sellSideStrategy) UpdateWithOps(history []api.State, currentState api.State, offers []horizon.Offer) (ops []build.TransactionMutator, newTopOffer *model.Number, e error) {
	newTopOffer = nil
	for i := len(s.currentLevels) - 1; i >= 0; i-- {
		op := s.updateSellLevel(offers, i)
		if op != nil {
			offer, e := model.NumberFromString(op.MO.Price.String(), 7)
			if e != nil {
				return nil, nil, e
			}

			// newTopOffer is minOffer because this is a sell strategy, and the lowest price is the best (top) price on the orderbook
			if newTopOffer == nil || offer.AsFloat() < newTopOffer.AsFloat() {
				newTopOffer = offer
			}

			ops = append(ops, op)
		}
	}
	return ops, newTopOffer, nil
}

// PostUpdate impl
func (s *sellSideStrategy) PostUpdate(history []api.State, currentState api.State) error {
	return nil
}

// Selling Base
func (s *sellSideStrategy) updateSellLevel(offers []horizon.Offer, index int) *build.ManageOfferBuilder {
	targetPrice := s.currentLevels[index].Price
	targetAmount := s.currentLevels[index].Amount
	if s.isBuySide {
		targetAmount = *model.NumberFromFloat(targetAmount.AsFloat()/targetPrice.AsFloat(), targetAmount.Precision())
	}
	targetAmount = *model.NumberFromFloat(math.Min(targetAmount.AsFloat(), s.maxAssetBase), targetAmount.Precision())

	if len(offers) <= index {
		if targetPrice.Precision() > utils.SdexPrecision {
			targetPrice = *model.NumberFromFloat(targetPrice.AsFloat(), utils.SdexPrecision)
		}
		if targetAmount.Precision() > utils.SdexPrecision {
			targetAmount = *model.NumberFromFloat(targetAmount.AsFloat(), utils.SdexPrecision)
		}
		// no existing offer at this index
		log.Printf("sell,create,p=%.7f,a=%.7f\n", targetPrice.AsFloat(), targetAmount.AsFloat())
		return s.sdex.CreateSellOffer(*s.assetBase, *s.assetQuote, targetPrice.AsFloat(), targetAmount.AsFloat())
	}

	highestPrice := targetPrice.AsFloat() + targetPrice.AsFloat()*s.priceTolerance
	lowestPrice := targetPrice.AsFloat() - targetPrice.AsFloat()*s.priceTolerance
	minAmount := targetAmount.AsFloat() - targetAmount.AsFloat()*s.amountTolerance
	maxAmount := targetAmount.AsFloat() + targetAmount.AsFloat()*s.amountTolerance

	//check if existing offer needs to be modified
	curPrice := utils.GetPrice(offers[index])
	curAmount := utils.AmountStringAsFloat(offers[index].Amount)

	// existing offer not within tolerances
	priceTrigger := (curPrice > highestPrice) || (curPrice < lowestPrice)
	amountTrigger := (curAmount < minAmount) || (curAmount > maxAmount)
	if priceTrigger || amountTrigger {
		if targetPrice.Precision() > utils.SdexPrecision {
			targetPrice = *model.NumberFromFloat(targetPrice.AsFloat(), utils.SdexPrecision)
		}
		if targetAmount.Precision() > utils.SdexPrecision {
			targetAmount = *model.NumberFromFloat(targetAmount.AsFloat(), utils.SdexPrecision)
		}
		log.Printf("sell,modify,tp=%.7f,ta=%.7f,curPrice=%.7f,highPrice=%.7f,lowPrice=%.7f,curAmt=%.7f,minAmt=%.7f,maxAmt=%.7f\n",
			targetPrice.AsFloat(), targetAmount.AsFloat(), curPrice, highestPrice, lowestPrice, curAmount, minAmount, maxAmount)
		return s.sdex.ModifySellOffer(offers[index], targetPrice.AsFloat(), targetAmount.AsFloat())
	}
	return nil
}
