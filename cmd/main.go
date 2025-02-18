package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// URL API Wildberries
const wildberriesAPI = "https://marketplace-api.wildberries.ru/api/v3/orders/new"

// Структура ответа Wildberries API
type WildberriesResponse struct {
	Orders []Order `json:"orders"`
}

type Order struct {
	ID        int    `json:"id"`
	Article   string `json:"article"`
	SalePrice int    `json:"salePrice"`
	DDate     string `json:"ddate"`
}

// Функция для отправки сообщения в Telegram
func sendTelegramMessage(message string) {
	botToken := os.Getenv("TELEGRAM_BOT_TOKEN")
	chatID := os.Getenv("TELEGRAM_CHAT_ID")

	if botToken == "" || chatID == "" {
		log.Println("Ошибка: TELEGRAM_BOT_TOKEN или TELEGRAM_CHAT_ID не установлены.")
		return
	}

	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", botToken)
	data := url.Values{}
	data.Set("chat_id", chatID)
	data.Set("text", message)

	resp, err := http.Post(apiURL, "application/x-www-form-urlencoded", strings.NewReader(data.Encode()))
	if err != nil {
		log.Printf("Ошибка при отправке сообщения в Telegram: %v", err)
		return
	}
	defer resp.Body.Close()

	body, _ := ioutil.ReadAll(resp.Body)
	log.Printf("Ответ Telegram: %s", body)
}

// Функция для запроса заказов из Wildberries
func checkWildberriesOrders() {
	apiKey := os.Getenv("WB_TOKEN")
	if apiKey == "" {
		log.Println("Ошибка: WB_TOKEN не установлен.")
		return
	}

	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest("GET", wildberriesAPI, nil)
	if err != nil {
		log.Printf("Ошибка при создании запроса: %v", err)
		return
	}

	req.Header.Set("Authorization", apiKey)
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Ошибка при выполнении запроса: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		log.Printf("Ошибка: API вернул статус %d", resp.StatusCode)
		return
	}

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Printf("Ошибка при чтении ответа: %v", err)
		return
	}

	var response WildberriesResponse
	if err := json.Unmarshal(body, &response); err != nil {
		log.Printf("Ошибка при разборе JSON: %v", err)
		return
	}

	if len(response.Orders) > 0 {
		message := "📦 Новые заказы на Wildberries:\n"
		for _, order := range response.Orders {
			message += fmt.Sprintf("🔹 ID: %d, Артикул: %s, Цена: %d₽, Дата доставки: %s\n",
				order.ID, order.Article, order.SalePrice, order.DDate)
		}
		sendTelegramMessage(message)
	} else {
		log.Println("✅ Новых заказов на WB нет.")
	}
}

// Основной цикл с проверкой раз в минуту
func main() {
	for {
		checkWildberriesOrders()
		checkOzonOrders()
		time.Sleep(1 * time.Minute)
	}
}

// OZON

// requestBody структура для запроса
type requestBody struct {
	Dir    string            `json:"dir"`
	Filter map[string]string `json:"filter"`
	Limit  int               `json:"limit"`
	Offset int               `json:"offset"`
	With   map[string]bool   `json:"with"`
}

// product структура для товара
type product struct {
	Price        string `json:"price"`
	CurrencyCode string `json:"currency_code"`
	OfferID      string `json:"offer_id"`
	Name         string `json:"name"`
	SKU          int    `json:"sku"`
	Quantity     int    `json:"quantity"`
}

// posting структура для каждого заказа
type posting struct {
	PostingNumber string    `json:"posting_number"`
	OrderID       int       `json:"order_id"`
	Status        string    `json:"status"`
	Products      []product `json:"products"`
}

// response структура для ответа
type response struct {
	Result struct {
		Postings []posting `json:"postings"`
	} `json:"result"`
}

func checkOzonOrders() {
	apiKey := os.Getenv("OZON_API_KEY")
	clientID := os.Getenv("OZON_CLIENT_ID")

	if apiKey == "" || clientID == "" {
		fmt.Println("Переменные окружения OZON_API_KEY и OZON_CLIENT_ID должны быть установлены")
		return
	}

	// Определяем временные интервалы: с вчерашнего дня 23:00 до текущего момента
	now := time.Now()
	yesterday := now.Add(-24 * time.Hour).Truncate(24 * time.Hour).Add(23 * time.Hour)

	reqBody := requestBody{
		Dir: "ASC",
		Filter: map[string]string{
			"since":  yesterday.Format("2006-01-02T15:04:05Z"), // Вчера 23:00
			"to":     now.Format("2006-01-02T15:04:05Z"),       // Сейчас
			"status": "awaiting_approve",
		},
		Limit:  100,
		Offset: 0,
		With: map[string]bool{
			"analytics_data": true,
			"financial_data": true,
		},
	}

	// Сериализуем тело запроса в JSON
	payload, err := json.Marshal(reqBody)
	if err != nil {
		fmt.Printf("Ошибка при маршалинге данных: %v\n", err)
		return
	}

	// Создаем HTTP-клиент
	client := &http.Client{Timeout: 10 * time.Second}

	// Создаем HTTP-запрос
	req, err := http.NewRequest("POST", "https://api-seller.ozon.ru/v3/posting/fbs/list", bytes.NewBuffer(payload))
	if err != nil {
		fmt.Printf("Ошибка при создании запроса: %v\n", err)
		return
	}

	// Устанавливаем заголовки
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Client-Id", clientID)
	req.Header.Set("Api-Key", apiKey)

	// Выполняем запрос
	resp, err := client.Do(req)
	if err != nil {
		fmt.Printf("Ошибка при отправке запроса: %v\n", err)
		return
	}
	defer resp.Body.Close()

	// Читаем ответ
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Printf("Ошибка при чтении ответа: %v\n", err)
		return
	}

	// Проверяем статус-код
	if resp.StatusCode != http.StatusOK {
		fmt.Printf("Ошибка: статус %d, ответ: %s\n", resp.StatusCode, string(body))
		return
	}

	// Разбираем ответ
	var res response
	if err := json.Unmarshal(body, &res); err != nil {
		fmt.Printf("Ошибка при разборе ответа: %v\n", err)
		return
	}
	if len(res.Result.Postings) == 0 {
		log.Println("✅ Новых заказов на OZON нет.")
		return
	}
	// Выводим информацию о заказах и их товарах
	for _, posting := range res.Result.Postings {
		message := "📦 Новые заказы на OZON:\n"

		for _, product := range posting.Products {
			message += fmt.Sprintf("  - Товар: %s (SKU: %d, Количество: %d, Цена: %s %s, OfferID: %s)\n",
				product.Name, product.SKU, product.Quantity, product.Price, product.CurrencyCode, product.OfferID)
		}
		sendTelegramMessage(message)
	}
}
