package filter

import (
	"context"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/adshao/go-binance/v2"
	"github.com/adshao/go-binance/v2/futures"
	"go.uber.org/atomic"
	"golang.org/x/text/language"
	"golang.org/x/text/message"

	"alertbot/utils/list"
)

const (
	freq        int64 = 1 * 60 * 24
	milliInSec        = 1000
	milliInMin        = milliInSec * 60
	milliInHour       = milliInMin * 60
	milliInDay        = milliInMin * 24
)

const (
	UP     string = "UP"
	DOWN          = "DOWN"
	BUY           = "BUY"
	SELL          = "SELL"
	FBUY          = "FBUY"
	FSELL         = "FSELL"
	SYSTEM        = "SYSTEM"
	ALL           = "ALL"
)

type marketdata struct {
	Price       float64
	BaseVolume  float64
	QuoteVolume float64
	Time        int64
}

type alertdata struct {
	Time       int64
	UpNumber   int
	DownNumber int
}

// BinanceFilter for Binance data filtering
type BinanceFilter struct {
	symbols       map[string]*atomic.Bool
	fsymbolLevels map[string]time.Duration
	market        map[string]*list.List
	funding       map[string]*atomic.Float64
	alert         map[string]*alertdata
	channel       map[string]*atomic.Bool
	ignored       map[string]struct{} // not thread-safe

	sRateThreshold    *atomic.Float64
	fRateThreshold    *atomic.Float64
	largeSThreshold   *atomic.Float64
	largeFThreshold   *atomic.Float64
	upThreshold       *atomic.Float64
	downThreshold     *atomic.Float64
	volumeThreshold   *atomic.Float64
	minQuoteThreshold *atomic.Float64
	maxQuoteThreshold *atomic.Float64
	windowThreshold   *atomic.Int64

	stopCMarketsStatServe        chan struct{}
	stopCCombinedTrade           chan struct{}
	stopCFutureCombinedTrade     chan struct{}
	stopCFutureCombinedMarkPrice chan struct{}
	runningC                     chan struct{}

	futureFilter *atomic.String

	postMessageBackend func(string)
	printer            *message.Printer
	localTime          *time.Location
}

// New create BinanceFilter
func New(postMessage func(string), location string) *BinanceFilter {
	symbols := make(map[string]*atomic.Bool)
	market := make(map[string]*list.List)
	alert := make(map[string]*alertdata)
	channel := map[string]*atomic.Bool{
		UP:    atomic.NewBool(true),
		DOWN:  atomic.NewBool(true),
		BUY:   atomic.NewBool(true),
		SELL:  atomic.NewBool(true),
		FBUY:  atomic.NewBool(true),
		FSELL: atomic.NewBool(true),
		ALL:   atomic.NewBool(true),
		// SYSTEM: atomic.NewBool(true),
	}
	localTime, _ := time.LoadLocation(location)

	bf := BinanceFilter{
		symbols: symbols,
		market:  market,
		alert:   alert,
		channel: channel,
		ignored: make(map[string]struct{}),

		sRateThreshold:    atomic.NewFloat64(5.0),
		fRateThreshold:    atomic.NewFloat64(10.0),
		upThreshold:       atomic.NewFloat64(2.0),
		downThreshold:     atomic.NewFloat64(-5.0),
		volumeThreshold:   atomic.NewFloat64(2.0),
		minQuoteThreshold: atomic.NewFloat64(10_000_000),
		maxQuoteThreshold: atomic.NewFloat64(500_000_000),
		largeSThreshold:   atomic.NewFloat64(1_000_000),
		largeFThreshold:   atomic.NewFloat64(2_000_000),
		windowThreshold:   atomic.NewInt64(2 * milliInMin),

		stopCMarketsStatServe:        make(chan struct{}),
		stopCCombinedTrade:           make(chan struct{}),
		stopCFutureCombinedTrade:     make(chan struct{}),
		stopCFutureCombinedMarkPrice: make(chan struct{}),
		runningC:                     make(chan struct{}),

		futureFilter: atomic.NewString(""),

		postMessageBackend: postMessage,
		printer:            message.NewPrinter(language.English),
		localTime:          localTime,
	}

	if err := bf.updateData(); err != nil {
		panic(err)
	}

	return &bf
}

// Start to filter Binance's events
func (bf *BinanceFilter) Start() {
	go bf.handleWsAllMarketsStat()
	go bf.handleWsFutureCombinedTrade()
	go bf.handleWsFutureCombinedMarkPriceServeWithRate()
	go bf.handleWsCombinedTrade()

	<-bf.runningC
}

