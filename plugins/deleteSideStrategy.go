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
}

// ensure it implements SideStrategy
var _ api.SideStrategy = &deleteSideStrategy{}

// makeDeleteSideStrategy is a factory method for deleteSideStrategy
func makeDeleteSideStrategy(
	sdex *SDEX,
	assetBase *horizon.Asset,
	assetQuote *horizon.Asset,
) api.SideStrategy {
	return &deleteSideStrategy{
		sdex:       sdex,
		assetBase:  assetBase,
		assetQuote: assetQuote,
	}
}

// PruneExistingOffers impl
func (s *deleteSideStrategy) PruneExistingOffers(history []api.State, currentState api.State, offers []horizon.Offer) ([]build.TransactionMutator, []horizon.Offer) {
	log.Printf("deleteSideStrategy: deleting %d offers\n", len(offers))
	pruneOps := []build.TransactionMutator{}
	for i := 0; i < len(offers); i++ {
		pOp := s.sdex.DeleteOffer(offers[i])
		pruneOps = append(pruneOps, &pOp)
	}
	return pruneOps, []horizon.Offer{}
}

// PreUpdate impl
func (s *deleteSideStrategy) PreUpdate(history []api.State, currentState api.State, maxAssetBase float64, maxAssetQuote float64, trustBase float64, trustQuote float64, buyingAOffers []horizon.Offer, sellingAOffers []horizon.Offer) error {
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
