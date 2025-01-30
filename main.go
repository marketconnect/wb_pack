package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
	_ "modernc.org/sqlite"
)

const baseURL = "https://sp.cargo-avto.ru/catalog/"

type TariffResponse struct {
	Response struct {
		Data struct {
			WarehouseList []struct {
				WarehouseName    string          `json:"warehouseName"`
				BoxDeliveryBase  json.RawMessage `json:"boxDeliveryBase"`
				BoxDeliveryLiter json.RawMessage `json:"boxDeliveryLiter"`
			} `json:"warehouseList"`
		} `json:"data"`
	} `json:"response"`
}

// parseFloat – обрабатываем и строки (с запятой), и числа
func parseFloat(raw json.RawMessage) (float64, error) {
	var num float64
	if err := json.Unmarshal(raw, &num); err == nil {
		// Если получилось распарсить как float
		return num, nil
	}
	var str string
	if err := json.Unmarshal(raw, &str); err == nil {
		// Заменяем запятые на точки
		str = strings.ReplaceAll(str, ",", ".")
		return strconv.ParseFloat(str, 64)
	}
	return 0, fmt.Errorf("не удалось преобразовать значение в float64")
}

// getFBSTariffs – получаем тарифы, учитывая возможные запятые
func getFBSTariffs(apiKey string) (float64, float64, error) {
	url := "https://common-api.wildberries.ru/api/v1/tariffs/box"
	client := &http.Client{Timeout: 10 * time.Second}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return 0, 0, err
	}
	req.Header.Set("Authorization", apiKey)

	// Пример: date=2025-02-01
	q := req.URL.Query()
	q.Add("date", "2025-02-01")
	req.URL.RawQuery = q.Encode()

	resp, err := client.Do(req)
	if err != nil {
		return 0, 0, err
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return 0, 0, err
	}

	var data TariffResponse
	if err := json.Unmarshal(body, &data); err != nil {
		return 0, 0, err
	}

	for _, warehouse := range data.Response.Data.WarehouseList {
		if warehouse.WarehouseName == "Маркетплейс" {
			base, err1 := parseFloat(warehouse.BoxDeliveryBase)
			liter, err2 := parseFloat(warehouse.BoxDeliveryLiter)
			if err1 != nil || err2 != nil {
				return 0, 0, fmt.Errorf("ошибка конвертации тарифов: %v, %v", err1, err2)
			}
			return base, liter, nil
		}
	}

	return 0, 0, fmt.Errorf("не найден склад 'Маркетплейс'")
}

type CardsListResponse struct {
	Cards  []Card `json:"cards"`
	Cursor struct {
		UpdatedAt string `json:"updatedAt"`
		NmID      int    `json:"nmID"`
		Total     int    `json:"total"`
	} `json:"cursor"`
}

type Card struct {
	NmID       int        `json:"nmID"`
	VendorCode string     `json:"vendorCode"`
	Title      string     `json:"title"`
	UpdatedAt  string     `json:"updatedAt"`
	Dimensions Dimensions `json:"dimensions"`
}

type Dimensions struct {
	Width   int  `json:"width"`
	Height  int  `json:"height"`
	Length  int  `json:"length"`
	IsValid bool `json:"isValid"`
}

type Size struct {
	SizeID              int64   `json:"sizeID"`
	Price               float64 `json:"price"`
	DiscountedPrice     float64 `json:"discountedPrice"`
	ClubDiscountedPrice float64 `json:"clubDiscountedPrice"`
	TechSizeName        string  `json:"techSizeName"`
}

type Product struct {
	NmID              int64  `json:"nmID"`
	VendorCode        string `json:"vendorCode"`
	Sizes             []Size `json:"sizes"`
	CurrencyIsoCode   string `json:"currencyIsoCode4217"`
	Discount          int    `json:"discount"`
	ClubDiscount      int    `json:"clubDiscount"`
	EditableSizePrice bool   `json:"editableSizePrice"`
}

