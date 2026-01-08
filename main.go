package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/joho/godotenv"
	chart "github.com/wcharczuk/go-chart/v2"
)

// KlineResponse - структура API відповіді для свічок
type KlineResponse struct {
	RetCode int    `json:"retCode"`
	RetMsg  string `json:"retMsg"`
	Result  struct {
		List [][]interface{} `json:"list"`
	} `json:"result"`
}

func tgHandleKline(symbol string) string {
	if symbol == "" {
		return "Вкажіть символ, наприклад: /kline BTCUSDT"
	}
	url := fmt.Sprintf("https://api.bybit.com/v5/market/kline?category=spot&symbol=%s&interval=1&limit=5", symbol)
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return "Помилка HTTP: " + err.Error()
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "Помилка читання: " + err.Error()
	}
	log.Printf("Bybit kline response for %s: %s", symbol, string(body))
	var kline KlineResponse
	if err := json.Unmarshal(body, &kline); err != nil {
		return "Помилка JSON: " + err.Error()
	}
	if kline.RetCode != 0 {
		return "Помилка API: " + kline.RetMsg
	}
	if len(kline.Result.List) == 0 {
		return "Немає даних для " + symbol
	}
	// Візуалізація: графік закриття
	closes := make([]float64, 0, len(kline.Result.List))
	for _, item := range kline.Result.List {
		if len(item) < 5 {
			continue
		}
		closeVal, ok := item[4].(string)
		if !ok {
			continue
		}
		f, _ := parseFloat(closeVal)
		closes = append(closes, f)
	}
	maxClose := 0.0
	for _, v := range closes {
		if v > maxClose {
			maxClose = v
		}
	}
	res := fmt.Sprintf("Останні 5 свічок %s (закриття):\n", symbol)
	for i, v := range closes {
		barLen := 0
		if maxClose > 0 {
			barLen = int((v / maxClose) * 20)
		}
		bar := strings.Repeat("█", barLen)
		res += fmt.Sprintf("`%d: %8.2f %s`\n", i+1, v, bar)
	}
	return res
}

func tgHandleKlinePhoto(symbolsRaw string, bot *tgbotapi.BotAPI, chatID int64) string {
	symbols := strings.Split(symbolsRaw, ",")
	if len(symbols) == 0 || (len(symbols) == 1 && strings.TrimSpace(symbols[0]) == "") {
		return "Вкажіть символи через кому, наприклад: /klinephoto BTCUSDT,ETHUSDT"
	}
	series := []chart.Series{}
	legend := []string{}
	maxLen := 0
	var errors []string
	for _, symbol := range symbols {
		symbol = strings.TrimSpace(symbol)
		if symbol == "" {
			continue
		}
		url := fmt.Sprintf("https://api.bybit.com/v5/market/kline?category=spot&symbol=%s&interval=1&limit=20", symbol)
		log.Printf("Requesting URL: %s", url)
		client := &http.Client{Timeout: 10 * time.Second}
		resp, err := client.Get(url)
		if err != nil {
			log.Printf("HTTP error for %s: %v", symbol, err)
			errors = append(errors, fmt.Sprintf("%s: Помилка HTTP", symbol))
			continue
		}
		log.Printf("HTTP status for %s: %d %s", symbol, resp.StatusCode, resp.Status)
		log.Printf("HTTP headers for %s: %v", symbol, resp.Header)
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			log.Printf("Read error for %s: %v", symbol, err)
			errors = append(errors, fmt.Sprintf("%s: Помилка читання", symbol))
			continue
		}
		log.Printf("Bybit kline response for %s: %s", symbol, string(body))
		var kline KlineResponse
		if err := json.Unmarshal(body, &kline); err != nil {
			log.Printf("JSON error for %s: %v", symbol, err)
			errors = append(errors, fmt.Sprintf("%s: Помилка JSON", symbol))
			continue
		}
		if kline.RetCode != 0 || len(kline.Result.List) == 0 {
			log.Printf("API error for %s: %s", symbol, kline.RetMsg)
			errors = append(errors, fmt.Sprintf("%s: Помилка API: %s", symbol, kline.RetMsg))
			continue
		}
		closes := make([]float64, 0, len(kline.Result.List))
		for _, item := range kline.Result.List {
			if len(item) < 5 {
				continue
			}
			closeVal, ok := item[4].(string)
			if !ok {
				continue
			}
			f, _ := parseFloat(closeVal)
			closes = append(closes, f)
		}
		if len(closes) > maxLen {
			maxLen = len(closes)
		}
		xValues := make([]float64, len(closes))
		for i := range closes {
			xValues[i] = float64(i + 1)
		}
		series = append(series, chart.ContinuousSeries{
			Name:    symbol,
			XValues: xValues,
			YValues: closes,
		})
		legend = append(legend, symbol)
	}
	if len(series) == 0 {
		return "Немає даних для заданих символів.\n" + strings.Join(errors, "\n")
	}
	graph := chart.Chart{
		Width:      600,
		Height:     300,
		Background: chart.Style{Padding: chart.Box{Top: 20, Left: 40, Right: 20, Bottom: 20}},
		Series:     series,
		YAxis:      chart.YAxis{},
		XAxis:      chart.XAxis{},
		Elements: []chart.Renderable{
			chart.Legend(&chart.Chart{
				Series: series,
			}),
		},
	}
	buf := bytes.NewBuffer([]byte{})
	if err := graph.Render(chart.PNG, buf); err != nil {
		return "Помилка рендеру графіка: " + err.Error()
	}
	photoFileBytes := tgbotapi.FileBytes{Name: "kline_compare.png", Bytes: buf.Bytes()}
	photoMsg := tgbotapi.NewPhoto(chatID, photoFileBytes)
	photoMsg.Caption = "Порівняння графіків: " + strings.Join(legend, ", ")
	_, err := bot.Send(photoMsg)
	if err != nil {
		return "Помилка надсилання фото: " + err.Error()
	}
	if len(errors) > 0 {
		return "Графік порівняння надіслано!\n" + strings.Join(errors, "\n")
	}
	return "Графік порівняння надіслано!"
}

