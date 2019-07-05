package plugins

import (
	"database/sql"
	"fmt"
	"log"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stellar/kelp/api"
	"github.com/stellar/kelp/model"
)

const sqlSelectSumSold = "SELECT SUM(base_volume) as sum_base, SUM(counter_cost) as sum_quote FROM trades WHERE date_utc = $1 AND base = $2 AND quote = $3 AND action = $4"
const maxSellLimitsTolerancePct = 0.001

// staticLevel represents a layer in the orderbook defined statically
// extracted here because it's shared by strategy and sideStrategy where strategy depeneds on sideStrategy
type staticLevel struct {
	SPREAD float64 `valid:"-"`
	AMOUNT float64 `valid:"-"`
}

// how much to offset your rates by. Can use percent and offset together.
// A positive value indicates that your base asset (ASSET_A) has a higher rate than the rate received from your price feed
// A negative value indicates that your base asset (ASSET_A) has a lower rate than the rate received from your price feed
type rateOffset struct {
	// specified as a decimal (ex: 0.05 = 5%).
	percent float64
	// specified as a decimal.
	absolute float64

	// specifies the order in which to offset the rates. If true then we apply the RATE_OFFSET_PERCENT first otherwise we apply the RATE_OFFSET first.
	// example rate calculation when set to true: ((rate_from_price_feed_a/rate_from_price_feed_b) * (1 + rate_offset_percent)) + rate_offset
	// example rate calculation when set to false: ((rate_from_price_feed_a/rate_from_price_feed_b) + rate_offset) * (1 + rate_offset_percent)
	percentFirst bool

	// set this to true if buying
	invert bool
}

// MaxDailySell is the maximum amount we want to sell for the day (based on UTC timezone)
type MaxDailySell struct {
	amount    float64
	assetType string // "base" or "quote"
}

// staticSpreadLevelProvider provides a fixed number of levels using a static percentage spread
type staticSpreadLevelProvider struct {
	staticLevels     []staticLevel
	amountOfBase     float64
	offset           rateOffset
	pf               *api.FeedPair
	orderConstraints *model.OrderConstraints
	tradesDB         *sql.DB
	baseAsset        string
	quoteAsset       string
	maxDailySell     *MaxDailySell
	minSellPrice     float64
}

// ensure it implements the LevelProvider interface
var _ api.LevelProvider = &staticSpreadLevelProvider{}

// makeStaticSpreadLevelProvider is a factory method
func makeStaticSpreadLevelProvider(
	staticLevels []staticLevel,
	amountOfBase float64,
	offset rateOffset,
	pf *api.FeedPair,
	orderConstraints *model.OrderConstraints,
	tradesDB *sql.DB,
	baseAsset string,
	quoteAsset string,
	maxDailySell *MaxDailySell,
	minSellPrice float64,
) api.LevelProvider {
	return &staticSpreadLevelProvider{
		staticLevels:     staticLevels,
		amountOfBase:     amountOfBase,
		offset:           offset,
		pf:               pf,
		orderConstraints: orderConstraints,
		tradesDB:         tradesDB,
		baseAsset:        baseAsset,
		quoteAsset:       quoteAsset,
		maxDailySell:     maxDailySell,
		minSellPrice:     minSellPrice,
	}
}

