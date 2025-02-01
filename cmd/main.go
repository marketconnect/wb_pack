package main

import (
	"encoding/json"
	"fmt"
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
		log.Println("✅ Новых заказов нет.")
	}
}

// Основной цикл с проверкой раз в минуту
func main() {
	for {
		checkWildberriesOrders()
		time.Sleep(1 * time.Minute)
	}
}