func (bf *BinanceFilter) handleWsAllMarketsStat() {
	wsHandler := func(events binance.WsAllMarketsStatEvent) {
		// start := time.Now()
		for _, ev := range events {
			if _, found := bf.market[ev.Symbol]; !found {
				continue
			}

			if _, found := bf.ignored[ev.Symbol]; found {
				continue
			}

			if ev.CloseTime/milliInSec <= bf.market[ev.Symbol].Back().Value.(*marketdata).Time/milliInSec {
				continue
			}

			baseVolume, err := strconv.ParseFloat(ev.BaseVolume, 64)
			if err != nil {
				continue
			}

			quoteVolume, err := strconv.ParseFloat(ev.QuoteVolume, 64)
			if err != nil {
				continue
			}

			askPrice, err := strconv.ParseFloat(ev.AskPrice, 64)
			if err != nil {
				continue
			}

			bf.market[ev.Symbol].Push(&marketdata{Price: askPrice, BaseVolume: baseVolume, QuoteVolume: quoteVolume, Time: ev.CloseTime})

			if quoteVolume < bf.minQuoteThreshold.Load() || quoteVolume > bf.maxQuoteThreshold.Load() {
				continue
			}

			if ev.CloseTime < bf.alert[ev.Symbol].Time+bf.windowThreshold.Load() {
				continue
			}

			minElement, maxElement, firstElement := bf.market[ev.Symbol].MinAndMax(bf.compare(ev.CloseTime - bf.windowThreshold.Load()))
			if minElement.Value.(*marketdata).Price == 0 || maxElement.Value.(*marketdata).Price == 0 {
				continue
			}

			minPrice := minElement.Value.(*marketdata).Price
			upRate := (askPrice - minPrice) * 100 / minPrice
			maxPrice := maxElement.Value.(*marketdata).Price
			downRate := (askPrice - maxPrice) * 100 / maxPrice

			if upRate >= 0 && upRate < bf.upThreshold.Load() ||
				downRate < 0 && downRate > bf.downThreshold.Load() {
				continue
			}

			minVolume := firstElement.Value.(*marketdata).QuoteVolume
			maxVolume := bf.market[ev.Symbol].Back().Value.(*marketdata).QuoteVolume
			volumeRate := (maxVolume - minVolume) * 100 / minVolume

			if volumeRate < bf.volumeThreshold.Load() {
				continue
			}

			var priceRate float64
			var updown string
			var updownNumber int
			// UP
			if upRate >= bf.upThreshold.Load() {
				priceRate = upRate
				updown = "UP"
				if ev.CloseTime <= bf.alert[ev.Symbol].Time+2*bf.windowThreshold.Load() {
					bf.alert[ev.Symbol].UpNumber++
				} else {
					bf.alert[ev.Symbol].UpNumber = 1
				}
				updownNumber = bf.alert[ev.Symbol].UpNumber
			}

			// DOWN
			if downRate <= bf.downThreshold.Load() {
				priceRate = downRate
				updown = "DOWN"
				if ev.CloseTime <= bf.alert[ev.Symbol].Time+2*bf.windowThreshold.Load() {
					bf.alert[ev.Symbol].DownNumber++
				} else {
					bf.alert[ev.Symbol].DownNumber = 1
				}
				updownNumber = bf.alert[ev.Symbol].DownNumber
			}

			bf.alert[ev.Symbol].Time = ev.CloseTime

			future := "S"
			if bf.symbols[ev.Symbol].Load() {
				future = "F"
			}
			msg := fmt.Sprintf("<b>#%s(%d) #%s(%s)</b>: <u>%4.2f-%4.2f</u> P: <u>%s</u> V: %s T: %s",
				updown, updownNumber, ev.Symbol[:len(ev.Symbol)-4], future, priceRate, volumeRate, strconv.FormatFloat(askPrice, 'f', -1, 64),
				bf.printer.Sprintf("%d", int64(quoteVolume)), time.Now().In(bf.localTime).Format("15:04:05 2006-01-02"))
			log.Println(msg)
			bf.postMessage(updown, msg)
		}
		// fmt.Printf("took %s\n", time.Since(start))
	}

	errHandler := func(err error) {
		log.Println(err)
	}

	doneC, stopC, err := binance.WsAllMarketsStatServe(wsHandler, errHandler)
	if err != nil {
		panic(err)
	}
	bf.stopCMarketsStatServe = stopC
	<-doneC
}

