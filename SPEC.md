# Technical Specification

## System Overview
The system is designed to manage and automate various tasks related to the Wildberries marketplace, including order processing, image handling, product data management, and price/stock updates. The system comprises several components, including backend services, databases, and interactions with external APIs such as Wildberries and Telegram.

### Main Components and Their Roles
- **Backend Services:** Handle core logic such as order checking, image processing, product data scraping, and price/stock updates.
- **Databases:** Store product information, order data, and other relevant metadata. Primarily uses SQLite for local storage.
- **External APIs:** Interact with Wildberries API for fetching orders, updating product information, and sending messages via Telegram Bot API for notifications.

## Core Functionality
The core functionality of the system revolves around the following primary features:

### 1. Order Processing and Notification
- **`checkWildberriesOrders` Function:** Queries the Wildberries API for new orders and sends a Telegram message if new orders are found. 
  - Retrieves `WB_TOKEN` from environment variables.
  - Makes an HTTP GET request to the Wildberries API.
  - Parses the JSON response and constructs a message detailing new orders.
  - Calls `sendTelegramMessage` to send the message.
- **`sendTelegramMessage` Function:** Sends a message to a Telegram chat using the bot API.
  - Retrieves `TELEGRAM_BOT_TOKEN` and `TELEGRAM_CHAT_ID` from environment variables.
  - Constructs the API URL and payload.
  - Makes an HTTP POST request to send the message.
  - Logs the response from Telegram.
- **`main` Function:** The entry point of the application, which continuously checks for new orders every minute by calling `checkWildberriesOrders` in an infinite loop with a 1-minute sleep interval between calls.

### 2. Image Processing and Upload
- **`upload_single_image_to_wb` Function:** Uploads a single image to Wildberries using the provided API key.
  - Constructs the necessary headers and file payload.
  - Sends a POST request to the Wildberries content API.
  - Handles the response to determine if the upload was successful.
- **`process_vendor_list` Function:** Processes a list of vendor items, checks if the corresponding image files exist, and uploads them to Wildberries if they do.
  - Iterates over the vendor list to extract `nmID` and `vendorCode`.
  - Parses the `vendorCode` to determine the expected image file name.
  - Checks if the image file exists in the specified folder.
  - Calls `upload_single_image_to_wb` to upload the image if it exists.

### 3. Product Data Management
- **`main_prod_info.go` Functions:**
  - **`scrapeProductData` Function:** Scrapes product data (name, price, characteristics, and description) from a web page using ChromeDP.
  - **`saveToDatabase` Function:** Saves the scraped product data to the SQLite database.
  - **`cleanHTML` Function:** Cleans and formats the HTML content extracted from the web page.
  - **`parseCharacteristics` Function:** Parses the characteristics section of the scraped data into a map.
  - **`cleanDescription` Function:** Cleans and improves the description text.

### 4. Price and Stock Updates
- **`update_prices.ipynb` Functions:**
  - **Updating Prices and Discounts:** Fetches product data from a SQLite database, constructs payloads, and sends batch requests to the Wildberries API to update prices and discounts.
  - **Updating Stock Levels:** Fetches stock data from the SQLite database, constructs payloads, and sends batch requests to the Wildberries API to update stock levels.
  - **Rate Limiting and Batch Processing:** Implements rate limiting and batch processing to comply with the Wildberries API rate limits.
  - **Error Handling for Warehouse Restrictions:** Includes specific error handling for cases where the selected warehouse is not suitable for certain types of goods.

## Architecture
The system is structured to handle data flow efficiently from external APIs to local databases and vice versa. 

### Data Flow Patterns
1. **Order Processing:**
   - Data from the Wildberries API is fetched periodically.
   - New orders are identified and messages are sent via the Telegram Bot API.
   - Relevant information and errors are logged.

2. **Image Processing and Upload:**
   - Image files are processed by resizing, adding text labels, and uploading to the Wildberries content API.
   - The system relies on a specific naming convention for image files to match them with the correct products.

3. **Product Data Management:**
   - Product data is scraped from web pages and stored in a SQLite database.
   - The scraped data is cleaned and formatted for better readability and storage.

4. **Price and Stock Updates:**
   - Product and stock data are fetched from a SQLite database.
   - Payloads are constructed and sent in batch requests to the Wildberries API to update prices, discounts, and stock levels.
   - Rate limiting and error handling mechanisms are in place to ensure smooth operation.