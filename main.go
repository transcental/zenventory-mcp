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
	search := request.GetString("search", "")
	viewMode := request.GetString("view_mode", "all")
	warehouseID := request.GetString("warehouse_id", "")

	params := url.Values{}
	params.Set("search", search)
	params.Set("view_mode", viewMode)
	params.Set("include_sellable", "true")
	if warehouseID != "" {
		params.Set("warehouse_id", warehouseID)
	}

	data, err := doRequest("GET", "/items?"+params.Encode(), nil)
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
			mcp.WithDescription("Search Zenventory items/inventory by SKU or description. Returns item details including id, sku, description, stock levels, and pricing."),
			mcp.WithString("search", mcp.Description("Search term — matches SKUs and descriptions"), mcp.Required()),
			mcp.WithString("view_mode", mcp.Description("'all' or 'instock' (default: all)")),
			mcp.WithString("warehouse_id", mcp.Description("Warehouse ID to filter by, or -1 for all")),
		),
		searchItems,
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
