package plugins

import (
	"fmt"
	"log"
	"strconv"

	hProtocol "github.com/stellar/go/protocols/horizon"
	"github.com/stellar/go/txnbuild"
	"github.com/stellar/kelp/support/utils"
)

// SubmitFilter allows you to filter out operations before submitting to the network
type SubmitFilter interface {
	Apply(
		ops []txnbuild.Operation,
		sellingOffers []hProtocol.Offer, // quoted quote/base
		buyingOffers []hProtocol.Offer, // quoted base/quote
	) ([]txnbuild.Operation, error)
}

type filterFn func(op *txnbuild.ManageSellOffer) (*txnbuild.ManageSellOffer, bool, error)

type filterCounter struct {
	idx         int
	kept        uint8
	dropped     uint8
	transformed uint8
	ignored     uint8
}

// build a list of the existing offers that have a corresponding operation so we ignore these offers and only consider the operation version
func ignoreOfferIDs(ops []txnbuild.Operation) map[int64]bool {
	ignoreOfferIDs := map[int64]bool{}
	for _, op := range ops {
		switch o := op.(type) {
		case *txnbuild.ManageSellOffer:
			ignoreOfferIDs[o.OfferID] = true
		default:
			continue
		}
	}
	return ignoreOfferIDs
}

// TODO - simplify filterOps by separating out logic to convert into a single list of operations from transforming the operations
/*
What filterOps() does and why:

Solving the "existing offers problem":
Problem: We need to run the existing offers against the filter as well since they may no longer be compliant.
Solution: Do a merge of two "sorted" lists (operations list, offers list) to create a new list of operations.
	When sorted by price, this will ensure that we delete any spurious existing offers to meet the filter's
	needs. This also serves the purpose of "interleaving" the operations related to the offers and ops.

Solving the "ordering problem":
Problem: The incoming operations list combines both buy and sell operations. We want to run it though the filter
	without modifying the order of the buy or sell segments, or modify operations within the segments since that
	ordering is dictated by the strategy logic.
Solution: Since both these segments of buy/sell offers are contiguous, i.e. buy offers are all together and sell
	offers are all together, we can identify the "cutover point" in each list of operations and offers, and then
	advance the iteration index to the next segment for both segments in both lists by converting the remaining
	offers and operations to delete operations. This will not affect the order of operations, but any new delete
	operations created should be placed at the beginning of the respective buy and sell segments as is a requirement
	on sdex (see sellSideStrategy.go for details on why we need to start off with the delete operations).

Possible Question: Why do we not reuse the same logic that is in sellSideStrategy.go to "delete remaining offers"?
Answer: The logic that could possibly be reused is minimal -- it's just a for loop. The logic that converts offers
	to the associated delete operation is reused, which is the main crux of the "business logic" that we want to
	avoid rewriting. The logic in sellSideStrategy.go also only works on offers, here we work on offers and ops.

Solving the "increase price problem":
Problem: If we increase the price off a sell offer (or decrease price of a buy offer) then we will see the offer
	with an incorrect price before we see the update to the offer. This will result in an incorrect calculation,
	since we will later on see the updated offer and make adjustments, which would result in runtime complexity
	worse than O(N).
Solution: We first "dedupe" the offers and operations, by removing any offers that have a corresponding operation
	update based on offerID. This has an additional overhead on runtime complexity of O(N).

Solving the "no update operations problem":
Problem: if our trading strategy produces no operations for a given update cycle, indicating that the state of the
	orderbook is correct, then we will not enter the for-loop which is conditioned on operations. This would result
	in control going straight to the post-operations logic which should correctly consider the existing offers. This
	logic would be the same as what happens inside the for loop and we should ensure there is no repetition.
Solution: Refactor the code inside the for loop to clearly allow for reuse of functions and evaluation of existing
	offers outside the for loop.
*/
func filterOps(
	filterName string,
	baseAsset hProtocol.Asset,
	quoteAsset hProtocol.Asset,
	sellingOffers []hProtocol.Offer,
	buyingOffers []hProtocol.Offer,
	ops []txnbuild.Operation,
	fn filterFn,
) ([]txnbuild.Operation, error) {
	ignoreOfferIds := ignoreOfferIDs(ops)
	opCounter := filterCounter{}
	buyCounter := filterCounter{}
	sellCounter := filterCounter{}
	filteredOps := []txnbuild.Operation{}

	for opCounter.idx < len(ops) {
		op := ops[opCounter.idx]

		switch o := op.(type) {
		case *txnbuild.ManageSellOffer:
			var keep bool
			var originalOffer *txnbuild.ManageSellOffer
			originalMSO := *o
			var newOp *txnbuild.ManageSellOffer

			offerList, offerCounter, e := selectBuySellList(
				baseAsset,
				quoteAsset,
				o,
				sellingOffers,
				buyingOffers,
				&sellCounter,
				&buyCounter,
			)
			if e != nil {
				return nil, fmt.Errorf("unable to pick between whether the op was a buy or sell op: %s", e)
			}

			opToTransform, originalOffer, filterCounterToIncrement, isIgnoredOffer, e := selectOpOrOffer(
				offerList,
				offerCounter,
				o,
				&opCounter,
				ignoreOfferIds,
			)
			if e != nil {
				return nil, fmt.Errorf("error while picking op or offer: %s", e)
			}
			filterCounterToIncrement.idx++
			if isIgnoredOffer {
				filterCounterToIncrement.ignored++
				continue
			}

			// delete operations should never be dropped
			if opToTransform.Amount == "0" {
				newOp, keep = opToTransform, true
			} else {
				newOp, keep, e = fn(opToTransform)
				if e != nil {
					return nil, fmt.Errorf("could not transform offer (pointer case): %s", e)
				}
			}

			isNewOpNil := newOp == nil || fmt.Sprintf("%v", newOp) == "<nil>"
			if keep {
				if isNewOpNil {
					return nil, fmt.Errorf("we want to keep op but newOp was nil (programmer error?)")
				}

				if originalOffer != nil && originalOffer.Price == newOp.Price && originalOffer.Amount == newOp.Amount {
					// do not append to filteredOps because this is an existing offer that we want to keep as-is
					offerCounter.kept++
				} else if originalOffer != nil {
					// we were dealing with an existing offer that was changed
					filteredOps = append(filteredOps, newOp)
					offerCounter.transformed++
				} else {
					// we were dealing with an operation
					filteredOps = append(filteredOps, newOp)
					if originalMSO.Price != newOp.Price || originalMSO.Amount != newOp.Amount {
						opCounter.transformed++
					} else {
						opCounter.kept++
					}
				}
			} else {
				if !isNewOpNil {
					// newOp can be a transformed op to change the op to an effectively "dropped" state
					// prepend this so we always have delete commands at the beginning of the operation list
					filteredOps = append([]txnbuild.Operation{newOp}, filteredOps...)
					if originalOffer != nil {
						// we are dealing with an existing offer that needs dropping
						offerCounter.dropped++
					} else {
						// we are dealing with an operation that had updated an offer which now needs dropping
						// using the transformed counter here because we are changing the actual intent of the operation
						// from an update existing offer to delete existing offer logic
						opCounter.transformed++
					}
				} else {
					// newOp will never be nil for an original offer so the case will be handled in the if clause (!isNewOpNil)
					opCounter.dropped++
				}
			}
		default:
			filteredOps = append(filteredOps, o)
			opCounter.kept++
			opCounter.idx++
		}
	}

	// convert all remaining buy and sell offers to delete offers
	for sellCounter.idx < len(sellingOffers) {
		dropOp := convertOffer2MSO(sellingOffers[sellCounter.idx])
		dropOp.Amount = "0"
		filteredOps = append([]txnbuild.Operation{dropOp}, filteredOps...)
		sellCounter.dropped++
		sellCounter.idx++
	}
	for buyCounter.idx < len(buyingOffers) {
		dropOp := convertOffer2MSO(buyingOffers[buyCounter.idx])
		dropOp.Amount = "0"
		filteredOps = append([]txnbuild.Operation{dropOp}, filteredOps...)
		buyCounter.dropped++
		buyCounter.idx++
	}

	log.Printf("filter \"%s\" result A: dropped %d, transformed %d, kept %d ops from the %d ops passed in\n", filterName, opCounter.dropped, opCounter.transformed, opCounter.kept, len(ops))
	log.Printf("filter \"%s\" result B: dropped %d, transformed %d, kept %d, ignored %d sell offers from original %d sell offers\n", filterName, sellCounter.dropped, sellCounter.transformed, sellCounter.kept, sellCounter.ignored, len(sellingOffers))
	log.Printf("filter \"%s\" result C: dropped %d, transformed %d, kept %d, ignored %d buy offers from original %d buy offers\n", filterName, buyCounter.dropped, buyCounter.transformed, buyCounter.kept, buyCounter.ignored, len(buyingOffers))
	log.Printf("filter \"%s\" result D: len(filteredOps) = %d\n", filterName, len(filteredOps))
	return filteredOps, nil
}

