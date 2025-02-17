package main

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
	_ "modernc.org/sqlite"
)

const (
	WBAPINUrl    = "https://marketplace-api.wildberries.ru/api/v3/stocks/%d"
	WarehouseID  = 1283008
	BatchSize    = 1000
	RequestLimit = 300 // 300 запросов в минуту

)

var bubblebagsURLMap = make(map[string]string)

func main() {
	apiKey := os.Getenv("WB_API_KEY")
	if apiKey == "" {
		log.Fatal("Перед запуском необходимо установить переменную окружения API_KEY")
	}
	if err := loadBubblebagsCSV(); err != nil {
		log.Fatalf("Ошибка загрузки URL из CSV: %v", err)
	}

	cfg := Config{
		ObjectIDs: []int{3979, 3756},

		DBName: "weights.db",
		VendorCodePatterns: []string{
			"^box_\\d+_\\d+$",
			"^bubblebags_9\\d+_\\d+$",
			"^bubblebags_1\\d+_\\d+$",
		},
		UsePcs: true,
	}

	err := Process(apiKey, cfg)
	if err != nil {
		log.Fatalf("Ошибка при обработке: %v", err)
	}

}

func loadBubblebagsCSV() error {
	file, err := os.Open("../cargo_avto/urls.csv")
	if err != nil {
		return fmt.Errorf("ошибка при открытии файла urls.csv: %v", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.Split(line, ",")
		if len(parts) == 2 {
			// Пример: "bubblebags_19323,https://packio.ru/product/paket..."
			bubblebagsURLMap[parts[0]] = parts[1]
		}
	}
	return scanner.Err()
}

type Config struct {
	ObjectIDs []int // SubjectIDs

	DBName             string   // DBName (for example, "ue.db")
	VendorCodePatterns []string // VendorCodePattern (for example, "^box_\d+_\d+$")
	UsePcs             bool     // UsePcs (for example, true)
}

const baseURL = "https://sp.cargo-avto.ru/catalog/"

func Process(apiKey string, cfg Config) error {

	if err := os.Remove(cfg.DBName); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("ошибка удаления старой базы данных: %v", err)
	}
	log.Println("Старая база данных удалена.")

	db, err := sql.Open("sqlite", cfg.DBName)
	if err != nil {
		return fmt.Errorf("ошибка при открытии базы данных: %v", err)
	}
	defer db.Close()

	createTable(db)

	// 3. Загружаем карточки, используя переданные objectIDs
	allCards := fetchAllCards(apiKey, cfg.ObjectIDs)
	log.Printf("Всего загружено %d карточек.", len(allCards))

	// 4. Настраиваем Chromedp для парсинга страниц
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", false),
		chromedp.Flag("disable-gpu", true),
	)
	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(), opts...)
	defer allocCancel()

	ctx, ctxCancel := chromedp.NewContext(allocCtx)
	defer ctxCancel()

	productDataCache := make(map[string]map[string]string)
	skuMap := extractSKUs(allCards)
	// vendorCodePattern := regexp.MustCompile(cfg.VendorCodePattern)
	// 7. Обрабатываем каждую карточку
	for _, card := range allCards {
		var matched bool
		for _, pattern := range cfg.VendorCodePatterns {
			if regexp.MustCompile(pattern).MatchString(card.VendorCode) {
				matched = true
				break
			}
		}
		if !matched {
			log.Printf("Пропускаем товар с некорректным VendorCode: %s", card.VendorCode)
			continue
		}

		skus := skuMap[card.NmID]
		if len(skus) != 1 {
			panic(fmt.Sprintf("SKU либо отсутствует, либо их больше 1 для товара с VendorCode: %s", card.VendorCode))
		}

		// Извлекаем productID и pcs из vendorCode
		parts := strings.Split(card.VendorCode, "_")
		if len(parts) < 2 {
			log.Printf("Некорректный VendorCode: %s", card.VendorCode)
			continue
		}
		productID := parts[1]

		// Парсинг данных товара (с кешированием)
		var productData map[string]string
		if cachedData, exists := productDataCache[productID]; exists {
			log.Printf("Используем кешированные данные для товара: %s", productID)
			productData = cachedData
		} else {
			log.Printf("Парсим страницу для товара: %s", productID)
			// url := baseURL + productID + "/"
			// productData, err = scrapeProductData(ctx, url)
			productData, err = scrapeProductData(ctx, card.VendorCode)
			if err != nil {
				log.Printf("Ошибка при обработке товара %s: %v", productID, err)
				continue
			}
			productDataCache[productID] = productData
		}

		// Рассчитываем стоимость с учетом количества pcs

		saveToDatabase(db, SaveParams{

			VendorCode: card.VendorCode,
			Weight:     productData["weight"],
		}, skus[0])
	}

	log.Println("Обработка завершена.")
	return nil
}

func createTable(db *sql.DB) {
	query := `
	CREATE TABLE IF NOT EXISTS weights (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		vendor_code TEXT,
		weight TEXT,
		UNIQUE (vendor_code)
	);
	`
	_, err := db.Exec(query)
	if err != nil {
		log.Fatalf("Ошибка при создании таблицы: %v", err)
	}
	log.Println("Таблица weights проверена/создана.")
}

