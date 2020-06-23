package plugins

import (
	"crypto/sha1"
	"fmt"
	"log"
	"math"
	"math/rand"
	"time"

	"github.com/stellar/kelp/api"
	"github.com/stellar/kelp/model"
	"github.com/stellar/kelp/support/postgresdb"
)

const secondsInHour = 60 * 60
const secondsInDay = 24 * secondsInHour
const timeFormat = time.RFC3339

// sellTwapLevelProvider provides a fixed number of levels using a static percentage spread
type sellTwapLevelProvider struct {
	startPf                                               api.PriceFeed
	offset                                                rateOffset
	orderConstraints                                      *model.OrderConstraints
	dowFilter                                             [7]volumeFilter
	numHoursToSell                                        int
	parentBucketSizeSeconds                               int
	distributeSurplusOverRemainingIntervalsPercentCeiling float64
	exponentialSmoothingFactor                            float64
	minChildOrderSizePercentOfParent                      float64
	random                                                *rand.Rand

	// uninitialized
	activeBucket    *bucketInfo
	previousRoundID *roundID
}

// ensure it implements the LevelProvider interface
var _ api.LevelProvider = &sellTwapLevelProvider{}

// makeSellTwapLevelProvider is a factory method
func makeSellTwapLevelProvider(
	startPf api.PriceFeed,
	offset rateOffset,
	orderConstraints *model.OrderConstraints,
	dowFilter [7]volumeFilter,
	numHoursToSell int,
	parentBucketSizeSeconds int,
	distributeSurplusOverRemainingIntervalsPercentCeiling float64,
	exponentialSmoothingFactor float64,
	minChildOrderSizePercentOfParent float64,
	randSeed int64,
) (api.LevelProvider, error) {
	if numHoursToSell <= 0 || numHoursToSell > 24 {
		return nil, fmt.Errorf("invalid number of hours to sell, expected 0 < numHoursToSell <= 24; was %d", numHoursToSell)
	}

	if parentBucketSizeSeconds <= 0 || parentBucketSizeSeconds > secondsInDay {
		return nil, fmt.Errorf("invalid value for parentBucketSizeSeconds, expected 0 < parentBucketSizeSeconds <= %d (secondsInDay); was %d", secondsInDay, parentBucketSizeSeconds)
	}

	if (secondsInDay % parentBucketSizeSeconds) != 0 {
		return nil, fmt.Errorf("parentBucketSizeSeconds needs to perfectly divide secondsInDay but it does not; secondsInDay is %d and parentBucketSizeSeconds was %d", secondsInDay, parentBucketSizeSeconds)
	}

	if distributeSurplusOverRemainingIntervalsPercentCeiling < 0.0 || distributeSurplusOverRemainingIntervalsPercentCeiling > 1.0 {
		return nil, fmt.Errorf("distributeSurplusOverRemainingIntervalsPercentCeiling is invalid, expected 0.0 <= distributeSurplusOverRemainingIntervalsPercentCeiling <= 1.0; was %.f", distributeSurplusOverRemainingIntervalsPercentCeiling)
	}

	if exponentialSmoothingFactor < 0.0 || exponentialSmoothingFactor > 1.0 {
		return nil, fmt.Errorf("exponentialSmoothingFactor is invalid, expected 0.0 <= exponentialSmoothingFactor <= 1.0; was %.f", exponentialSmoothingFactor)
	}

	if minChildOrderSizePercentOfParent < 0.0 || minChildOrderSizePercentOfParent > 1.0 {
		return nil, fmt.Errorf("minChildOrderSizePercentOfParent is invalid, expected 0.0 <= minChildOrderSizePercentOfParent <= 1.0; was %.f", exponentialSmoothingFactor)
	}

	for i, f := range dowFilter {
		if !f.isSellingBase() {
			return nil, fmt.Errorf("volume filter at index %d was not selling the base asset as expected: %s", i, f.configValue)
		}
	}

	random := rand.New(rand.NewSource(randSeed))
	return &sellTwapLevelProvider{
		startPf:                 startPf,
		offset:                  offset,
		orderConstraints:        orderConstraints,
		dowFilter:               dowFilter,
		numHoursToSell:          numHoursToSell,
		parentBucketSizeSeconds: parentBucketSizeSeconds,
		distributeSurplusOverRemainingIntervalsPercentCeiling: distributeSurplusOverRemainingIntervalsPercentCeiling,
		exponentialSmoothingFactor:                            exponentialSmoothingFactor,
		minChildOrderSizePercentOfParent:                      minChildOrderSizePercentOfParent,
		random:                                                random,
	}, nil
}