func selectBuySellList(
	baseAsset hProtocol.Asset,
	quoteAsset hProtocol.Asset,
	mso *txnbuild.ManageSellOffer,
	sellingOffers []hProtocol.Offer,
	buyingOffers []hProtocol.Offer,
	sellCounter *filterCounter,
	buyCounter *filterCounter,
) ([]hProtocol.Offer, *filterCounter, error) {
	isSellOp, e := utils.IsSelling(baseAsset, quoteAsset, mso.Selling, mso.Buying)
	if e != nil {
		return nil, nil, fmt.Errorf("could not check whether the ManageSellOffer was selling or buying: %s", e)
	}

	if isSellOp {
		return sellingOffers, sellCounter, nil
	}
	return buyingOffers, buyCounter, nil
}

func selectOpOrOffer(
	offerList []hProtocol.Offer,
	offerCounter *filterCounter,
	mso *txnbuild.ManageSellOffer,
	opCounter *filterCounter,
	ignoreOfferIds map[int64]bool,
) (
	opToTransform *txnbuild.ManageSellOffer,
	originalOfferAsOp *txnbuild.ManageSellOffer,
	c *filterCounter,
	isIgnoredOffer bool,
	err error,
) {
	if offerCounter.idx >= len(offerList) {
		return mso, nil, opCounter, false, nil
	}

	existingOffer := offerList[offerCounter.idx]
	if _, ignoreOffer := ignoreOfferIds[existingOffer.ID]; ignoreOffer {
		// we want to only compare against valid offers so skip this offer by returning ignored = true
		return nil, nil, offerCounter, true, nil
	}

	offerPrice := float64(existingOffer.PriceR.N) / float64(existingOffer.PriceR.D)
	opPrice, e := strconv.ParseFloat(mso.Price, 64)
	if e != nil {
		return nil, nil, nil, false, fmt.Errorf("could not parse price as float64: %s", e)
	}

	// use the existing offer if the price is the same so we don't recreate an offer unnecessarily
	if opPrice < offerPrice {
		return mso, nil, opCounter, false, nil
	}

	offerAsOp := convertOffer2MSO(existingOffer)
	offerAsOpCopy := *offerAsOp
	return offerAsOp, &offerAsOpCopy, offerCounter, false, nil
}

func convertOffer2MSO(offer hProtocol.Offer) *txnbuild.ManageSellOffer {
	return &txnbuild.ManageSellOffer{
		Selling:       utils.Asset2Asset(offer.Selling),
		Buying:        utils.Asset2Asset(offer.Buying),
		Amount:        offer.Amount,
		Price:         offer.Price,
		OfferID:       offer.ID,
		SourceAccount: &txnbuild.SimpleAccount{AccountID: offer.Seller},
	}
}
