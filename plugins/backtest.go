package plugins

import (
	"fmt"
	"log"
	"strconv"
	"time"

	"github.com/stellar/kelp/api"
	"github.com/stellar/kelp/model"
)

// ensure that backtest conforms to the Exchange interface
var _ api.Exchange = &backtest{}

// backtest is the implementation for the backtesting framework
type backtest struct {
	pair              *model.TradingPair
	balances          *balanceStruct
	obFn              orderbookFn
	nextTransactionID uint64
}

type orderbookFn interface {
	getOrderBook() (*model.OrderBook, error)
}

type balanceStruct struct {
	base  *model.Number
	quote *model.Number
}

type slippageBasedOrderBook struct {
	pair        *model.TradingPair
	pf          api.PriceFeed
	slippagePct float64
}

var _ orderbookFn = &slippageBasedOrderBook{}

func (ob *slippageBasedOrderBook) getOrderBook() (*model.OrderBook, error) {
	price, e := ob.pf.GetPrice()
	if e != nil {
		return nil, fmt.Errorf("unable to get price for orderbook: %s", e)
	}

	ts := model.MakeTimestamp(time.Now().UnixNano() / int64(time.Millisecond))
	ask := model.Order{
		Pair:        ob.pair,
		OrderAction: model.OrderActionSell,
		OrderType:   model.OrderTypeLimit,
		Price:       model.NumberFromFloat(price*(1+ob.slippagePct), largePrecision),
		Volume:      model.NumberFromFloat(1000000000000.0, largePrecision), // 1 trillion should be large enough
		Timestamp:   ts,
	}
	bid := model.Order{
		Pair:        ob.pair,
		OrderAction: model.OrderActionBuy,
		OrderType:   model.OrderTypeLimit,
		Price:       model.NumberFromFloat(price*(1-ob.slippagePct), largePrecision),
		Volume:      model.NumberFromFloat(1000000000000.0, largePrecision), // 1 trillion should be large enough
		Timestamp:   ts,
	}
	return model.MakeOrderBook(ob.pair, []model.Order{ask}, []model.Order{bid}), nil
}

// makeBacktest is a factory method to make the backtesting framework
func makeBacktestSimple(
	pair *model.TradingPair,
	baseBalance *model.Number,
	quoteBalance *model.Number,
	pf api.PriceFeed,
	slippagePct float64,
) (*backtest, error) {
	return &backtest{
		pair: pair,
		balances: &balanceStruct{
			base:  baseBalance,
			quote: baseBalance,
		},
		obFn: &slippageBasedOrderBook{
			pair:        pair,
			pf:          pf,
			slippagePct: slippagePct,
		},
		nextTransactionID: 0,
	}, nil
}