func tgHandleVolumePhoto(bot *tgbotapi.BotAPI, chatID int64) string {
	tickers, err := fetchTickers()
	if err != nil {
		return "Error fetching volume: " + err.Error()
	}
	type pair struct {
		Symbol string
		Volume float64
	}
	var pairs []pair
	for _, t := range tickers.Result.List {
		v, err := parseFloat(t.Volume24h)
		if err != nil {
			continue
		}
		pairs = append(pairs, pair{t.Symbol, v})
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].Volume > pairs[j].Volume })
	if len(pairs) == 0 {
		return "Немає даних для об'єму."
	}
	max := 5
	if len(pairs) < 5 {
		max = len(pairs)
	}
	labels := make([]string, max)
	values := make([]float64, max)
	for i := 0; i < max; i++ {
		labels[i] = pairs[i].Symbol
		values[i] = pairs[i].Volume
	}
	bar := chart.BarChart{
		Width:      600,
		Height:     300,
		Background: chart.Style{Padding: chart.Box{Top: 20, Left: 40, Right: 20, Bottom: 20}},
		Bars:       []chart.Value{},
	}
	for i := 0; i < max; i++ {
		bar.Bars = append(bar.Bars, chart.Value{Value: values[i], Label: labels[i]})
	}
	buf := bytes.NewBuffer([]byte{})
	if err := bar.Render(chart.PNG, buf); err != nil {
		return "Помилка рендеру графіка: " + err.Error()
	}
	photoFileBytes := tgbotapi.FileBytes{Name: "volume_bar.png", Bytes: buf.Bytes()}
	photoMsg := tgbotapi.NewPhoto(chatID, photoFileBytes)
	photoMsg.Caption = "Топ-5 монет за об'ємом"
	_, err = bot.Send(photoMsg)
	if err != nil {
		return "Помилка надсилання фото: " + err.Error()
	}
	return "Графік об'єму надіслано!"
}

func tgHandleSalesPhoto(bot *tgbotapi.BotAPI, chatID int64) string {
	tickers, err := fetchTickers()
	if err != nil {
		return "Error fetching sales: " + err.Error()
	}
	type pair struct {
		Symbol string
		Sales  float64
	}
	var pairs []pair
	for _, t := range tickers.Result.List {
		v, err := parseFloat(t.Volume24h)
		if err != nil {
			continue
		}
		pairs = append(pairs, pair{t.Symbol, v})
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].Sales > pairs[j].Sales })
	if len(pairs) == 0 {
		return "Немає даних для продажу."
	}
	max := 5
	if len(pairs) < 5 {
		max = len(pairs)
	}
	labels := make([]string, max)
	values := make([]float64, max)
	for i := 0; i < max; i++ {
		labels[i] = pairs[i].Symbol
		values[i] = pairs[i].Sales
	}
	bar := chart.BarChart{
		Width:      600,
		Height:     300,
		Background: chart.Style{Padding: chart.Box{Top: 20, Left: 40, Right: 20, Bottom: 20}},
		Bars:       []chart.Value{},
	}
	for i := 0; i < max; i++ {
		bar.Bars = append(bar.Bars, chart.Value{Value: values[i], Label: labels[i]})
	}
	buf := bytes.NewBuffer([]byte{})
	if err := bar.Render(chart.PNG, buf); err != nil {
		return "Помилка рендеру графіка: " + err.Error()
	}
	photoFileBytes := tgbotapi.FileBytes{Name: "sales_bar.png", Bytes: buf.Bytes()}
	photoMsg := tgbotapi.NewPhoto(chatID, photoFileBytes)
	photoMsg.Caption = "Топ-5 монет за об'ємом продажу"
	_, err = bot.Send(photoMsg)
	if err != nil {
		return "Помилка надсилання фото: " + err.Error()
	}
	return "Графік продажу надіслано!"
}

