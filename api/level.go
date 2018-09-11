package api

import (
	"github.com/lightyeario/kelp/model"
)

// Level represents a layer in the orderbook
type Level struct {
	Price  model.Number
	Amount model.Number
}

// LevelProvider returns the levels for the given center price, which controls the spread and number of levels
type LevelProvider interface {
	GetLevels(state *State) ([]Level, error)
}