// AddOrder impl.
func (b *backtest) AddOrder(order *model.Order) (*model.TransactionID, error) {
	if order.Pair.String() != b.pair.String() {
		return nil, fmt.Errorf("invalid pair passed in: %s (accepted = %s)", order.Pair.String(), b.pair.String())
	}

	ob, e := b.obFn.getOrderBook()
	if e != nil {
		return nil, fmt.Errorf("unable to get orderbook when trying to add order: %s", e)
	}

	if order.OrderAction.IsBuy() {
		unitsBought := model.NumberFromFloat(0.0, largePrecision)
		unitsSold := model.NumberFromFloat(0.0, largePrecision)
		for i, ask := range ob.Asks() {
			if order.Price.AsFloat() < ask.Price.AsFloat() {
				return nil, fmt.Errorf("kelp does not currently support the case where you place maker offers in backtesting mode, order price = %s, orderbook ask price = %s, index of ask = ", order.Price.AsString(), ask.Price.AsString(), i)
			}

			if order.Volume.AsFloat() <= ask.Volume.AsFloat() {
				unitsBought = unitsBought.Add(*order.Volume)
				// use the price of the ask since that's the maker order
				unitsSold = unitsSold.Add(*order.Volume.Multiply(*ask.Price))
				// we're done
				break
			} else {
				unitsBought = unitsBought.Add(*ask.Volume)
				// use the price of the ask since that's the maker order, and also the volume of the ask
				unitsSold = unitsSold.Add(*ask.Volume.Multiply(*ask.Price))
				// continue
			}
		}

		if unitsBought.AsFloat() < order.Volume.AsFloat() {
			return nil, fmt.Errorf("not enough liquidity to place buy order, number of asks in orderbook is %d", len(ob.Asks()))
		}
		if unitsSold.AsFloat() > b.balances.quote.AsFloat() {
			return nil, fmt.Errorf("cannot buy %s units of base since that results in trying to sell at least %s units of the quote assets which is more than the %s quote units in the balance",
				order.Volume.AsString(), unitsSold.AsString(), b.balances.quote.AsString(),
			)
		}

		b.balances.base = b.balances.base.Add(*unitsBought)
		b.balances.quote = b.balances.quote.Subtract(*unitsSold)
	} else {
		unitsBought := model.NumberFromFloat(0.0, largePrecision)
		unitsSold := model.NumberFromFloat(0.0, largePrecision)
		for i, bid := range ob.Bids() {
			if order.Price.AsFloat() > bid.Price.AsFloat() {
				return nil, fmt.Errorf("kelp does not currently support the case where you place maker offers in backtesting mode, order price = %s, orderbook bid price = %s, index of bid = ", order.Price.AsString(), bid.Price.AsString(), i)
			}

			if order.Volume.AsFloat() <= bid.Volume.AsFloat() {
				unitsSold = unitsSold.Add(*order.Volume)
				// use the price of the bid since that's the maker order
				unitsBought = unitsBought.Add(*order.Volume.Multiply(*bid.Price))
				// we're done
				break
			} else {
				unitsSold = unitsSold.Add(*bid.Volume)
				// use the price of the bid since that's the maker order, and also the volume of the bid
				unitsBought = unitsBought.Add(*bid.Volume.Multiply(*bid.Price))
				// continue
			}
		}

		if unitsSold.AsFloat() < order.Volume.AsFloat() {
			return nil, fmt.Errorf("not enough liquidity to place sell order, number of bids in orderbook is %d", len(ob.Bids()))
		}
		if unitsSold.AsFloat() > b.balances.base.AsFloat() {
			return nil, fmt.Errorf("cannot sell %s units of base since that's more than the %s base units in the balance", unitsSold.AsString(), b.balances.base.AsString())
		}

		b.balances.base = b.balances.base.Subtract(*unitsSold)
		b.balances.quote = b.balances.quote.Add(*unitsBought)
	}

	txID := model.MakeTransactionID(strconv.FormatUint(b.nextTransactionID, 64))
	b.nextTransactionID++
	return txID, nil
}

// CancelOrder impl.
func (b *backtest) CancelOrder(txID *model.TransactionID, pair model.TradingPair) (model.CancelOrderResult, error) {
	log.Printf("kelp does not currently support canceling orders since you cannot place maker offers in backtesting mode that would need canceling, returning successful CancelOrderResult\n")
	return model.CancelResultCancelSuccessful, nil
}

// GetAccountBalances impl.
func (b *backtest) GetAccountBalances(assetList []interface{}) (map[interface{}]model.Number, error) {
	if assetList[0] != "base" && assetList[1] != "quote" {
		return map[interface{}]model.Number{}, fmt.Errorf("invalid inputs passed in to backtesting mode, can only pass in [\"base\", \"quote\"]")
	}

	return map[interface{}]model.Number{
		"base":  *b.balances.base,
		"quote": *b.balances.quote,
	}, nil
}

// GetOrderConstraints impl
func (b *backtest) GetOrderConstraints(pair *model.TradingPair) *model.OrderConstraints {
	return model.MakeOrderConstraints(largePrecision, largePrecision, 1.0)
}

// OverrideOrderConstraints impl, can partially override values for specific pairs
func (b *backtest) OverrideOrderConstraints(pair *model.TradingPair, override *model.OrderConstraintsOverride) {
	panic("not supported in backtest mode")
}

// GetAssetConverter impl.
func (b *backtest) GetAssetConverter() model.AssetConverterInterface {
	return model.Display
}