func fetchAllCards(apiKey string, objectIDs []int) []Card {
	var allCards []Card
	var updatedAt string
	var nmID int

	for {
		response, err := getCardsList(apiKey, updatedAt, nmID, objectIDs)
		if err != nil {
			log.Printf("Ошибка запроса карточек: %v", err)
			break
		}
		if response == nil || len(response.Cards) == 0 {
			log.Println("Больше нет карточек для загрузки.")
			break
		}
		allCards = append(allCards, response.Cards...)
		updatedAt = response.Cursor.UpdatedAt
		nmID = response.Cursor.NmID

		if updatedAt == "" || nmID == 0 {
			break
		}
		log.Printf("Загружено %d карточек, продолжаем...", len(allCards))
	}
	return allCards
}

type Card struct {
	NmID       int           `json:"nmID"`
	VendorCode string        `json:"vendorCode"`
	UpdatedAt  string        `json:"updatedAt"`
	Sizes      []ProductSize `json:"sizes"`
}

type ProductSize struct {
	SKUs []string `json:"skus"`
}

type CardsListResponse struct {
	Cards  []Card `json:"cards"`
	Cursor struct {
		UpdatedAt string `json:"updatedAt"`
		NmID      int    `json:"nmID"`
		Total     int    `json:"total"`
	} `json:"cursor"`
}

func extractSKUs(cards []Card) map[int][]string {
	skuMap := make(map[int][]string)
	for _, card := range cards {
		var skus []string
		for _, size := range card.Sizes {
			skus = append(skus, size.SKUs...)
		}
		skuMap[card.NmID] = skus
	}
	return skuMap
}

func scrapeProductData(ctx context.Context, vendorCode string) (map[string]string, error) {

	matched, _ := regexp.MatchString(`^bubblebags_1\d+_\d+$`, vendorCode)
	if matched {

		baseKey := vendorCode
		if idx := strings.LastIndex(baseKey, "_"); idx != -1 {

			baseKey = baseKey[:idx]
		}

		csvURL, ok := bubblebagsURLMap[baseKey]
		if !ok {
			log.Printf("Не найден URL для %s в urls.csv", vendorCode)
			return map[string]string{"price": "0", "availableCount": "0"}, nil
		}

		var weightGrams float64

		var weight string

		err := chromedp.Run(ctx,
			chromedp.Navigate(csvURL),
			chromedp.Sleep(3*time.Second),
			chromedp.Text(`//tr[td[text()="Вес, кг"]]/td[2]`, &weight, chromedp.BySearch),
		)

		fmt.Println("weight", weight)

		if err != nil {
			log.Fatalf("Failed to get weight: %v", err)
		}

		weightValue, err := strconv.ParseFloat(weight, 64)
		if err != nil {
			log.Fatalf("Failed to parse weight: %v", err)
		}

		weightGrams = weightValue * 1000
		fmt.Printf("Вес в граммах: %.0f г\n", weightGrams)

		if err != nil {
			return nil, fmt.Errorf("ошибка при парсинге страницы %s: %v", csvURL, err)
		}

		return map[string]string{"weight": fmt.Sprintf("%.0f", weightGrams)}, nil
	}

	// Остальной код для "box_\d+_\d+$" и т. д.
	// (пример парсинга sp.cargo-avto.ru)
	parts := strings.Split(vendorCode, "_")
	if len(parts) < 2 {
		return nil, fmt.Errorf("некорректный VendorCode: %s", vendorCode)
	}
	url := baseURL + parts[1] + "/"

	var weight string
	err := chromedp.Run(ctx,
		chromedp.Navigate(url),
		chromedp.Sleep(3*time.Second),
		chromedp.Evaluate(`window.weight = Array.from(document.querySelectorAll('.characteristics-list__item')).find(el => el.textContent.includes('Вес, г'))?.querySelector('.valls')?.textContent || '';`, nil),
		chromedp.Evaluate(`window.weight`, &weight),
	)

	fmt.Println("weight", weight)

	if err != nil {
		log.Fatalf("Ошибка парсинга веса: %v", err)
	}

	fmt.Println(weight)

	return map[string]string{"weight": weight}, nil
}

func getCardsList(apiKey string, updatedAt string, nmID int, objectIDs []int) (*CardsListResponse, error) {
	url := "https://content-api.wildberries.ru/content/v2/get/cards/list"
	client := &http.Client{Timeout: 10 * time.Second}

	bodyData := map[string]interface{}{
		"settings": map[string]interface{}{
			"cursor": map[string]interface{}{
				"limit": 100,
			},
			"filter": map[string]interface{}{
				"withPhoto": 1,
				"objectIDs": objectIDs,
			},
		},
	}

	if updatedAt != "" {
		bodyData["settings"].(map[string]interface{})["cursor"].(map[string]interface{})["updatedAt"] = updatedAt
	}
	if nmID != 0 {
		bodyData["settings"].(map[string]interface{})["cursor"].(map[string]interface{})["nmID"] = nmID
	}

	bodyJSON, err := json.Marshal(bodyData)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", url, bytes.NewReader(bodyJSON))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	b, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var response CardsListResponse
	if err := json.Unmarshal(b, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

type SaveParams struct {
	VendorCode string
	Weight     string
}

func saveToDatabase(db *sql.DB, params SaveParams, sku string) {

	fmt.Printf("vendorCode: %s, weight: %s\n", params.VendorCode, params.Weight)

	query := `
			INSERT INTO weights (
			 vendor_code, weight)
			VALUES (?, ?)
			ON CONFLICT(vendor_code) DO UPDATE SET
			weight = excluded.weight;
		`

	_, err := db.Exec(query,
		params.VendorCode, params.Weight,
	)
	if err != nil {
		log.Printf("Ошибка при сохранении данных для %s: %v", params.VendorCode, err)
	} else {
		log.Printf("Данные для товара %s успешно сохранены. SKUs: %s", params.VendorCode, sku)
	}
}