type Data struct {
	ListGoods []Product `json:"listGoods"`
}

type ProductResponse struct {
	Data Data `json:"data"`
}

func getProductPrices(apiKey string, limit, offset int, filterNmID int64) ([]Product, error) {
	url := fmt.Sprintf("https://discounts-prices-api.wildberries.ru/api/v2/list/goods/filter?limit=%d&offset=%d", limit, offset)
	if filterNmID > 0 {
		url += fmt.Sprintf("&filterNmID=%d", filterNmID)
	}

	client := &http.Client{Timeout: 10 * time.Second}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", apiKey)

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var response ProductResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, err
	}

	return response.Data.ListGoods, nil
}

// Расчёт стоимости, если >1 литра
func CalculateTariff(volumeLiters float64, boxDeliveryBase, boxDeliveryLiter float64) float64 {
	return (volumeLiters-1)*boxDeliveryLiter + boxDeliveryBase
}

func CalculateVolumeLiters(width, height, length int) float64 {
	volumeCm3 := float64(width) * float64(height) * float64(length)
	return volumeCm3 / 1000.0
}

type Commission struct {
	KgvpMarketplace     float64 `json:"kgvpMarketplace"`
	KgvpSupplier        float64 `json:"kgvpSupplier"`
	KgvpSupplierExpress float64 `json:"kgvpSupplierExpress"`
	PaidStorageKgvp     float64 `json:"paidStorageKgvp"`
	ParentID            int     `json:"parentID"`
	ParentName          string  `json:"parentName"`
	SubjectID           int     `json:"subjectID"`
	SubjectName         string  `json:"subjectName"`
}

type CommissionResponse struct {
	Report []Commission `json:"report"`
}

func getCommission(apiKey string) ([]Commission, error) {
	url := "https://common-api.wildberries.ru/api/v1/tariffs/commission"
	client := &http.Client{Timeout: 10 * time.Second}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", apiKey)

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var response CommissionResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, err
	}
	return response.Report, nil
}