type bucketID int64

type dynamicBucketValues struct {
	isNew       bool
	roundID     roundID
	dayBaseSold float64
	baseSold    float64
	now         time.Time
}

type bucketInfo struct {
	ID                    bucketID
	startTime             time.Time
	endTime               time.Time
	sizeSeconds           int
	totalBuckets          int64
	totalBucketsToSell    int64
	dayBaseSoldStart      float64
	dayBaseCapacity       float64
	totalBaseSurplusStart float64
	baseSurplusIncluded   float64
	baseCapacity          float64
	minOrderSizeBase      float64
	dynamicValues         *dynamicBucketValues
}

func (b *bucketInfo) dayBaseRemaining() float64 {
	return b.dayBaseCapacity - b.dynamicValues.dayBaseSold
}

func (b *bucketInfo) baseRemaining() float64 {
	return b.baseCapacity - b.dynamicValues.baseSold
}

// String is the Stringer method
func (b *bucketInfo) String() string {
	return fmt.Sprintf("BucketInfo[UUID=%s, date=%s, dayID=%d (%s), bucketID=%d, startTime=%s, endTime=%s, sizeSeconds=%d, totalBuckets=%d, totalBucketsToSell=%d, dayBaseSoldStart=%.8f, dayBaseCapacity=%.8f, totalBaseSurplusStart=%.8f, baseSurplusIncluded=%.8f, baseCapacity=%.8f, minOrderSizeBase=%.8f, DynamicBucketValues[isNew=%v, roundID=%d, dayBaseSold=%.8f, dayBaseRemaining=%.8f, baseSold=%.8f, baseRemaining=%.8f, bucketProgress=%.2f%%, bucketTimeElapsed=%.2f%%]]",
		b.UUID(),
		b.startTime.Format("2006-01-02"),
		b.startTime.Weekday(),
		b.startTime.Weekday().String(),
		b.ID,
		b.startTime.Format(timeFormat),
		b.endTime.Format(timeFormat),
		b.sizeSeconds,
		b.totalBuckets,
		b.totalBucketsToSell,
		b.dayBaseSoldStart,
		b.dayBaseCapacity,
		b.totalBaseSurplusStart,
		b.baseSurplusIncluded,
		b.baseCapacity,
		b.minOrderSizeBase,
		b.dynamicValues.isNew,
		b.dynamicValues.roundID,
		b.dynamicValues.dayBaseSold,
		b.dayBaseRemaining(),
		b.dynamicValues.baseSold,
		b.baseRemaining(),
		100.0*b.dynamicValues.baseSold/b.baseCapacity,
		100.0*float64(b.dynamicValues.now.Unix()-b.startTime.Unix())/float64(b.endTime.Unix()-b.startTime.Unix()),
	)
}

// UUID gives a unique hash ID for this bucket that is unique to this specific configuration and time interval
// this should be constant for all bucket instances that overlap with this time interval and configuration
func (b *bucketInfo) UUID() string {
	timePartition := fmt.Sprintf("startTime=%s_endTime=%s", b.startTime.Format(time.RFC3339Nano), b.endTime.Format(time.RFC3339Nano))
	configPartition := fmt.Sprintf("totalBuckets=%d_totalBucketsToSell=%d_minOrderSizeBase=%.8f", b.totalBuckets, b.totalBucketsToSell, b.minOrderSizeBase)
	s := fmt.Sprintf("timePartition=%s__configPartition=%s", timePartition, configPartition)

	hash := sha1.Sum([]byte(s))
	return fmt.Sprintf("%x", hash)
}

