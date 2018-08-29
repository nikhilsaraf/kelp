package api

import (
	"github.com/lightyeario/kelp/model"
	"github.com/stellar/go/build"
	"github.com/stellar/go/clients/horizon"
)

// Strategy represents some logic for a bot to follow while doing market making
type Strategy interface {
	InitializeState(api *horizon.Client, assetBase horizon.Asset, assetQuote horizon.Asset, tradingAccount string) State
	MaxHistory() int64
	PruneExistingOffers(history []State, currentState State, buyingAOffers []horizon.Offer, sellingAOffers []horizon.Offer) ([]build.TransactionMutator, []horizon.Offer, []horizon.Offer)
	PreUpdate(history []State, currentState State, maxAssetA float64, maxAssetB float64, trustA float64, trustB float64, buyingAOffers []horizon.Offer, sellingAOffers []horizon.Offer) error
	UpdateWithOps(history []State, currentState State, buyingAOffers []horizon.Offer, sellingAOffers []horizon.Offer) ([]build.TransactionMutator, error)
	PostUpdate(history []State, currentState State) error
}

// SideStrategy represents a strategy on a single side of the orderbook
type SideStrategy interface {
	PruneExistingOffers(history []State, currentState State, offers []horizon.Offer) ([]build.TransactionMutator, []horizon.Offer)
	PreUpdate(history []State, currentState State, maxAssetA float64, maxAssetB float64, trustA float64, trustB float64, buyingAOffers []horizon.Offer, sellingAOffers []horizon.Offer) error
	UpdateWithOps(history []State, currentState State, offers []horizon.Offer) (ops []build.TransactionMutator, newTopOffer *model.Number, e error)
	PostUpdate(history []State, currentState State) error
}

// State is an interface that manages data to be stored between update cycles
type State interface {
	PreUpdate() error
	PostUpdate() error
}