func parseFloat(s string) (float64, error) {
	var f float64
	_, err := fmt.Sscanf(s, "%f", &f)
	return f, err
}

func tgHandlePrice(symbol string) string {
	tickers, err := fetchTickers()
	if err != nil {
		return "Помилка отримання ціни: " + err.Error()
	}
	for _, t := range tickers.Result.List {
		if t.Symbol == symbol {
			return fmt.Sprintf("%s ціна: %s", symbol, t.LastPrice)
		}
	}
	return "Символ не знайдено."
}

func tgHandleChange(symbol string) string {
	tickers, err := fetchTickers()
	if err != nil {
		return "Помилка отримання даних: " + err.Error()
	}
	for _, t := range tickers.Result.List {
		if t.Symbol == symbol {
			return fmt.Sprintf("%s зміна за 24г: %s%%", symbol, t.Price24hPcnt)
		}
	}
	return "Символ не знайдено."
}

func tgHandleVolume() string {
	tickers, err := fetchTickers()
	if err != nil {
		return "Помилка отримання об'єму: " + err.Error()
	}
	type pair struct {
		Symbol string
		Volume float64
	}
	var pairs []pair
	for _, t := range tickers.Result.List {
		v, _ := parseFloat(t.Volume24h)
		pairs = append(pairs, pair{t.Symbol, v})
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].Volume > pairs[j].Volume })
	res := "Топ-5 за об'ємом:\n"
	for i := 0; i < 5 && i < len(pairs); i++ {
		res += fmt.Sprintf("%s: %.0f\n", pairs[i].Symbol, pairs[i].Volume)
	}
	return res
}

func tgHandleGainers() string {
	tickers, err := fetchTickers()
	if err != nil {
		return "Помилка отримання лідерів: " + err.Error()
	}
	type pair struct {
		Symbol string
		Change float64
	}
	var pairs []pair
	for _, t := range tickers.Result.List {
		c, _ := parseFloat(t.Price24hPcnt)
		pairs = append(pairs, pair{t.Symbol, c})
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].Change > pairs[j].Change })
	res := "Топ-5 лідерів:\n"
	for i := 0; i < 5 && i < len(pairs); i++ {
		res += fmt.Sprintf("%s: %.2f%%\n", pairs[i].Symbol, pairs[i].Change)
	}
	return res
}

func tgHandleLosers() string {
	tickers, err := fetchTickers()
	if err != nil {
		return "Помилка отримання аутсайдерів: " + err.Error()
	}
	type pair struct {
		Symbol string
		Change float64
	}
	var pairs []pair
	for _, t := range tickers.Result.List {
		c, _ := parseFloat(t.Price24hPcnt)
		pairs = append(pairs, pair{t.Symbol, c})
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].Change < pairs[j].Change })
	res := "Топ-5 аутсайдерів:\n"
	for i := 0; i < 5 && i < len(pairs); i++ {
		res += fmt.Sprintf("%s: %.2f%%\n", pairs[i].Symbol, pairs[i].Change)
	}
	return res
}