type roundID uint64

type roundInfo struct {
	ID                  roundID
	bucketID            bucketID
	bucketUUID          string
	now                 time.Time
	secondsElapsedToday int64
	sizeBaseCapped      float64
	price               float64
}

// String is the Stringer method
func (r *roundInfo) String() string {
	return fmt.Sprintf(
		"RoundInfo[roundID=%d, bucketID=%d, bucketUUID=%s, now=%s (day=%s, secondsElapsedToday=%d), sizeBaseCapped=%.8f, price=%.8f]",
		r.ID,
		r.bucketID,
		r.bucketUUID,
		r.now.Format(timeFormat),
		r.now.Weekday().String(),
		r.secondsElapsedToday,
		r.sizeBaseCapped,
		r.price,
	)
}

// GetLevels impl.
func (p *sellTwapLevelProvider) GetLevels(maxAssetBase float64, maxAssetQuote float64) ([]api.Level, error) {
	now := time.Now().UTC()
	log.Printf("GetLevels, unix timestamp for 'now' in UTC = %d (%s)\n", now.Unix(), now)

	volFilter := p.dowFilter[now.Weekday()]
	log.Printf("volumeFilter = %s\n", volFilter.String())

	rID := p.makeRoundID()
	bucket, e := p.makeBucketInfo(now, volFilter, rID)
	if e != nil {
		return nil, fmt.Errorf("unable to make bucketInfo: %s", e)
	}
	log.Printf("bucketInfo: %s\n", bucket)

	round, e := p.makeRoundInfo(rID, now, bucket)
	if e != nil {
		return nil, fmt.Errorf("unable to make roundInfo: %s", e)
	}
	log.Printf("roundInfo: %s\n", round)

	// save bucket and round for future rounds
	p.activeBucket = bucket
	p.previousRoundID = &round.ID

	return []api.Level{{
		Price:  *model.NumberFromFloat(round.price, p.orderConstraints.PricePrecision),
		Amount: *model.NumberFromFloat(round.sizeBaseCapped, p.orderConstraints.VolumePrecision),
	}}, nil
}

func (p *sellTwapLevelProvider) makeFirstBucketFrame(
	now time.Time,
	volFilter volumeFilter,
	startTime time.Time,
	endTime time.Time,
	totalBuckets int64,
	bID bucketID,
	rID roundID,
) (*bucketInfo, error) {
	totalBucketsToSell := int64(math.Ceil(float64(p.numHoursToSell*secondsInHour) / float64(p.parentBucketSizeSeconds)))

	dayBaseCapacity, e := volFilter.mustGetBaseAssetCapInBaseUnits()
	if e != nil {
		return nil, fmt.Errorf("could not fetch base asset cap in base units: %s", e)
	}

	dailyVolumeValues, e := volFilter.dailyValuesByDate(now.Format(postgresdb.DateFormatString))
	if e != nil {
		return nil, fmt.Errorf("could not fetch daily values for today: %s", e)
	}
	dayBaseSoldStart := dailyVolumeValues.baseVol

	totalBaseSurplusStart := 0.0
	baseSurplus := 0.0
	baseCapacity := float64(dayBaseCapacity) / float64(totalBucketsToSell)
	minOrderSizeBase := p.minChildOrderSizePercentOfParent * baseCapacity
	// upon instantiation the first bucket frame does not have anything sold beyond the starting values
	dynamicValues := &dynamicBucketValues{
		isNew:       true,
		roundID:     rID,
		dayBaseSold: dayBaseSoldStart,
		baseSold:    0.0,
		now:         now,
	}

	return &bucketInfo{
		ID:                    bID,
		startTime:             startTime,
		endTime:               endTime,
		sizeSeconds:           p.parentBucketSizeSeconds,
		totalBuckets:          totalBuckets,
		totalBucketsToSell:    totalBucketsToSell,
		dayBaseSoldStart:      dayBaseSoldStart,
		dayBaseCapacity:       dayBaseCapacity,
		totalBaseSurplusStart: totalBaseSurplusStart,
		baseSurplusIncluded:   baseSurplus,
		baseCapacity:          baseCapacity,
		minOrderSizeBase:      minOrderSizeBase,
		dynamicValues:         dynamicValues,
	}, nil
}