// GetLevels impl.
func (p *staticSpreadLevelProvider) GetLevels(maxAssetBase float64, maxAssetQuote float64) ([]api.Level, error) {
	centerPrice, e := p.pf.GetCenterPrice()
	if e != nil {
		log.Printf("error: center price couldn't be loaded! | %s\n", e)
		return nil, e
	}

	capAmountFn := func(
		maxAssetBase float64,
		baseAmountSoFar float64,
		desiredAmountBase float64,
		price float64,
	) float64 {
		return desiredAmountBase
	}
	if p.tradesDB == nil && p.maxDailySell.assetType == "" {
		log.Printf("tradesDB was nil and maxDailySell.assetType was empty; not checking maxSold amounts for today\n")
	} else if p.tradesDB == nil {
		log.Printf("only tradesDB was nil; not checking maxSold amounts for today\n")
	} else if p.maxDailySell.assetType == "" {
		log.Printf("only maxDailySell.assetType was empty; not checking maxSold amounts for today\n")
	} else {
		dateString := time.Now().UTC().Format(dbDateFormatString)
		mSold, e := p.maxSoldToday(dateString)
		if e != nil {
			return nil, fmt.Errorf("could not load max sold amounts for today (%s): %s", dateString, e)
		}
		log.Printf("maxSold amounts for today (%s): baseSoldUnits = %.8f %s, quoteCostUnits = %.8f %s (maxDailySell = %.8f %s units)\n", dateString, mSold.sumBaseSold, p.baseAsset, mSold.sumQuoteCost, p.quoteAsset, p.maxDailySell.amount, p.maxDailySell.assetType)

		if p.maxDailySell.assetType == "base" && mSold.sumBaseSold >= p.maxDailySell.amount*(1-maxSellLimitsTolerancePct) {
			log.Printf("base threshold crossed (%f%% tolerance), returning 0 levels\n", maxSellLimitsTolerancePct*100)
			return []api.Level{}, nil
		} else if p.maxDailySell.assetType == "quote" && mSold.sumQuoteCost >= p.maxDailySell.amount*(1-maxSellLimitsTolerancePct) {
			log.Printf("quote threshold crossed (%f%% tolerance), returning 0 levels\n", maxSellLimitsTolerancePct*100)
			return []api.Level{}, nil
		} else if p.maxDailySell.assetType != "quote" && p.maxDailySell.assetType != "base" {
			return []api.Level{}, fmt.Errorf("staticSpreadLevelProvider has invalid value for maxDailySell.assetType (%s)\n", p.maxDailySell.assetType)
		}

		log.Printf("maxDailySell thresholds are within daily limits\n")
		if p.maxDailySell.assetType == "base" {
			capAmountFn = func(
				maxAssetBase float64,
				baseAmountSoFar float64,
				desiredAmountBase float64,
				price float64,
			) float64 {
				return p.capSellAmountUsingBaseConstraint(
					maxAssetBase,
					p.maxDailySell.amount-mSold.sumBaseSold,
					baseAmountSoFar,
					desiredAmountBase,
					price,
				)
			}
		} else {
			capAmountFn = func(
				maxAssetBase float64,
				baseAmountSoFar float64,
				desiredAmountBase float64,
				price float64,
			) float64 {
				return p.capSellAmountUsingQuoteConstraint(
					maxAssetBase,
					p.maxDailySell.amount-mSold.sumQuoteCost,
					baseAmountSoFar,
					desiredAmountBase,
					price,
				)
			}
		}
	}

	if p.offset.percent != 0.0 || p.offset.absolute != 0 {
		// if inverted, we want to invert before we compute the adjusted price, and then invert back
		if p.offset.invert {
			centerPrice = 1 / centerPrice
		}
		scaleFactor := 1 + p.offset.percent
		if p.offset.percentFirst {
			centerPrice = (centerPrice * scaleFactor) + p.offset.absolute
		} else {
			centerPrice = (centerPrice + p.offset.absolute) * scaleFactor
		}
		if p.offset.invert {
			centerPrice = 1 / centerPrice
		}
		log.Printf("center price (adjusted): %.7f\n", centerPrice)
	}

	levels := []api.Level{}
	baseAmountSoFar := 0.0
	for _, sl := range p.staticLevels {
		absoluteSpread := centerPrice * sl.SPREAD
		price := model.NumberFromFloat(centerPrice+absoluteSpread, p.orderConstraints.PricePrecision)
		if p.minSellPrice > 0.0 && price.AsFloat() < p.minSellPrice {
			log.Printf("skipping level at price = %f because it was less than minSellPrice (%f)\n", price.AsFloat(), p.minSellPrice)
			continue
		}

		amount := model.NumberFromFloat(sl.AMOUNT*p.amountOfBase, p.orderConstraints.VolumePrecision)
		amountCapped := capAmountFn(maxAssetBase, baseAmountSoFar, amount.AsFloat(), price.AsFloat())
		if amountCapped <= 0 {
			break
		}
		amountCappedNumber := model.NumberFromFloat(amountCapped, p.orderConstraints.VolumePrecision)
		levels = append(levels, api.Level{
			// we always add here because it is only used in the context of selling so we always charge a higher price to include a spread
			Price:  *price,
			Amount: *amountCappedNumber,
		})
		baseAmountSoFar += amountCappedNumber.AsFloat()
	}
	return levels, nil
}

