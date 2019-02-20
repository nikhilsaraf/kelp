package plugins

const pathOpFeeStats = "/operation_fee_stats"

// FeeStatsResponse represents the response from /operation_fee_stats
type FeeStatsResponse struct {
	LastLedger        string `json:"last_ledger"`          // uint64 as a string
	LastLedgerBaseFee string `json:"last_ledger_base_fee"` // uint64 as a string
	MinAcceptedFee    string `json:"min_accepted_fee"`     // uint64 as a string
	ModeAcceptedFee   string `json:"mode_accepted_fee"`    // uint64 as a string
}

func getBaseFee(horizonBaseURL string, maxBaseFee uint64) (uint64, error) {
	return maxBaseFee, nil
	// feeStatsURL := horizonBaseURL + pathOpFeeStats
	// output := FeeStatsResponse{}
	// e := networking.Request(http.DefaultClient, "POST", feeStatsURL, "", map[string]string{}, &output, false)
	// if e != nil {
	// 	return 0, fmt.Errorf("error fetching fee stats (%s): %s", feeStatsURL, e)
	// }

	// lastFeeInt, e := strconv.Atoi(output.LastLedgerBaseFee)
	// if e != nil {
	// 	return 0, fmt.Errorf("could not parse last_ledger_base_fee (%s) as int: %s", output.LastLedgerBaseFee, e)
	// }
	// modeFeeInt, e := strconv.Atoi(output.ModeAcceptedFee)
	// if e != nil {
	// 	return 0, fmt.Errorf("could not parse mode_accepted_fee (%s) as int: %s", output.ModeAcceptedFee, e)
	// }
	// lastFee := uint64(lastFeeInt)
	// modeFee := uint64(modeFeeInt)

	// if lastFee >= modeFee && lastFee <= maxBaseFee {
	// 	log.Printf("using last_ledger_base_fee of %d stroops (maxBaseFee = %d)\n", lastFee, maxBaseFee)
	// 	return lastFee, nil
	// }
	// if modeFee >= lastFee && modeFee <= maxBaseFee {
	// 	log.Printf("using mode_accepted_fee of %d stroops (maxBaseFee = %d)\n", modeFee, maxBaseFee)
	// 	return modeFee, nil
	// }
	// log.Printf("using maxBaseFee of %d stroops (last_ledger_base_fee = %d; mode_accepted_fee = %d)\n", maxBaseFee, lastFee, modeFee)
	// return maxBaseFee, nil
}