func (p *sellTwapLevelProvider) updateExistingBucket(now time.Time, volFilter volumeFilter, rID roundID) (*bucketInfo, error) {
	bucketCopy := *p.activeBucket
	bucket := &bucketCopy

	dailyVolumeValues, e := volFilter.dailyValuesByDate(now.Format(postgresdb.DateFormatString))
	if e != nil {
		return nil, fmt.Errorf("could not fetch daily values for today: %s", e)
	}
	dayBaseSold := dailyVolumeValues.baseVol

	bucket.dynamicValues = &dynamicBucketValues{
		isNew:       false,
		roundID:     rID,
		dayBaseSold: dayBaseSold,
		baseSold:    dayBaseSold - bucket.dayBaseSoldStart,
		now:         now,
	}
	return bucket, nil
}

func (p *sellTwapLevelProvider) cutoverToNewBucketSameDay(newBucket *bucketInfo) (*bucketInfo, error) {
	if newBucket.ID != p.activeBucket.ID+1 {
		return nil, fmt.Errorf("new bucketID (%d) needs to be one more than the previous bucketID (%d)", newBucket.ID, p.activeBucket.ID)
	}

	// update values that will change for a brand new bucket on the same day
	thisBucketDayBaseSoldStart := p.activeBucket.dynamicValues.dayBaseSold
	thisBucketDayBaseSold := newBucket.dayBaseSoldStart     // pull dayBaseSold from what was queried, this can be more than what was eventually sold in last bucket
	newBucket.dayBaseSoldStart = thisBucketDayBaseSoldStart // start new bucket with ending value of previous bucket
	newBucket.dynamicValues = &dynamicBucketValues{
		isNew:       true,
		roundID:     newBucket.dynamicValues.roundID,
		dayBaseSold: thisBucketDayBaseSold,
		baseSold:    thisBucketDayBaseSold - thisBucketDayBaseSoldStart,
		now:         newBucket.dynamicValues.now,
	}

	// the total surplus remaining up until this point gets distributed over the remaining buckets
	averageBaseCapacity := newBucket.baseCapacity
	numPreviousBuckets := newBucket.ID // buckets are 0-indexed, so bucketID is equal to numbers of previous buckets
	expectedSold := averageBaseCapacity * float64(numPreviousBuckets)
	newBucket.totalBaseSurplusStart = expectedSold - thisBucketDayBaseSoldStart
	totalRemainingBuckets := newBucket.totalBuckets - int64(numPreviousBuckets)
	newBucket.baseSurplusIncluded = p.firstDistributionOfBaseSurplus(newBucket.totalBaseSurplusStart, totalRemainingBuckets)
	newBucket.baseCapacity = averageBaseCapacity + newBucket.baseSurplusIncluded

	return newBucket, nil
}

