package plugins

import (
	"time"

	"github.com/lightyeario/kelp/api"
)

// InitializedData holds the initialized data objects for the full repository of data fields supported
var InitializedData = map[string]api.Datum{
	"time": defaultTimeDatum,
}

type timeDatum struct {
	now time.Time
}

var defaultTimeDatum api.Datum = &timeDatum{}

func (d *timeDatum) Load() error {
	d.now = time.Now()
	return nil
}