func (bf *BinanceFilter) handleWsCombinedTrade() {
	wsCombinedTradeHandler := func(event *binance.WsCombinedTradeEvent) {
		data := event.Data

		if _, found := bf.ignored[data.Symbol]; found {
			return
		}

		quantity, err := strconv.ParseFloat(data.Quantity, 64)
		if err != nil {
			return
		}

		price, err := strconv.ParseFloat(data.Price, 64)
		if err != nil {
			return
		}

		maketData := bf.market[data.Symbol].Back().Value.(*marketdata)

		if maketData.BaseVolume == 0 || maketData.QuoteVolume < bf.minQuoteThreshold.Load() || maketData.QuoteVolume > bf.maxQuoteThreshold.Load() {
			return
		}

		rate := quantity * 100 / maketData.BaseVolume
		value := quantity * price
		channel := BUY
		if rate >= bf.sRateThreshold.Load() || value >= bf.largeSThreshold.Load() {
			future := "S"
			if bf.symbols[data.Symbol].Load() {
				future = "F"
			}

			msg := fmt.Sprintf("<b>#BUY #%s(%s)</b>", data.Symbol[:len(data.Symbol)-4], future)
			if data.IsBuyerMaker {
				channel = SELL
				msg = fmt.Sprintf("<b>#SELL #%s(%s)</b>", data.Symbol[:len(data.Symbol)-4], future)
			}

			msg = fmt.Sprintf("%s <u>%4.2f</u> P: <u>%s</u> V: %s Q: %s %s",
				msg, rate, strconv.FormatFloat(price, 'f', -1, 64), bf.printer.Sprintf("%d", int(value)),
				bf.printer.Sprintf("%d", int(quantity)), time.Now().In(bf.localTime).Format("15:04:05 2006-01-02"))
			log.Println(msg)
			bf.postMessage(channel, msg)
		}
	}

	errHandler := func(err error) {
		panic(err)
	}

	symbols := []string{}
	for symbol := range bf.symbols {
		symbols = append(symbols, symbol)
	}

	doneC, stopC, err := binance.WsCombinedTradeServe(symbols, wsCombinedTradeHandler, errHandler)
	if err != nil {
		panic(err)
	}
	bf.stopCCombinedTrade = stopC
	<-doneC
}

func (bf *BinanceFilter) handleWsFutureCombinedTrade() {
	wsCombinedTradeHandler := func(event *futures.WsAggTradeEvent) {
		if _, found := bf.ignored[event.Symbol]; found {
			return
		}

		if strings.HasPrefix(event.Symbol, "BTC") || strings.HasPrefix(event.Symbol, "ETH") {
			return
		}

		if bf.futureFilter.Load() != "" && !strings.HasPrefix(event.Symbol, bf.futureFilter.Load()) {
			return
		}

		quantity, err := strconv.ParseFloat(event.Quantity, 64)
		if err != nil {
			return
		}

		price, err := strconv.ParseFloat(event.Price, 64)
		if err != nil {
			return
		}

		maketData := bf.market[event.Symbol].Back().Value.(*marketdata)

		if maketData.BaseVolume == 0 || maketData.QuoteVolume < bf.minQuoteThreshold.Load() || maketData.QuoteVolume > bf.maxQuoteThreshold.Load() {
			return
		}

		rate := quantity * 100 / maketData.BaseVolume
		value := quantity * price
		channel := FBUY
		if bf.futureFilter.Load() != "" ||
			(bf.futureFilter.Load() == "" && rate >= bf.fRateThreshold.Load() || value >= bf.largeFThreshold.Load()) {
			msg := fmt.Sprintf("<b>#FBUY #%s #R%d</b>", event.Symbol[:len(event.Symbol)-4], int(rate+0.5))
			if event.Maker {
				channel = FSELL
				msg = fmt.Sprintf("<b>#FSELL #%s #R%d</b>", event.Symbol[:len(event.Symbol)-4], int(rate+0.5))
			}

			msg = fmt.Sprintf("%s <u>%4.2f</u> P: <u>%s</u> V: %s Q: %s %s",
				msg, rate, strconv.FormatFloat(price, 'f', -1, 64), bf.printer.Sprintf("%d", int(value)),
				bf.printer.Sprintf("%d", int(quantity)), time.Now().In(bf.localTime).Format("15:04:05 2006-01-02"))
			log.Println(msg)
			bf.postMessage(channel, msg)
		}
	}

	errHandler := func(err error) {
		panic(err)
	}

	symbols := []string{}
	for symbol := range bf.symbols {
		if bf.symbols[symbol].Load() {
			symbols = append(symbols, symbol)
		}
	}

	doneC, stopC, err := futures.WsCombinedAggTradeServe(symbols, wsCombinedTradeHandler, errHandler)
	if err != nil {
		panic(err)
	}
	bf.stopCFutureCombinedTrade = stopC
	<-doneC
}