// GetOpenOrders impl.
func (b *backtest) GetOpenOrders(pairs []*model.TradingPair) (map[model.TradingPair][]model.OpenOrder, error) {
	log.Printf("kelp does not currently support maker offers in backtesting mode so there cannot be any open orders\n")
	return map[model.TradingPair][]model.OpenOrder{}, nil
}

// GetOrderBook impl.
func (b *backtest) GetOrderBook(pair *model.TradingPair, maxCount int32) (*model.OrderBook, error) {
	if pair.String() != b.pair.String() {
		return nil, fmt.Errorf("invalid pair passed in: %s (accepted = %s)", pair.String(), b.pair.String())
	}

	ob, e := b.obFn.getOrderBook()
	if e != nil {
		return nil, fmt.Errorf("cannot get orderbook: %s", e)
	}

	asks := ob.Asks()
	if len(asks) > int(maxCount) {
		asks = asks[:maxCount]
	}
	bids := ob.Bids()
	if len(bids) > int(maxCount) {
		bids = bids[:maxCount]
	}

	return model.MakeOrderBook(b.pair, asks, bids), nil
}

// GetTickerPrice impl.
func (b *backtest) GetTickerPrice(pairs []model.TradingPair) (map[model.TradingPair]api.Ticker, error) {
	if len(pairs) != 1 {
		return map[model.TradingPair]api.Ticker{}, fmt.Errorf("invalid number of pairs passed in, exactly 1 allowed: %v", pairs)
	}

	if pairs[0].String() != b.pair.String() {
		return map[model.TradingPair]api.Ticker{}, fmt.Errorf("invalid pair passed in: %s (accepted = %s)", pairs[0].String(), b.pair.String())
	}

	ob, e := b.obFn.getOrderBook()
	if e != nil {
		return map[model.TradingPair]api.Ticker{}, fmt.Errorf("unable to get orderbook when fetching ticker price in backtesting mode: %s", e)
	}

	m := map[model.TradingPair]api.Ticker{}
	var askPrice *model.Number
	if len(ob.Asks()) > 0 {
		askPrice = ob.Asks()[0].Price
	}
	var bidPrice *model.Number
	if len(ob.Bids()) > 0 {
		bidPrice = ob.Bids()[0].Price
	}
	m[pairs[0]] = api.Ticker{
		AskPrice: askPrice,
		BidPrice: bidPrice,
	}

	return m, nil
}

// GetTradeHistory impl.
func (b *backtest) GetTradeHistory(pair model.TradingPair, maybeCursorStart interface{}, maybeCursorEnd interface{}) (*api.TradeHistoryResult, error) {
	// TODO implement
	return nil, fmt.Errorf("not supported in backtest mode yet")
}

// GetLatestTradeCursor impl.
func (b *backtest) GetLatestTradeCursor() (interface{}, error) {
	// TODO implement
	return nil, fmt.Errorf("not supported in backtest mode yet")
}

// GetTrades impl.
func (b *backtest) GetTrades(pair *model.TradingPair, maybeCursor interface{}) (*api.TradesResult, error) {
	if pair == nil {
		pair = b.pair
	}

	thr, e := b.GetTradeHistory(*pair, maybeCursor, nil)
	if e != nil {
		return nil, fmt.Errorf("error when delegating to GetTradeHistory function: %s", e)
	}

	return &api.TradesResult{
		Cursor: thr.Cursor,
		Trades: thr.Trades,
	}, nil
}

// GetWithdrawInfo impl.
func (b *backtest) GetWithdrawInfo(
	asset model.Asset,
	amountToWithdraw *model.Number,
	address string,
) (*api.WithdrawInfo, error) {
	return nil, fmt.Errorf("not supported in backtest mode")
}

// PrepareDeposit impl.
func (b *backtest) PrepareDeposit(asset model.Asset, amount *model.Number) (*api.PrepareDepositResult, error) {
	return nil, fmt.Errorf("not supported in backtest mode")
}

// WithdrawFunds impl.
func (b *backtest) WithdrawFunds(
	asset model.Asset,
	amountToWithdraw *model.Number,
	address string,
) (*api.WithdrawFunds, error) {
	return nil, fmt.Errorf("not supported in backtest mode")
}
