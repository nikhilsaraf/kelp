package plugins

import (
	"log"

	"github.com/lightyeario/kelp/api"
	"github.com/lightyeario/kelp/model"
	"github.com/stellar/go/build"
	"github.com/stellar/go/clients/horizon"
)

// deleteSideStrategy is a sideStrategy to delete the orders for a given currency pair on one side of the orderbook
type deleteSideStrategy struct {
	sdex       *SDEX
	assetBase  *horizon.Asset
	assetQuote *horizon.Asset
	isBuySide  bool
}

// ensure it implements SideStrategy
var _ api.SideStrategy = &deleteSideStrategy{}

// makeDeleteSideStrategy is a factory method for deleteSideStrategy
func makeDeleteSideStrategy(
	sdex *SDEX,
	assetBase *horizon.Asset,
	assetQuote *horizon.Asset,
	isBuySide bool,
) api.SideStrategy {
	return &deleteSideStrategy{
		sdex:       sdex,
		assetBase:  assetBase,
		assetQuote: assetQuote,
		isBuySide:  isBuySide,
	}
}

// DataDependencies impl.
func (s *deleteSideStrategy) DataDependencies() []api.DataKey {
	return []api.DataKey{DataKeyOffers}
}

// MaxHistory impl.
func (s *deleteSideStrategy) MaxHistory() int64 {
	return 0
}

// PruneExistingOffers impl
func (s *deleteSideStrategy) PruneExistingOffers(state *api.State) ([]build.TransactionMutator, []horizon.Offer) {
	allOffers := *(*state.Transient)[DataKeyOffers].(*DatumOffers)
	var offers []horizon.Offer
	if s.isBuySide {
		offers = allOffers.BuyingAOffers
	} else {
		offers = allOffers.SellingAOffers
	}

	log.Printf("deleteSideStrategy: deleting %d offers\n", len(offers))
	pruneOps := []build.TransactionMutator{}
	for i := 0; i < len(offers); i++ {
		pOp := s.sdex.DeleteOffer(offers[i])
		pruneOps = append(pruneOps, &pOp)
	}
	return pruneOps, []horizon.Offer{}
}

// PreUpdate impl
func (s *deleteSideStrategy) PreUpdate(state *api.State) error {
	return nil
}

// UpdateWithOps impl
func (s *deleteSideStrategy) UpdateWithOps(history []api.State, currentState api.State, offers []horizon.Offer) (ops []build.TransactionMutator, newTopOffer *model.Number, e error) {
	return []build.TransactionMutator{}, nil, nil
}

// PostUpdate impl
func (s *deleteSideStrategy) PostUpdate(history []api.State, currentState api.State) error {
	return nil
}
