package filter

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Ignore filter Binance's message
func (bf *BinanceFilter) Ignore(s string) {
	bf.ignored[strings.ToUpper(s+"USDT")] = struct{}{}
	bf.postMessage(SYSTEM, fmt.Sprintf("%s ignored", strings.ToUpper(s+"USDT")))
}

// Unignore filter Binance's message
func (bf *BinanceFilter) Unignore(s string) {
	if _, found := bf.ignored[strings.ToUpper(s+"USDT")]; found {
		delete(bf.ignored, strings.ToUpper(s+"USDT"))
		bf.postMessage(SYSTEM, fmt.Sprintf("%s unignored", strings.ToUpper(s+"USDT")))
	} else {
		bf.postMessage(SYSTEM, fmt.Sprintf("%s not found", strings.ToUpper(s+"USDT")))
	}
}

// Mute filter Binance's message
func (bf *BinanceFilter) Mute(s string) {
	errCheck := func(err bool) bool {
		if err {
			bf.postMessage(SYSTEM, "wrong format")
		}
		return err
	}

	if errCheck(s == "") {
		return
	}

	channel := strings.ToUpper(s)
	if _, found := bf.channel[channel]; errCheck(!found) {
		return
	}

	bf.channel[channel].Store(false)
	bf.postMessage(SYSTEM, "muted")
}

// Unmute filter Binance's message
func (bf *BinanceFilter) Unmute(content string) {
	s := strings.Fields(content)
	errCheck := func(err bool) bool {
		if err {
			bf.postMessage(SYSTEM, "wrong format")
		}
		return err
	}

	if errCheck(len(s) != 1) {
		return
	}

	channel := strings.ToUpper(s[0])
	if channel == ALL {
		for c := range bf.channel {
			if !bf.channel[c].Load() {
				bf.channel[c].Store(true)
			}
		}
	} else {
		bf.channel[channel].Store(true)
		if !bf.channel[ALL].Load() {
			bf.channel[ALL].Store(true)
		}
	}
	bf.postMessage(SYSTEM, "unmuted")
}

// Filter symbol
func (bf *BinanceFilter) Filter(s string) {
	bf.futureFilter.Store(strings.ToUpper(s))
}

// Clear filter symbol
func (bf *BinanceFilter) Clear(s string) {
	bf.futureFilter.Store("")
}

// Price get
func (bf *BinanceFilter) Price(s string) {
	symbol := strings.ToUpper(s + "USDT")
	if _, found := bf.market[symbol]; found {
		bf.postMessage(SYSTEM, strconv.FormatFloat(bf.market[symbol].Back().Value.(*marketdata).Price, 'f', -1, 64))
	} else {
		bf.postMessage(SYSTEM, "not found")
	}
}

// FundingRate get
func (bf *BinanceFilter) FundingRate(s string) {
	symbol := strings.ToUpper(s)
	if !strings.Contains(s, "USDT") {
		symbol += "USDT"
	}
	if _, found := bf.funding[symbol]; found {
		bf.postMessage(SYSTEM, fmt.Sprintf("%0.4f", bf.funding[symbol].Load()))
	} else {
		bf.postMessage(SYSTEM, "not found")
	}
}

// FundingRateTop get
func (bf *BinanceFilter) FundingRateTop(s string) {
	top, err := strconv.ParseUint(s, 10, 64)
	if err != nil || int(top) >= len(bf.funding) {
		top = 3
	}
	symbols := make([]string, 0, len(bf.funding))
	for symbol := range bf.funding {
		symbols = append(symbols, symbol)
	}

	sort.SliceStable(symbols, func(i, j int) bool {
		return bf.funding[symbols[i]].Load() > bf.funding[symbols[j]].Load()
	})

	ret := ""
	for _, symbol := range symbols[:top] {
		ret = ret + fmt.Sprintf("%s: %s\n", symbol, fmt.Sprintf("%0.4f", bf.funding[symbol].Load()))
	}

	bf.postMessage(SYSTEM, ret)
}

// FundingRateBottom get
func (bf *BinanceFilter) FundingRateBottom(s string) {
	bot, err := strconv.ParseUint(s, 10, 64)
	if err != nil || int(bot) >= len(bf.funding) {
		bot = 3
	}
	symbols := make([]string, 0, len(bf.funding))
	for symbol := range bf.funding {
		symbols = append(symbols, symbol)
	}

	sort.SliceStable(symbols, func(i, j int) bool {
		return bf.funding[symbols[i]].Load() > bf.funding[symbols[j]].Load()
	})

	ret := ""
	for i := 1; i <= int(bot); i++ {
		ret = ret + fmt.Sprintf("%s: %s\n", symbols[len(symbols)-i], fmt.Sprintf("%0.4f", bf.funding[symbols[len(symbols)-i]].Load()))
	}

	bf.postMessage(SYSTEM, ret)
}

