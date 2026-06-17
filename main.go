package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

var apiBase = "https://app.zenventory.com/rest"

func doRequest(method, path string, body io.Reader) ([]byte, error) {
	reqURL := apiBase + path
	req, err := http.NewRequest(method, reqURL, body)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(os.Getenv("ZENVENTORY_API_KEY"), os.Getenv("ZENVENTORY_API_SECRET"))
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(data))
	}
	return data, nil
}

func searchItems(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	searchFor := request.GetString("search", "")
	category := request.GetString("category", "")
	active := request.GetString("active", "")

	params := url.Values{}
	if searchFor != "" {
		params.Set("searchFor", searchFor)
	}
	if category != "" {
		params.Set("category", category)
	}
	if active != "" {
		params.Set("active", active)
	}
	params.Set("perPage", "100")

	data, err := doRequest("GET", "/items?"+params.Encode(), nil)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(string(data)), nil
}

func getStockLevels(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	searchFor := request.GetString("search", "")
	warehouseID := request.GetString("warehouse_id", "")
	warehouseName := request.GetString("warehouse_name", "")
	inStock := request.GetString("in_stock", "")
	category := request.GetString("category", "")

	params := url.Values{}
	if searchFor != "" {
		params.Set("searchFor", searchFor)
	}
	if warehouseID != "" {
		params.Set("warehouseId", warehouseID)
	}
	if warehouseName != "" {
		params.Set("warehouseName", warehouseName)
	}
	if inStock != "" {
		params.Set("inStock", inStock)
	}
	if category != "" {
		params.Set("category", category)
	}
	params.Set("perPage", "100")

	data, err := doRequest("GET", "/inventory?"+params.Encode(), nil)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(string(data)), nil
}

func listOrders(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	status := request.GetString("status", "")
	path := "/customer-orders"
	if status != "" {
		path += "?status=" + url.QueryEscape(status)
	}

	data, err := doRequest("GET", path, nil)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(string(data)), nil
}