func (p *sellTwapLevelProvider) makeBucketInfo(now time.Time, volFilter volumeFilter, rID roundID) (*bucketInfo, error) {
	dayStartTime := floorDate(now)
	dayEndTime := ceilDate(now)

	secondsElapsedToday := now.Unix() - dayStartTime.Unix()
	bID := bucketID(secondsElapsedToday / int64(p.parentBucketSizeSeconds))
	startTime := dayStartTime.Add(time.Second * time.Duration(bID) * time.Duration(p.parentBucketSizeSeconds))
	endTime := startTime.Add(time.Second*time.Duration(p.parentBucketSizeSeconds) - time.Nanosecond)

	totalBuckets := int64(math.Ceil(float64(dayEndTime.Unix()-dayStartTime.Unix()) / float64(p.parentBucketSizeSeconds)))

	// bucket on bot load
	if p.activeBucket == nil {
		bucket, e := p.makeFirstBucketFrame(now, volFilter, startTime, endTime, totalBuckets, bID, rID)
		if e != nil {
			return nil, fmt.Errorf("could not make first bucket: %s", e)
		}
		return bucket, nil
	}

	// new round in the same bucket
	if bID == p.activeBucket.ID {
		bucket, e := p.updateExistingBucket(now, volFilter, rID)
		if e != nil {
			return nil, fmt.Errorf("could not update existing bucket (ID=%d): %s", bID, e)
		}
		return bucket, nil
	}

	// new bucket needs to be created
	newBucket, e := p.makeFirstBucketFrame(now, volFilter, startTime, endTime, totalBuckets, bID, rID)
	if e != nil {
		return nil, fmt.Errorf("unable to make first bucket frame when cutting over with new bucketID (ID=%d): %s", bID, e)
	}
	// on a new day
	if newBucket.ID == 0 {
		return newBucket, nil
	}
	// on the same day
	return p.cutoverToNewBucketSameDay(newBucket)
}

/*
Using a geometric series calculation:
Sn = a * (r^n - 1) / (r - 1)
a = Sn * (r - 1) / (r^n - 1)
a = 8,000 * (0.5 - 1) / (0.5^4 - 1)
a = 8,000 * (-0.5) / (0.0625 - 1)
a = 8,000 * (0.5/0.9375)
a = 4,266.67
*/
func (p *sellTwapLevelProvider) firstDistributionOfBaseSurplus(totalSurplus float64, totalRemainingBuckets int64) float64 {
	Sn := totalSurplus
	r := p.exponentialSmoothingFactor
	n := math.Ceil(p.distributeSurplusOverRemainingIntervalsPercentCeiling * float64(totalRemainingBuckets))

	a := Sn * (r - 1.0) / (math.Pow(r, n) - 1.0)
	return a
}

func (p *sellTwapLevelProvider) makeRoundID() roundID {
	if p.previousRoundID == nil {
		return roundID(0)
	}
	return *p.previousRoundID + 1
}

func (p *sellTwapLevelProvider) makeRoundInfo(rID roundID, now time.Time, bucket *bucketInfo) (*roundInfo, error) {
	dayStartTime := floorDate(now)
	secondsElapsedToday := now.Unix() - dayStartTime.Unix()

	var sizeBaseCapped float64
	if bucket.baseRemaining() <= bucket.minOrderSizeBase {
		sizeBaseCapped = bucket.baseRemaining()
	} else {
		sizeBaseCapped = bucket.minOrderSizeBase + (p.random.Float64() * (bucket.baseRemaining() - bucket.minOrderSizeBase))
	}

	price, e := p.startPf.GetPrice()
	if e != nil {
		return nil, fmt.Errorf("could not get price from feed: %s", e)
	}
	adjustedPrice, wasModified := p.offset.apply(price)
	if wasModified {
		log.Printf("feed price (adjusted): %.8f\n", adjustedPrice)
	}

	return &roundInfo{
		ID:                  rID,
		bucketID:            bucket.ID,
		bucketUUID:          bucket.UUID(),
		now:                 now,
		secondsElapsedToday: secondsElapsedToday,
		sizeBaseCapped:      sizeBaseCapped,
		price:               adjustedPrice,
	}, nil
}

// GetFillHandlers impl
func (p *sellTwapLevelProvider) GetFillHandlers() ([]api.FillHandler, error) {
	return nil, nil
}

func floorDate(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

func ceilDate(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, -1, t.Location())
}