// Restart to filter Binance's events
func (bf *BinanceFilter) Restart(settings string) {
	bf.stopCMarketsStatServe <- struct{}{}
	bf.stopCCombinedTrade <- struct{}{}
	bf.stopCFutureCombinedTrade <- struct{}{}
	bf.stopCFutureCombinedMarkPrice <- struct{}{}

	go bf.handleWsAllMarketsStat()
	go bf.handleWsFutureCombinedTrade()
	go bf.handleWsCombinedTrade()
}

// UpdateConfiguration from message bot command
func (bf *BinanceFilter) UpdateConfiguration(settings string) {
	s := strings.Fields(settings)
	errCheck := func(err bool) bool {
		if err {
			bf.postMessage(SYSTEM, "wrong format")
		}
		return err
	}

	if errCheck(len(s) != 2) {
		return
	}

	configurable := map[string]struct{}{
		"srate":     {},
		"frate":     {},
		"minvolume": {},
		"maxvolume": {},
		"slarge":    {},
		"flarge":    {},
		"window":    {},
		"up":        {},
		"down":      {},
		"volume":    {},
	}

	if _, found := configurable[s[0]]; errCheck(!found) {
		return
	}

	threshold, err := strconv.ParseFloat(s[1], 64)
	if errCheck(err != nil) {
		return
	}

	switch {
	case s[0] == "srate":
		if errCheck(threshold <= 0) {
			return
		}
		bf.sRateThreshold.Store(threshold)
		bf.postMessage(SYSTEM, fmt.Sprintf("SRate to %0.2f%%\n", threshold))
	case s[0] == "frate":
		if errCheck(threshold <= 0) {
			return
		}
		bf.fRateThreshold.Store(threshold)
		bf.postMessage(SYSTEM, fmt.Sprintf("FRate to %0.2f%%\n", threshold))
	case s[0] == "minvolume":
		if errCheck(threshold <= 0) {
			return
		}
		bf.minQuoteThreshold.Store(threshold)
		bf.postMessage(SYSTEM, bf.printer.Sprintf("Min Volume to %d$\n", int64(threshold)))
	case s[0] == "maxvolume":
		if errCheck(threshold <= 0) {
			return
		}
		bf.maxQuoteThreshold.Store(threshold)
		bf.postMessage(SYSTEM, bf.printer.Sprintf("Max Volume to %d$\n", int64(threshold)))
	case s[0] == "slarge":
		if errCheck(threshold <= 0) {
			return
		}
		bf.largeSThreshold.Store(threshold)
		bf.postMessage(SYSTEM, bf.printer.Sprintf("SLarge to %d$\n", int64(threshold)))
	case s[0] == "flarge":
		if errCheck(threshold <= 0) {
			return
		}
		bf.largeFThreshold.Store(threshold)
		bf.postMessage(SYSTEM, bf.printer.Sprintf("FLarge to %d$\n", int64(threshold)))
	case s[0] == "window":
		if errCheck(threshold <= 0 || threshold > 60) {
			return
		}
		bf.windowThreshold.Store(int64(threshold * float64(milliInMin)))
		bf.postMessage(SYSTEM, fmt.Sprintf("Window to %0.2f minutes(s)", threshold))
	case s[0] == "up":
		if errCheck(threshold <= 0) {
			return
		}
		bf.upThreshold.Store(threshold)
		bf.postMessage(SYSTEM, fmt.Sprintf("Up to %0.2f%%\n", threshold))
	case s[0] == "down":
		if errCheck(threshold >= 0) {
			return
		}
		bf.downThreshold.Store(threshold)
		bf.postMessage(SYSTEM, fmt.Sprintf("Down to %0.2f%%\n", threshold))
	case s[0] == "volume":
		if errCheck(threshold <= 0) {
			return
		}
		bf.volumeThreshold.Store(threshold)
		bf.postMessage(SYSTEM, fmt.Sprintf("Volume to %0.2f%%\n", threshold))
	}
}

// UpdateData from message bot command
func (bf *BinanceFilter) UpdateData(settings string) {
	if bf.updateData() == nil {
		bf.postMessage(SYSTEM, "updated")
	} else {
		bf.postMessage(SYSTEM, "failed")
	}
}

// GetConfiguration list current configuration
func (bf *BinanceFilter) GetConfiguration(setting string) {
	if setting != "config" {
		bf.postMessage(SYSTEM, "unsupported")
		return
	}

	bf.postMessage(SYSTEM, bf.printer.Sprintf("SRate: %0.2f%%\nFRate: %0.2f%%\nMin Volume: %d$\nMax Volume: %d$\nSLarge: %d$\nFLarge: %d$\nWindow: %0.2f minute(s)\nUp: %0.2f%%\nDown: %0.2f%%\nVolume: %0.2f%%",
		bf.sRateThreshold.Load(), bf.fRateThreshold.Load(), int64(bf.minQuoteThreshold.Load()), int64(bf.maxQuoteThreshold.Load()), int64(bf.largeSThreshold.Load()), int64(bf.largeFThreshold.Load()),
		float64(bf.windowThreshold.Load())/float64(milliInMin), bf.upThreshold.Load(), bf.downThreshold.Load(), bf.volumeThreshold.Load()))
}