func getOrder(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id, err := request.RequireString("id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	data, err := doRequest("GET", "/customer-orders/"+id, nil)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(string(data)), nil
}

func listPurchaseOrders(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	params := url.Values{}
	for _, p := range []string{"orderNumber", "warehouseId", "warehouseName", "supplierId", "supplierName", "clientId", "clientName"} {
		snake := camelToSnake(p)
		if v := request.GetString(snake, ""); v != "" {
			params.Set(p, v)
		}
	}
	for _, flag := range []string{"open", "completed", "draft", "deleted"} {
		if v := request.GetString(flag, ""); v != "" {
			params.Set(flag, v)
		}
	}
	params.Set("perPage", "100")

	data, err := doRequest("GET", "/purchase-orders?"+params.Encode(), nil)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(string(data)), nil
}

func getPurchaseOrder(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id, err := request.RequireString("id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	data, err := doRequest("GET", "/purchase-orders/"+id, nil)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(string(data)), nil
}

// camelToSnake converts camelCase to snake_case for param lookup.
func camelToSnake(s string) string {
	var result strings.Builder
	for i, r := range s {
		if r >= 'A' && r <= 'Z' && i > 0 {
			result.WriteByte('_')
		}
		result.WriteRune(r | 0x20) // to lower
	}
	return result.String()
}

func createOrder(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()

	customerRaw, ok := args["customer"].(map[string]interface{})
	if !ok {
		return mcp.NewToolResultError("customer is required (object with name, surname, and optionally id/email)"), nil
	}

	shippingRaw, ok := args["shipping_address"].(map[string]interface{})
	if !ok {
		return mcp.NewToolResultError("shipping_address is required (object with line1, city, state, zip, countryCode)"), nil
	}

	itemsRaw, ok := args["items"].([]interface{})
	if !ok || len(itemsRaw) == 0 {
		return mcp.NewToolResultError("items is required (array of {sku, quantity})"), nil
	}

	type OrderItem struct {
		SKU      string `json:"sku"`
		Quantity int    `json:"quantity"`
	}

	var orderItems []OrderItem
	for _, raw := range itemsRaw {
		item, ok := raw.(map[string]interface{})
		if !ok {
			return mcp.NewToolResultError("each item must be an object with sku and quantity"), nil
		}
		sku, _ := item["sku"].(string)
		qty, _ := item["quantity"].(float64)
		if sku == "" || qty == 0 {
			return mcp.NewToolResultError("each item needs sku (string) and quantity (number > 0)"), nil
		}
		orderItems = append(orderItems, OrderItem{SKU: sku, Quantity: int(qty)})
	}

	order := map[string]interface{}{
		"customer":        customerRaw,
		"shippingAddress":  shippingRaw,
		"billingAddress":   map[string]interface{}{"sameAsShipping": true},
		"items":           orderItems,
		"saveAsDraft":     true,
	}

	if billingRaw, ok := args["billing_address"].(map[string]interface{}); ok {
		order["billingAddress"] = billingRaw
	}

	for _, field := range []struct{ from, to string }{
		{"order_number", "orderNumber"},
		{"order_reference", "orderReference"},
		{"internal_note", "internalNote"},
		{"note_to_customer", "noteToCustomer"},
	} {
		if v := request.GetString(field.from, ""); v != "" {
			order[field.to] = v
		}
	}

	body, err := json.Marshal(order)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	data, err := doRequest("POST", "/customer-orders", strings.NewReader(string(body)))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	var result map[string]interface{}
	if err := json.Unmarshal(data, &result); err == nil {
		if id, ok := result["id"].(float64); ok {
			orderURL := fmt.Sprintf("https://app.zenventory.com/orders/edit-order/%.0f", id)
			result["_url"] = orderURL
			data, _ = json.Marshal(result)
		}
	}

	return mcp.NewToolResultText(string(data)), nil
}

func main() {
	if os.Getenv("ZENVENTORY_API_KEY") == "" || os.Getenv("ZENVENTORY_API_SECRET") == "" {
		fmt.Fprintln(os.Stderr, "ZENVENTORY_API_KEY and ZENVENTORY_API_SECRET must be set")
		os.Exit(1)
	}

	s := server.NewMCPServer(
		"zenventory",
		"0.1.0",
		server.WithToolCapabilities(true),
	)

	s.AddTool(
		mcp.NewTool("search_items",
			mcp.WithDescription("Search Zenventory items by SKU, UPC, or description. Returns item details including id, sku, description, and pricing. Use get_stock_levels to get inventory quantities."),
			mcp.WithString("search", mcp.Description("Search term — matches SKU, UPC, and description (partial matches supported)")),
			mcp.WithString("category", mcp.Description("Filter by item category")),
			mcp.WithString("active", mcp.Description("'true' for active items (default), 'false' for inactive")),
		),
		searchItems,
	)

	s.AddTool(
		mcp.NewTool("get_stock_levels",
			mcp.WithDescription("Get inventory stock levels for items. Returns quantities on hand, allocated, and available per item per warehouse/location. Use search to filter by SKU or description."),
			mcp.WithString("search", mcp.Description("Search term — matches SKU, UPC, and description")),
			mcp.WithString("warehouse_id", mcp.Description("Filter by warehouse ID")),
			mcp.WithString("warehouse_name", mcp.Description("Filter by warehouse name (ignored if warehouse_id is set)")),
			mcp.WithString("in_stock", mcp.Description("'true' to only return items with stock > 0")),
			mcp.WithString("category", mcp.Description("Filter by item category")),
		),
		getStockLevels,
	)

	s.AddTool(
		mcp.NewTool("list_purchase_orders",
			mcp.WithDescription("List purchase orders. Filter by status, supplier, warehouse, or order number."),
			mcp.WithString("order_number", mcp.Description("Search by order number (partial match)")),
			mcp.WithString("supplier_id", mcp.Description("Filter by supplier ID")),
			mcp.WithString("supplier_name", mcp.Description("Filter by supplier name (ignored if supplier_id set)")),
			mcp.WithString("warehouse_id", mcp.Description("Filter by warehouse ID")),
			mcp.WithString("warehouse_name", mcp.Description("Filter by warehouse name (ignored if warehouse_id set)")),
			mcp.WithString("open", mcp.Description("'true' to filter for open purchase orders")),
			mcp.WithString("completed", mcp.Description("'true' to filter for completed purchase orders")),
			mcp.WithString("draft", mcp.Description("'true' to filter for draft purchase orders")),
			mcp.WithString("deleted", mcp.Description("'true' to include deleted purchase orders")),
		),
		listPurchaseOrders,
	)

	s.AddTool(
		mcp.NewTool("get_purchase_order",
			mcp.WithDescription("Get a single purchase order by ID, including all line items."),
			mcp.WithString("id", mcp.Description("Purchase order ID"), mcp.Required()),
		),
		getPurchaseOrder,
	)

	s.AddTool(
		mcp.NewTool("list_orders",
			mcp.WithDescription("List customer orders. Returns orders with customer info, items, and status."),
			mcp.WithString("status", mcp.Description("Filter by status (e.g. 'open', 'draft')")),
		),
		listOrders,
	)

	s.AddTool(
		mcp.NewTool("get_order",
			mcp.WithDescription("Get a specific customer order by ID, including all line items."),
			mcp.WithString("id", mcp.Description("Order ID"), mcp.Required()),
		),
		getOrder,
	)

	s.AddTool(
		mcp.NewTool("create_order",
			mcp.WithDescription("Create a new customer order in Zenventory as a DRAFT. The order will not be placed until confirmed in the Zenventory UI. Returns the order with a URL to review it. Always show the user what you're about to create and get explicit confirmation before calling this tool."),
			mcp.WithObject("customer", mcp.Description("Customer object: {id} for existing customer, or {name, surname, email} for new"), mcp.Required()),
			mcp.WithObject("shipping_address", mcp.Description("Shipping address: {id} for existing, or {line1, line2, city, state, zip, countryCode, company, name}"), mcp.Required()),
			mcp.WithObject("billing_address", mcp.Description("Billing address (defaults to same as shipping if omitted)")),
			mcp.WithArray("items", mcp.Description("Array of {sku: string, quantity: number}"), mcp.Required()),
			mcp.WithString("order_number", mcp.Description("Custom order number (auto-generated if blank)")),
			mcp.WithString("order_reference", mcp.Description("Order reference string")),
			mcp.WithString("internal_note", mcp.Description("Internal note (not visible to customer)")),
			mcp.WithString("note_to_customer", mcp.Description("Note visible to customer")),
		),
		createOrder,
	)

	if err := server.ServeStdio(s); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
