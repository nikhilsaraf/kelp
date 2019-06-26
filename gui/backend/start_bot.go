package backend

import (
	"fmt"
	"io/ioutil"
	"log"
	"net/http"

	"github.com/stellar/kelp/gui/model"
	"github.com/stellar/kelp/support/kelpos"
)

func (s *APIServer) startBot(w http.ResponseWriter, r *http.Request) {
	botNameBytes, e := ioutil.ReadAll(r.Body)
	if e != nil {
		s.writeError(w, fmt.Sprintf("error when reading request input: %s\n", e))
		return
	}

	botName := string(botNameBytes)
	e = s.doStartBot(botName, "buysell", nil, nil)
	if e != nil {
		s.writeError(w, fmt.Sprintf("error starting bot: %s\n", e))
		return
	}

	e = s.kos.AdvanceBotState(botName, kelpos.BotStateStopped)
	if e != nil {
		s.writeError(w, fmt.Sprintf("error advancing bot state: %s\n", e))
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (s *APIServer) doStartBot(botName string, strategy string, iterations *uint8, maybeFinishCallback func()) error {
	filenamePair := model.GetBotFilenames(botName, strategy)
	logPrefix := model.GetLogPrefix(botName, strategy)

	botConfigPath := fmt.Sprintf("%s/%s", s.configsDir, filenamePair.Trader)
	stratConfigPath := fmt.Sprintf("%s/%s", s.configsDir, filenamePair.Strategy)
	logPrefixInput := fmt.Sprintf("%s/%s", s.logsDir, logPrefix)
	operationalBuffer := float64(20)
	operationalBufferNonNativePct := 0.001
	fls := false
	zeroUi64 := uint64(0)
	inputs := kelpos.Inputs{
		BotConfigPath:                 &botConfigPath,
		Strategy:                      &strategy,
		StratConfigPath:               &stratConfigPath,
		LogPrefix:                     &logPrefixInput,
		OperationalBuffer:             &operationalBuffer,
		OperationalBufferNonNativePct: &operationalBufferNonNativePct,
		WithIPC:         &fls,
		SimMode:         &fls,
		FixedIterations: &zeroUi64,
		NoHeaders:       &fls,
	}
	if iterations != nil {
		ui64 := uint64(*iterations)
		inputs.FixedIterations = &ui64
	}
	log.Printf("run command for inputs: %v\n", inputs)
	go s.runTradeCmd(inputs)

	// go func(kelpCommand *exec.Cmd, name string) {
	// 	defer s.kos.SafeUnregister(name)

	// 	if kelpCommand == nil {
	// 		log.Printf("kelpCommand was nil for bot '%s' with strategy '%s'\n", name, strategy)
	// 		return
	// 	}

	// 	e := kelpCommand.Wait()
	// 	if e != nil {
	// 		if strings.Contains(e.Error(), "signal: terminated") {
	// 			log.Printf("terminated start bot command for bot '%s' with strategy '%s'\n", name, strategy)
	// 			return
	// 		}
	// 		log.Printf("error when starting bot '%s' with strategy '%s': %s\n", name, strategy, e)
	// 		return
	// 	}

	// 	log.Printf("finished start bot command for bot '%s' with strategy '%s'\n", name, strategy)
	// 	if maybeFinishCallback != nil {
	// 		maybeFinishCallback()
	// 	}
	// }(p.Cmd, botName)

	return nil
}
