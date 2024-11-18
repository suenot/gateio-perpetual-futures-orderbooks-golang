package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// Структуры для REST API
type OrderBookItem struct {
	P string  `json:"p"` // Price
	S float64 `json:"s"` // Size as float64
}

type OrderBookResponse struct {
	ID      int64           `json:"id"`      // Orderbook ID
	Current float64         `json:"current"` // Data generation time
	Update  float64         `json:"update"`  // Last update time
	Asks    []OrderBookItem `json:"asks"`    // Ask orders
	Bids    []OrderBookItem `json:"bids"`    // Bid orders
}

// Структура для WebSocket сообщений
type WebSocketMessage struct {
	Time    int64           `json:"time"`
	Channel string          `json:"channel"`
	Event   string          `json:"event"`
	Result  OrderBookUpdate `json:"result"`
}

type OrderBookUpdate struct {
	Contract string          `json:"contract"`
	Asks     []OrderBookItem `json:"asks"`
	Bids     []OrderBookItem `json:"bids"`
}

// Глобальные переменные для хранения ордербуков
var orderbooks = make(map[string]OrderBookResponse)

// Получение REST снимка ордербука
func getOrderBookSnapshot(settle, contract string, limit int) (OrderBookResponse, error) {
	host := "https://api.gateio.ws"
	prefix := "/api/v4"
	endpoint := fmt.Sprintf("%s%s/futures/%s/order_book?contract=%s&limit=%d", host, prefix, settle, contract, limit)

	resp, err := http.Get(endpoint)
	if err != nil {
		return OrderBookResponse{}, fmt.Errorf("HTTP request error: %v", err)
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return OrderBookResponse{}, fmt.Errorf("Response read error: %v", err)
	}

	if resp.StatusCode != 200 {
		return OrderBookResponse{}, fmt.Errorf("API error, status: %d, response: %s", resp.StatusCode, string(body))
	}

	var orderbook OrderBookResponse
	err = json.Unmarshal(body, &orderbook)
	if err != nil {
		return OrderBookResponse{}, fmt.Errorf("JSON parse error: %v", err)
	}

	return orderbook, nil
}

// Форматирование ордербука в текстовый формат
func formatOrderBook(orderbook OrderBookResponse) string {
	var sb strings.Builder

	// Форматируем asks (в обратном порядке)
	for i := len(orderbook.Asks) - 1; i >= 0; i-- {
		ask := orderbook.Asks[i]
		price, _ := strconv.ParseFloat(ask.P, 64)
		size := ask.S / 100000000.0 // Convert from Gate.io's internal representation
		sb.WriteString(fmt.Sprintf("ASK %9.2f | %9.4f\n", price, size))
	}

	// Разделительная линия
	sb.WriteString("------------------------\n")

	// Форматируем bids
	for _, bid := range orderbook.Bids {
		price, _ := strconv.ParseFloat(bid.P, 64)
		size := bid.S / 100000000.0 // Convert from Gate.io's internal representation
		sb.WriteString(fmt.Sprintf("BID %9.2f | %9.4f\n", price, size))
	}

	return sb.String()
}

// Сохранение ордербука в файл
func saveOrderBook(symbol string, orderbook OrderBookResponse) error {
	// Создаем директорию если её нет
	orderbookDir := "./orderbooks"
	err := os.MkdirAll(orderbookDir, 0755)
	if err != nil {
		return fmt.Errorf("failed to create orderbooks directory: %v", err)
	}

	// Форматируем ордербук в текстовый вид
	formattedOrderbook := formatOrderBook(orderbook)

	filename := filepath.Join(orderbookDir, fmt.Sprintf("%s.txt", symbol))
	err = ioutil.WriteFile(filename, []byte(formattedOrderbook), 0644)
	if err != nil {
		return fmt.Errorf("failed to write file %s: %v", filename, err)
	}

	log.Printf("Orderbook saved to %s", filename)
	return nil
}

// Обработка WebSocket сообщений
func handleWebSocketMessage(msg []byte) {
	var wsMsg WebSocketMessage
	err := json.Unmarshal(msg, &wsMsg)
	if err != nil {
		log.Printf("WebSocket message parse error: %v", err)
		return
	}

	// Проверяем, что это сообщение с обновлением ордербука
	if wsMsg.Channel == "futures.order_book_update" {
		// Обновляем существующий ордербук
		if existing, ok := orderbooks[wsMsg.Result.Contract]; ok {
			// Обновляем asks и bids
			existing.Asks = wsMsg.Result.Asks
			existing.Bids = wsMsg.Result.Bids
			existing.Update = float64(wsMsg.Time) / 1000
			orderbooks[wsMsg.Result.Contract] = existing

			// Сохраняем обновленный ордербук
			err = saveOrderBook(wsMsg.Result.Contract, existing)
			if err != nil {
				log.Printf("Failed to save updated orderbook for %s: %v", wsMsg.Result.Contract, err)
			}
		}
	}
}

// Подключение к WebSocket
func connectWebSocket(contracts []string) {
	url := "wss://fx-ws.gateio.ws/v4/ws/usdt"

	c, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		log.Fatal("WebSocket connection error:", err)
	}
	defer c.Close()

	// Подписываемся на обновления ордербука
	subscribeMsg := map[string]interface{}{
		"channel": "futures.order_book_update",
		"event":   "subscribe",
		"payload": contracts,
	}

	err = c.WriteJSON(subscribeMsg)
	if err != nil {
		log.Fatal("WebSocket subscription error:", err)
	}

	log.Println("WebSocket connected and subscribed")

	// Обработка входящих сообщений
	for {
		_, message, err := c.ReadMessage()
		if err != nil {
			log.Println("WebSocket read error:", err)
			return
		}
		handleWebSocketMessage(message)
	}
}

// Запуск периодического сохранения ордербуков
func startOrderBookSaver() {
	ticker := time.NewTicker(1 * time.Second)
	go func() {
		for range ticker.C {
			for symbol, orderbook := range orderbooks {
				err := saveOrderBook(symbol, orderbook)
				if err != nil {
					log.Printf("Error saving orderbook for %s: %v", symbol, err)
				}
			}
		}
	}()
}

func main() {
	fmt.Println("Gate.io Perpetual Futures Orderbook Tracker")
	fmt.Println("Version: 1.0.0")
	fmt.Println("---")

	// Создаем директорию для ордербуков если её нет
	err := os.MkdirAll("./orderbooks", 0755)
	if err != nil {
		log.Fatal("Failed to create orderbooks directory:", err)
	}

	// Список контрактов для отслеживания
	contracts := []string{"BTC_USDT", "ETH_USDT", "LTC_USDT"}

	// Получаем начальные снимки ордербуков
	for _, contract := range contracts {
		orderbook, err := getOrderBookSnapshot("usdt", contract, 50)
		if err != nil {
			log.Printf("Failed to get initial orderbook for %s: %v", contract, err)
			continue
		}

		orderbooks[contract] = orderbook
		log.Printf("Initial orderbook snapshot received for %s", contract)

		// Сохраняем начальный снимок
		err = saveOrderBook(contract, orderbook)
		if err != nil {
			log.Printf("Failed to save initial orderbook for %s: %v", contract, err)
		}
	}

	// Запускаем периодическое сохранение
	startOrderBookSaver()

	// Подключаемся к WebSocket
	connectWebSocket(contracts)
}
