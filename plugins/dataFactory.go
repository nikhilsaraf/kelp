package plugins

import (
	"time"

	"github.com/lightyeario/kelp/api"
)

// Constants for the keys to InitializedData
const (
	DataKeyTime api.DataKey = iota
)

// InitializedData holds the initialized data objects for the full repository of data fields supported
var InitializedData = map[api.DataKey]api.Datum{
	DataKeyTime: defaultTimeDatum,
}

type timeDatum struct {
	now time.Time
}

var defaultTimeDatum api.Datum = &timeDatum{}

func (d *timeDatum) Load() error {
	d.now = time.Now()
	return nil
}
