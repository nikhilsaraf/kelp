package api

import (
	"github.com/lightyeario/kelp/model"
	"github.com/stellar/go/build"
	"github.com/stellar/go/clients/horizon"
)

// Strategy represents some logic for a bot to follow while doing market making
type Strategy interface {
	DataKeys() []string
	MaxHistory() int64
	PruneExistingOffers(state *State) ([]build.TransactionMutator, []horizon.Offer, []horizon.Offer)
	PreUpdate(state *State) error
	UpdateWithOps(state *State) ([]build.TransactionMutator, error)
	PostUpdate(state *State) error
}

// SideStrategy represents a strategy on a single side of the orderbook
type SideStrategy interface {
	PruneExistingOffers(state *State) ([]build.TransactionMutator, []horizon.Offer)
	PreUpdate(state *State) error
	UpdateWithOps(state *State) (ops []build.TransactionMutator, newTopOffer *model.Number, e error)
	PostUpdate(state *State) error
}
