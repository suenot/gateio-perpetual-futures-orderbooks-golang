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
	Result  json.RawMessage `json:"result"` // Changed to RawMessage for flexible parsing
}

// Структура для обновления ордербука
type OrderBookUpdate struct {
	Contract string          `json:"s"` // Contract name in update messages
	Asks     []OrderBookItem `json:"a"` // Ask orders
	Bids     []OrderBookItem `json:"b"` // Bid orders
}

// Структура для подтверждения подписки
type SubscriptionResponse struct {
	Status string `json:"status"`
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
		size := ask.S
		sb.WriteString(fmt.Sprintf("ASK %.8f | %.8f\n", price, size))
	}

	// Разделительная линия
	sb.WriteString("------------------------\n")

	// Форматируем bids
	for _, bid := range orderbook.Bids {
		price, _ := strconv.ParseFloat(bid.P, 64)
		size := bid.S
		sb.WriteString(fmt.Sprintf("BID %.8f | %.8f\n", price, size))
	}

	return sb.String()
}

// Сохранение ордербука в файл
func saveOrderBook(symbol string, orderbook OrderBookResponse) error {
	// Проверяем, что символ не пустой
	if symbol == "" {
		return fmt.Errorf("empty symbol provided")
	}

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

// Обновление списка ордеров
func updateOrders(existing []OrderBookItem, updates []OrderBookItem) []OrderBookItem {
	// Создаем карту существующих ордеров для быстрого доступа
	ordersMap := make(map[string]float64)
	for _, order := range existing {
		ordersMap[order.P] = order.S
	}

	// Обновляем или удаляем ордера
	for _, update := range updates {
		if update.S == 0 {
			// Если размер 0, удаляем ордер
			delete(ordersMap, update.P)
		} else {
			// Иначе обновляем или добавляем
			ordersMap[update.P] = update.S
		}
	}

	// Преобразуем обратно в слайс
	var result []OrderBookItem
	for price, size := range ordersMap {
		result = append(result, OrderBookItem{P: price, S: size})
	}

	return result
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
		if wsMsg.Event == "subscribe" {
			// Обрабатываем подтверждение подписки
			var subResp SubscriptionResponse
			err = json.Unmarshal(wsMsg.Result, &subResp)
			if err != nil {
				log.Printf("Subscription response parse error: %v", err)
			} else {
				log.Printf("Subscription status: %s", subResp.Status)
			}
			return
		}

		if wsMsg.Event == "update" {
			// Обрабатываем обновление ордербука
			var update OrderBookUpdate
			err = json.Unmarshal(wsMsg.Result, &update)
			if err != nil {
				log.Printf("Update message parse error: %v", err)
				return
			}

			contract := update.Contract
			if contract == "" {
				log.Printf("Warning: Empty contract in update message: %s", string(msg))
				return
			}

			// Получаем существующий ордербук
			existing, ok := orderbooks[contract]
			if !ok {
				log.Printf("Warning: No existing orderbook for contract %s", contract)
				return
			}

			// Обновляем asks и bids
			if len(update.Asks) > 0 || len(update.Bids) > 0 {
				// Обновляем существующие ордера
				existing.Asks = updateOrders(existing.Asks, update.Asks)
				existing.Bids = updateOrders(existing.Bids, update.Bids)
				existing.Update = float64(wsMsg.Time) / 1000
				orderbooks[contract] = existing

				log.Printf("Updated orderbook for contract: %s (asks updates: %d, bids updates: %d)",
					contract, len(update.Asks), len(update.Bids))
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

	// Подписываемся на обновления ордербука для каждого контракта отдельно
	for _, contract := range contracts {
		subscribeMsg := map[string]interface{}{
			"time":    time.Now().Unix(),
			"channel": "futures.order_book_update",
			"event":   "subscribe",
			"payload": []string{contract, "100ms"}, // Добавляем интервал обновления как второй аргумент
		}

		err = c.WriteJSON(subscribeMsg)
		if err != nil {
			log.Printf("WebSocket subscription error for %s: %v", contract, err)
			continue
		}
		log.Printf("Subscribed to %s orderbook updates", contract)
	}

	log.Println("WebSocket connected and subscribed to all contracts")

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
	ticker := time.NewTicker(50 * time.Millisecond)
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
	go startOrderBookSaver()

	// Подключаемся к WebSocket
	connectWebSocket(contracts)
}