func main() {
	log.SetOutput(os.Stdout)
	// Завантажуємо змінні середовища з .env (якщо файл існує)
	_ = godotenv.Load()
	token := os.Getenv("TELEGRAM_TOKEN")
	if token == "" {
		log.Panic("TELEGRAM_TOKEN не встановлено. Встановіть змінну середовища або додайте її в .env")
	}
	bot, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		log.Panic(err)
	}
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := bot.GetUpdatesChan(u)

	alerts := make(map[string]float64)
	go func() {
		for {
			time.Sleep(30 * time.Second)
			if len(alerts) == 0 {
				continue
			}
			tickers, err := fetchTickers()
			if err != nil {
				log.Printf("Alert fetch error: %v", err)
				continue
			}
			for symbol, target := range alerts {
				for _, t := range tickers.Result.List {
					if t.Symbol == symbol {
						price, _ := parseFloat(t.LastPrice)
						if price >= target {
							log.Printf("ALERT: %s price %.2f >= %.2f", symbol, price, target)
							delete(alerts, symbol)
						}
					}
				}
			}
		}
	}()

	for update := range updates {
		if update.Message == nil {
			continue
		}
		text := update.Message.Text
		chatID := update.Message.Chat.ID

		if text == "/start" {
			keyboard := tgbotapi.NewReplyKeyboard(
				tgbotapi.NewKeyboardButtonRow(
					tgbotapi.NewKeyboardButton("/price BTCUSDT"),
					tgbotapi.NewKeyboardButton("/change BTCUSDT"),
					tgbotapi.NewKeyboardButton("/kline BTCUSDT"),
				),
				tgbotapi.NewKeyboardButtonRow(
					tgbotapi.NewKeyboardButton("/volume"),
					tgbotapi.NewKeyboardButton("/gainers"),
					tgbotapi.NewKeyboardButton("/losers"),
				),
				tgbotapi.NewKeyboardButtonRow(
					tgbotapi.NewKeyboardButton("/klinephoto BTCUSDT,ETHUSDT"),
					tgbotapi.NewKeyboardButton("/salesphoto"),
				),
			)
			//msg := tgbotapi.NewMessage(chatID, "Вітаю! Виберіть команду або введіть свою:")
			//
			commandsDescription := `👋 Вітаю!
			Оберіть команду нижче або введіть свою:

			💰 Ціни та зміни
			/price — поточна ціна *BTC/USDT*
			/change — зміна за 24 год. *ETH/USDT*

			📊 Ринок
			/volume — топ-5 пар за обсягом
			/gainers — топ-5 лідерів зростання
			/losers — топ-5 лідерів падіння

			📈 Графіки та свічки
			/kline — останні 5 свічок *BTC/USDT*
			/klinephoto — графік для кількох пар
			/volumephoto — графік топ-5 за обсягом
			/salesphoto — графік топ-5 за продажами
			`
			msg := tgbotapi.NewMessage(chatID, commandsDescription)
			msg.ReplyMarkup = keyboard
			bot.Send(msg)
			continue
		}

		if strings.HasPrefix(text, "/price") {
			symbol := strings.TrimSpace(strings.TrimPrefix(text, "/price"))
			msg := tgbotapi.NewMessage(chatID, tgHandlePrice(symbol))
			bot.Send(msg)
			continue
		}
		if strings.HasPrefix(text, "/change") {
			symbol := strings.TrimSpace(strings.TrimPrefix(text, "/change"))
			msg := tgbotapi.NewMessage(chatID, tgHandleChange(symbol))
			bot.Send(msg)
			continue
		}
		if text == "/volume" {
			msg := tgbotapi.NewMessage(chatID, tgHandleVolume())
			bot.Send(msg)
			continue
		}
		if text == "/gainers" {
			msg := tgbotapi.NewMessage(chatID, tgHandleGainers())
			bot.Send(msg)
			continue
		}
		if text == "/losers" {
			msg := tgbotapi.NewMessage(chatID, tgHandleLosers())
			bot.Send(msg)
			continue
		}
		if strings.HasPrefix(text, "/kline") {
			symbol := strings.TrimSpace(strings.TrimPrefix(text, "/kline"))
			msg := tgbotapi.NewMessage(chatID, tgHandleKline(symbol))
			bot.Send(msg)
			continue
		}
		if strings.HasPrefix(text, "/klinephoto") {
			symbols := strings.TrimSpace(strings.TrimPrefix(text, "/klinephoto"))
			msg := tgHandleKlinePhoto(symbols, bot, chatID)
			bot.Send(tgbotapi.NewMessage(chatID, msg))
			continue
		}
		if text == "/volumephoto" {
			msg := tgHandleVolumePhoto(bot, chatID)
			bot.Send(tgbotapi.NewMessage(chatID, msg))
			continue
		}
		if text == "/salesphoto" {
			msg := tgHandleSalesPhoto(bot, chatID)
			bot.Send(tgbotapi.NewMessage(chatID, msg))
			continue
		}
		msg := tgbotapi.NewMessage(chatID, "Невідома команда.")
		bot.Send(msg)
	}
}
