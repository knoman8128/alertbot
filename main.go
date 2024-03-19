package main

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/joho/godotenv"

	binancefilter "alertbot/binance"
	telegrambot "alertbot/telegram"
)

func main() {
	godotenv.Load(".env")
	loc, _ := time.LoadLocation(os.Getenv("LOCATION_TIME"))

	log.SetFlags(0)
	log.SetOutput(new(logWriter))

	messenger := telegrambot.New(os.Getenv("TELEGRAM_USERID"), os.Getenv("TELEGRAM_TOKEN"))
	filter := binancefilter.New(func(message string) {
		messenger.PostMessage(message)
	}, os.Getenv("LOCATION_TIME"))

	messenger.RegisterCommands([]string{"/update"}, func(content string) { filter.UpdateData(content) })
	messenger.RegisterCommands([]string{"/set", "/s"}, func(content string) { filter.UpdateConfiguration(content) })
	messenger.RegisterCommands([]string{"/get", "/g"}, func(content string) { filter.GetConfiguration(content) })
	messenger.RegisterCommands([]string{"/mute"}, func(content string) { filter.Mute(content) })
	messenger.RegisterCommands([]string{"/unmute"}, func(content string) { filter.Unmute(content) })
	messenger.RegisterCommands([]string{"/restart"}, func(content string) { filter.Restart(content) })
	messenger.RegisterCommands([]string{"/filter"}, func(content string) { filter.Filter(content) })
	messenger.RegisterCommands([]string{"/clear"}, func(content string) { filter.Clear(content) })
	messenger.RegisterCommands([]string{"/ignore"}, func(content string) { filter.Ignore(content) })
	messenger.RegisterCommands([]string{"/unignore"}, func(content string) { filter.Unignore(content) })
	messenger.RegisterCommands([]string{"/price", "/p"}, func(content string) { filter.Price(content) })
	messenger.RegisterCommands([]string{"/fr", "/f"}, func(content string) { filter.FundingRate(content) })
	messenger.RegisterCommands([]string{"/frtop", "/ft"}, func(content string) { filter.FundingRateTop(content) })
	messenger.RegisterCommands([]string{"/frbot", "/fb"}, func(content string) { filter.FundingRateBottom(content) })

	go func() { messenger.Start() }()
	messenger.PostMessage(fmt.Sprintf("Started %s", time.Now().In(loc).Format("2006-01-02 15:04:05 MST")))
	filter.Start()
}

type logWriter struct {
}

func (writer logWriter) Write(bytes []byte) (int, error) {
	loc, _ := time.LoadLocation(os.Getenv("LOCATION_TIME"))
	logEntry := time.Now().In(loc).Format("2006-01-02 15:04:05 MST") + " " + string(bytes)
	logFile, err := os.OpenFile(os.Getenv("LOGFILE_LOCATION"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		return 0, err
	}
	defer logFile.Close()

	_, err = logFile.WriteString(logEntry)
	if err != nil {
		return 0, err
	}

	return len(logEntry), nil
}