func (p *staticSpreadLevelProvider) capSellAmountUsingBaseConstraint(
	maxAssetBase float64,
	maxSellAmountRemainingBaseOrQuote float64,
	baseAmountSoFar float64,
	desiredAmountBase float64,
	price float64,
) float64 {
	currentMaxSellAmountRemaining := maxSellAmountRemainingBaseOrQuote - baseAmountSoFar
	if desiredAmountBase <= currentMaxSellAmountRemaining {
		return desiredAmountBase
	}
	return currentMaxSellAmountRemaining
}

func (p *staticSpreadLevelProvider) capSellAmountUsingQuoteConstraint(
	maxAssetBase float64,
	maxSellAmountRemainingBaseOrQuote float64,
	baseAmountSoFar float64,
	desiredAmountBase float64,
	price float64,
) float64 {
	quoteAmountSoFar := baseAmountSoFar * price
	desiredAmountQuote := desiredAmountBase * price
	currentMaxSellAmountRemaining := maxSellAmountRemainingBaseOrQuote - quoteAmountSoFar
	if desiredAmountQuote <= currentMaxSellAmountRemaining {
		// always return amounts in units of base asset
		return desiredAmountBase
	}
	// always return amounts in units of base asset
	return currentMaxSellAmountRemaining / price
}

// GetFillHandlers impl
func (p *staticSpreadLevelProvider) GetFillHandlers() ([]api.FillHandler, error) {
	return nil, nil
}

type maxSold struct {
	sumBaseSold  float64
	sumQuoteCost float64
}

func (p *staticSpreadLevelProvider) maxSoldToday(dateUTC string) (*maxSold, error) {
	ms := &maxSold{}

	var sumBase1 sql.NullFloat64
	var sumQuote1 sql.NullFloat64
	row := p.tradesDB.QueryRow(sqlSelectSumSold, dateUTC, p.baseAsset, p.quoteAsset, "sell")
	e := row.Scan(&sumBase1, &sumQuote1)
	if e != nil {
		return nil, fmt.Errorf("could not read data from first sqlSelectSumSold query: %s", e)
	}
	if sumBase1.Valid {
		ms.sumBaseSold += sumBase1.Float64
	}
	if sumQuote1.Valid {
		ms.sumQuoteCost += sumQuote1.Float64
	}

	var sumBase2Inverted sql.NullFloat64
	var sumQuote2Inverted sql.NullFloat64
	row = p.tradesDB.QueryRow(sqlSelectSumSold, dateUTC, p.quoteAsset, p.baseAsset, "buy")
	e = row.Scan(&sumBase2Inverted, &sumQuote2Inverted)
	if e != nil {
		return nil, fmt.Errorf("could not read data from second sqlSelectSumSold query: %s", e)
	}
	if sumQuote2Inverted.Valid {
		ms.sumBaseSold += sumQuote2Inverted.Float64
	}
	if sumBase2Inverted.Valid {
		ms.sumQuoteCost += sumBase2Inverted.Float64
	}
	return ms, nil
}