func (bf *BinanceFilter) handleWsFutureCombinedMarkPriceServeWithRate() {
	wsCombinedMarkPriceHandler := func(event *futures.WsMarkPriceEvent) {
		fundingRate, err := strconv.ParseFloat(event.FundingRate, 64)
		if err != nil {
			return
		}

		fundingRate *= 100
		bf.funding[event.Symbol].Store(fundingRate)
	}

	errHandler := func(err error) {
		panic(err)
	}

	symbols := []string{}
	for symbol := range bf.symbols {
		if bf.symbols[symbol].Load() {
			symbols = append(symbols, symbol)
		}
	}

	doneC, stopC, err := futures.WsCombinedMarkPriceServeWithRate(bf.fsymbolLevels, wsCombinedMarkPriceHandler, errHandler)
	if err != nil {
		panic(err)
	}
	bf.stopCFutureCombinedMarkPrice = stopC
	<-doneC
}

func (bf *BinanceFilter) compare(t int64) func(a *list.Element, b *list.Element) int {
	return func(a *list.Element, b *list.Element) int {
		if a.Value.(*marketdata).Price*b.Value.(*marketdata).Price == 0 {
			return -1
		}

		if a.Value.(*marketdata).Time < t {
			return -1
		}

		if a.Value.(*marketdata).Price >= b.Value.(*marketdata).Price {
			return 1
		}

		return 0
	}
}

func (bf *BinanceFilter) updateData() error {
	client := binance.NewClient(os.Getenv("BINANCE_API_KEY"), os.Getenv("BINANCE_SECRET_KEY"))
	res, err := client.NewExchangeInfoService().Symbols().Do(context.Background())
	if err != nil {
		return err
	}

	fclient := binance.NewFuturesClient(os.Getenv("BINANCE_API_KEY"), os.Getenv("BINANCE_SECRET_KEY"))
	fres, ferr := fclient.NewExchangeInfoService().Do(context.Background())
	if ferr != nil {
		return ferr
	}

	log.Println("Number of symbols:", len(res.Symbols))

	bf.fsymbolLevels = make(map[string]time.Duration)
	bf.funding = make(map[string]*atomic.Float64)
	for _, e := range fres.Symbols {
		bf.fsymbolLevels[e.Symbol] = 3 * time.Second
		bf.funding[e.Symbol] = atomic.NewFloat64(0)
	}

	for _, e := range res.Symbols {
		if !(strings.HasSuffix(e.Symbol, "USDT") /*|| strings.HasSuffix(e.Symbol, "USD") || strings.HasSuffix(e.Symbol, "BTC")*/) ||
			strings.HasSuffix(e.Symbol, "USDUSDT") ||
			strings.HasPrefix(e.Symbol, "USD") {
			continue
		}

		if _, found := bf.market[e.Symbol]; !found {
			bf.market[e.Symbol] = list.NewList(60*60, &marketdata{Price: 0, BaseVolume: 0, QuoteVolume: 0, Time: 0})
			bf.alert[e.Symbol] = &alertdata{Time: 0, UpNumber: 0, DownNumber: 0}
			bf.symbols[e.Symbol] = atomic.NewBool(false)
		}

		if _, found := bf.fsymbolLevels[e.Symbol]; found && !bf.symbols[e.Symbol].Load() {
			bf.symbols[e.Symbol].Store(true)
		}
	}

	for symbol := range bf.symbols {
		if _, found := bf.fsymbolLevels[symbol]; !found && bf.symbols[symbol].Load() {
			bf.symbols[symbol].Store(false)
		}
	}

	return nil
}

func (bf *BinanceFilter) postMessage(c string, s string) {
	if c == SYSTEM || (bf.channel[ALL].Load() && bf.channel[c].Load()) {
		bf.postMessageBackend(s)
	}
}