func getCardsList(apiKey string, updatedAt string, nmID int) (*CardsListResponse, error) {
	url := "https://content-api.wildberries.ru/content/v2/get/cards/list"
	client := &http.Client{Timeout: 10 * time.Second}

	bodyData := map[string]interface{}{
		"settings": map[string]interface{}{
			"cursor": map[string]interface{}{
				"limit": 100,
			},
			"filter": map[string]interface{}{
				"withPhoto": 1,
				"objectIDs": []int{3979}, // Пример ID
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

func fetchAllCards(apiKey string) []Card {
	var allCards []Card
	var updatedAt string
	var nmID int

	for {
		response, err := getCardsList(apiKey, updatedAt, nmID)
		if err != nil {
			log.Printf("Ошибка запроса карточек: %v\n", err)
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
		log.Printf("Загружено %d карточек, продолжаем...\n", len(allCards))
	}
	return allCards
}

func main() {
	apiKey := os.Getenv("WB_TOKEN")

	// Получаем тарифы (обработка "40,25" → 40.25)
	base, liter, err := getFBSTariffs(apiKey)
	if err != nil {
		log.Printf("Ошибка получения тарифов: %v\n", err)
	} else {
		log.Printf("Тарифы FBS: base=%.2f, liter=%.2f\n", base, liter)
	}

	// Открываем базу
	db, err := sql.Open("sqlite", "ue.db")
	if err != nil {
		log.Fatalf("Ошибка при открытии базы данных: %v", err)
	}
	defer db.Close()

	createTable(db)

	// Загружаем карточки
	allCards := fetchAllCards(apiKey)
	log.Printf("Всего загружено %d карточек.\n", len(allCards))

	// 1) Настраиваем Chrome Allocator (одно окно)
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", false),
		chromedp.Flag("disable-gpu", true),
	)

	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(), opts...)
	defer allocCancel()

	// 2) Создаём один контекст браузера
	ctx, ctxCancel := chromedp.NewContext(allocCtx)
	defer ctxCancel()

	// Загружаем цены
	prices, err := getProductPrices(apiKey, 1000, 0, 0)
	if err != nil {
		log.Printf("Ошибка получения цен: %v\n", err)
	}

	// Загружаем комиссии
	commissions, err := getCommission(apiKey)
	if err != nil {
		log.Printf("Ошибка получения комиссии: %v\n", err)
	}

	// Ищем комиссию для subjectID=3979
	var commissionSum int
	for _, c := range commissions {
		if c.SubjectID == 3979 {
			commissionSum = int(c.KgvpMarketplace)
		}
	}

	// Обрабатываем товары
	// Обрабатываем товары
	for _, card := range allCards {
		// 1) Извлекаем productID и pcs
		parts := strings.Split(card.VendorCode, "_")
		if len(parts) < 2 {
			log.Printf("Некорректный VendorCode: %s\n", card.VendorCode)
			continue
		}
		productID := parts[1]

		// Значение pcs (по умолчанию 1)
		pcsInt := 1
		if len(parts) > 2 {
			if val, err := strconv.Atoi(parts[2]); err == nil {
				pcsInt = val
			}
		}

		// 2) Ищем WB-цены (цена, скидка и т. п.)
		var (
			wbPrice           float64
			wbDiscountedPrice float64
			wbClubDiscounted  float64
		)
		for _, p := range prices {
			// Сравниваем VendorCode
			if p.VendorCode == card.VendorCode {
				// Берём первую размерную позицию или какую-то логику
				if len(p.Sizes) > 0 {
					wbPrice = p.Sizes[0].Price
					wbDiscountedPrice = p.Sizes[0].DiscountedPrice
					wbClubDiscounted = p.Sizes[0].ClubDiscountedPrice
				}
				break
			}
		}

		// 3) Парсим страницу Cargo-Avto (цена + количество доступных складов)
		log.Printf("Обрабатываем товар: %s\n", productID)
		url := baseURL + productID + "/"
		productData, err := scrapeProductData(ctx, url)
		if err != nil {
			log.Printf("Ошибка при обработке товара %s: %v\n", productID, err)
			continue
		}

		// 4) Рассчитываем cost — умножаем cargo-цена * pcs
		cost, err := convertAndMultiply(productData["price"], fmt.Sprintf("%d", pcsInt))
		if err != nil {
			log.Printf("Ошибка при конвертации и умножении %s: %v\n", productID, err)
			continue
		}

		// 5) Рассчитываем тариф
		volumeInLiters := CalculateVolumeLiters(card.Dimensions.Width, card.Dimensions.Height, card.Dimensions.Length)
		tariff := CalculateTariff(volumeInLiters, base, liter)

		// 6) Рассчитываем комиссию (берём clubDiscountPrice)
		commission := int(wbClubDiscounted * float64(commissionSum) / 100.0)

		// 7) Сохраняем в базу
		saveToDatabase(db, SaveParams{
			NmID:              card.NmID,
			VendorCode:        card.VendorCode,
			Width:             card.Dimensions.Width,
			Height:            card.Dimensions.Height,
			Length:            card.Dimensions.Length,
			Pcs:               pcsInt,
			ProductID:         productID,
			WbPrice:           wbPrice,
			WbDiscountedPrice: wbDiscountedPrice,
			WbClubDiscounted:  wbClubDiscounted,
			AvailableCountStr: productData["availableCount"], // строка с кол-вом складов
			Cost:              cost,
			Tariff:            tariff,
			Commission:        commission,
		})
	}

	log.Println("Обработка завершена.")
}

func scrapeProductData(ctx context.Context, url string) (map[string]string, error) {
	var productPrice string
	var availableStoresCount int

	err := chromedp.Run(ctx,
		chromedp.Navigate(url),
		chromedp.Sleep(2*time.Second),
		chromedp.Click(`li.tabs-item a[href="#samovivoz-tabs"]`, chromedp.ByQuery),
		chromedp.Sleep(2*time.Second), // Ждём вкладку
		chromedp.Text(`li[data-min="1"] .price-val`, &productPrice, chromedp.ByQuery),
		chromedp.Evaluate(`document.querySelectorAll('.avail-item-status.avail').length`, &availableStoresCount),
	)
	if err != nil {
		return nil, fmt.Errorf("ошибка парсинга страницы %s: %w", url, err)
	}

	// Очистка цены от лишних символов
	productPrice = strings.TrimSpace(productPrice)
	productPrice = strings.ReplaceAll(productPrice, "p", "")
	productPrice = strings.ReplaceAll(productPrice, " ", "")

	return map[string]string{
		"price":          productPrice,
		"availableCount": fmt.Sprintf("%d", availableStoresCount),
	}, nil
}

func convertAndMultiply(priceStr, multiplierStr string) (int, error) {
	price, err := strconv.ParseFloat(priceStr, 64)
	if err != nil {
		return 0, fmt.Errorf("ошибка преобразования price: %v", err)
	}
	roundedPrice := int(math.Ceil(price))

	multiplier, err := strconv.Atoi(multiplierStr)
	if err != nil {
		return 0, fmt.Errorf("ошибка преобразования multiplier: %v", err)
	}
	return roundedPrice * multiplier, nil
}

func createTable(db *sql.DB) {
	query := `
	CREATE TABLE IF NOT EXISTS products (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		nm_id INTEGER,
		vendor_code TEXT,
		width INTEGER,
		height INTEGER,
		length INTEGER,
		pcs INTEGER,

		product_id TEXT UNIQUE,

		price REAL,
		discounted_price REAL,
		club_discounted_price REAL,

		available_count INTEGER,
		cost INTEGER,
		tariff REAL,
		commission INTEGER
	);
	`
	_, err := db.Exec(query)
	if err != nil {
		log.Fatalf("Ошибка при создании таблицы: %v", err)
	}
	log.Println("Таблица products проверена/создана.")
}

// Дополнительная структура для передачи в saveToDatabase
type SaveParams struct {
	NmID                  int
	VendorCode            string
	Width, Height, Length int
	Pcs                   int

	ProductID string

	WbPrice           float64
	WbDiscountedPrice float64
	WbClubDiscounted  float64

	AvailableCountStr string
	Cost              int
	Tariff            float64
	Commission        int
}

func saveToDatabase(db *sql.DB, params SaveParams) {
	// Преобразуем available_count из строки
	availableCount, err := strconv.Atoi(params.AvailableCountStr)
	if err != nil {
		log.Printf("Ошибка при конвертации availableCount для %s: %v\n", params.ProductID, err)
		availableCount = 0
	}

	query := `
	INSERT INTO products (
		nm_id, vendor_code,
		width, height, length,
		pcs,

		product_id,

		price, discounted_price, club_discounted_price,

		available_count, cost, tariff, commission
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(product_id) DO UPDATE SET
		nm_id = excluded.nm_id,
		vendor_code = excluded.vendor_code,
		width = excluded.width,
		height = excluded.height,
		length = excluded.length,
		pcs = excluded.pcs,

		price = excluded.price,
		discounted_price = excluded.discounted_price,
		club_discounted_price = excluded.club_discounted_price,

		available_count = excluded.available_count,
		cost = excluded.cost,
		tariff = excluded.tariff,
		commission = excluded.commission;
	`

	_, err = db.Exec(query,
		params.NmID, params.VendorCode,
		params.Width, params.Height, params.Length,
		params.Pcs,

		params.ProductID,

		params.WbPrice,
		params.WbDiscountedPrice,
		params.WbClubDiscounted,

		availableCount,
		params.Cost,
		params.Tariff,
		params.Commission,
	)
	if err != nil {
		log.Printf("Ошибка при сохранении данных для %s: %v\n", params.ProductID, err)
	} else {
		log.Printf("Данные для товара %s успешно сохранены.\n", params.ProductID)
	}
}
