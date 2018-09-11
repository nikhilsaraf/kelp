package plugins

import (
	"fmt"

	"github.com/lightyeario/kelp/api"
	"github.com/lightyeario/kelp/model"

	"github.com/lightyeario/kelp/support/utils"
	"github.com/stellar/go/build"
	"github.com/stellar/go/clients/horizon"
	"github.com/stellar/go/support/errors"
)

// composeStrategy is a strategy that is composed of two sub-strategies
type composeStrategy struct {
	assetBase  *horizon.Asset
	assetQuote *horizon.Asset
	buyStrat   api.SideStrategy
	sellStrat  api.SideStrategy
}

// ensure it implements Strategy
var _ api.Strategy = &composeStrategy{}

// makeComposeStrategy is a factory method for composeStrategy
func makeComposeStrategy(
	assetBase *horizon.Asset,
	assetQuote *horizon.Asset,
	buyStrat api.SideStrategy,
	sellStrat api.SideStrategy,
) api.Strategy {
	return &composeStrategy{
		assetBase:  assetBase,
		assetQuote: assetQuote,
		buyStrat:   buyStrat,
		sellStrat:  sellStrat,
	}
}

// DataDependencies impl.
func (s *composeStrategy) DataDependencies() []api.DataKey {
	return append(s.buyStrat.DataDependencies(), s.sellStrat.DataDependencies()...)
}

// MaxHistory impl.
func (s *composeStrategy) MaxHistory() int64 {
	if s.buyStrat.MaxHistory() > s.sellStrat.MaxHistory() {
		return s.buyStrat.MaxHistory()
	}
	return s.sellStrat.MaxHistory()
}

// PruneExistingOffers impl
func (s *composeStrategy) PruneExistingOffers(state *api.State) ([]build.TransactionMutator, []horizon.Offer, []horizon.Offer) {
	pruneOps1, newBuyingAOffers := s.buyStrat.PruneExistingOffers(state)
	pruneOps2, newSellingAOffers := s.sellStrat.PruneExistingOffers(state)
	pruneOps1 = append(pruneOps1, pruneOps2...)
	return pruneOps1, newBuyingAOffers, newSellingAOffers
}

// PreUpdate impl
func (s *composeStrategy) PreUpdate(state *api.State) error {
	// swap assets (base/quote) for buying strategy
	e1 := s.buyStrat.PreUpdate(state)
	// assets maintain same ordering for selling
	e2 := s.sellStrat.PreUpdate(state)

	if e1 == nil && e2 == nil {
		return nil
	}

	if e1 != nil && e2 != nil {
		return fmt.Errorf("errors on both sides: buying (= %s) and selling (= %s)", e1, e2)
	}

	if e1 != nil {
		return errors.Wrap(e1, "error in buying sub-strategy")
	}
	return errors.Wrap(e2, "error in selling sub-strategy")
}

// UpdateWithOps impl
func (s *composeStrategy) UpdateWithOps(state *api.State) ([]build.TransactionMutator, error) {
	// buy side, flip newTopBuyPrice because it will be inverted from this parent strategy's context of base/quote
	buyOps, newTopBuyPriceInverted, e1 := s.buyStrat.UpdateWithOps(state)
	newTopBuyPrice := model.InvertNumber(newTopBuyPriceInverted)
	// sell side
	sellOps, _, e2 := s.sellStrat.UpdateWithOps(state)

	// check for errors
	ops := []build.TransactionMutator{}
	if e1 != nil && e2 != nil {
		return ops, fmt.Errorf("errors on both sides: buying (= %s) and selling (= %s)", e1, e2)
	} else if e1 != nil {
		return ops, errors.Wrap(e1, "error in buying sub-strategy")
	} else if e2 != nil {
		return ops, errors.Wrap(e2, "error in selling sub-strategy")
	}

	// combine ops correctly based on possible crossing offers
	allOffers := (*state.Transient)[DataKeyOffers].(*DatumOffers)
	if newTopBuyPrice != nil && len(allOffers.SellingAOffers) > 0 && newTopBuyPrice.AsFloat() >= utils.PriceAsFloat(allOffers.SellingAOffers[0].Price) {
		ops = append(ops, sellOps...)
		ops = append(ops, buyOps...)
	} else {
		ops = append(ops, buyOps...)
		ops = append(ops, sellOps...)
	}
	return ops, nil
}

// PostUpdate impl
func (s *composeStrategy) PostUpdate(state *api.State) error {
	return nil
}
