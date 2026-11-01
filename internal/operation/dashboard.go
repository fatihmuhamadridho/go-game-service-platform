package operation

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func (s *Service) Transactions(ctx context.Context) ([]map[string]any, error) {
	entries, err := s.repository.ListOrders(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		order, _ := entry["order"].(map[string]any)
		if order == nil {
			continue
		}
		items = append(items, map[string]any{
			"id": order["id"], "internalId": order["id"], "orderNumber": order["orderNumber"],
			"date": order["createdAt"], "user": order["customerName"], "game": order["gameName"],
			"service": order["serviceName"], "total": order["totalAmount"], "amount": order["totalAmount"],
			"status": order["paymentStatus"], "method": order["paymentMethod"], "paymentId": order["paymentId"],
		})
	}
	return items, nil
}

func (s *Service) ListCollectionPage(ctx context.Context, bucket, search, status, method, category string, page, size int) (map[string]any, error) {
	var items []map[string]any
	var err error
	if bucket == "transactions" {
		items, err = s.Transactions(ctx)
	} else {
		items, err = s.repository.List(ctx, bucket)
	}
	if err != nil {
		return nil, err
	}
	search = strings.ToLower(strings.TrimSpace(search))
	filtered := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if search != "" {
			encoded, _ := json.Marshal(item)
			if !strings.Contains(strings.ToLower(string(encoded)), search) {
				continue
			}
		}
		if status != "" && !strings.EqualFold(stringValue(item["status"]), status) && !strings.EqualFold(stringValue(item["result"]), status) {
			continue
		}
		if method != "" && !strings.EqualFold(stringValue(item["method"]), method) {
			continue
		}
		if category != "" && !strings.EqualFold(stringValue(item["category"]), category) {
			continue
		}
		filtered = append(filtered, item)
	}
	return pageMap(filtered, page, size), nil
}

func (s *Service) ListOrderPage(ctx context.Context, search, status, method string, page, size int) (map[string]any, error) {
	entries, err := s.repository.ListOrders(ctx)
	if err != nil {
		return nil, err
	}
	search, status = strings.ToLower(strings.TrimSpace(search)), strings.ToLower(strings.TrimSpace(status))
	orders := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		order, _ := entry["order"].(map[string]any)
		if order == nil {
			continue
		}
		if status != "" && !strings.EqualFold(stringValue(order["orderStatus"]), status) && !strings.EqualFold(stringValue(order["paymentStatus"]), status) {
			continue
		}
		if method != "" && !strings.EqualFold(stringValue(order["paymentMethod"]), method) {
			continue
		}
		if search != "" {
			encoded, _ := json.Marshal(order)
			if !strings.Contains(strings.ToLower(string(encoded)), search) {
				continue
			}
		}
		orders = append(orders, order)
	}
	return pageMap(orders, page, size), nil
}

func (s *Service) LookupTrackingOrder(ctx context.Context, number, phone string) (map[string]any, error) {
	if strings.TrimSpace(number) == "" || len(number) > 100 || !validWhatsApp(phone) {
		return nil, ErrOrderNotFound
	}
	entry, err := s.repository.FindOrder(ctx, bson.D{{Key: "order.orderNumber", Value: strings.TrimSpace(number)}})
	if err != nil {
		return nil, err
	}
	if entry == nil {
		return nil, ErrOrderNotFound
	}
	order, _ := entry["order"].(map[string]any)
	if order == nil || normalizePhone(stringValue(order["customerWhatsapp"])) != normalizePhone(phone) {
		return nil, ErrOrderNotFound
	}
	return map[string]any{"trackingToken": entry["trackingToken"], "expiresAt": order["expiresAt"]}, nil
}

func pageMap(items []map[string]any, page, size int) map[string]any {
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 20
	}
	if size > 100 {
		size = 100
	}
	total := len(items)
	start := (page - 1) * size
	if start > total {
		start = total
	}
	end := min(start+size, total)
	return map[string]any{"items": items[start:end], "total": total, "page": page, "pageSize": size, "totalPages": (total + size - 1) / size}
}

func (s *Service) Dashboard(ctx context.Context, from, to, granularity string) (map[string]any, error) {
	orders, err := s.ListTransactions(ctx)
	if err != nil {
		return nil, err
	}
	orders, previousOrders := ordersForDashboardRange(orders, from, to)
	users, err := s.List(ctx, "users")
	if err != nil {
		return nil, err
	}
	activity, err := s.List(ctx, "activityLogs")
	if err != nil {
		return nil, err
	}
	serviceCount, err := s.CatalogServiceCount(ctx)
	if err != nil {
		return nil, err
	}
	statuses := map[string]int{}
	methods := map[string]map[string]any{}
	byDate := map[string]map[string]any{}
	gameCounts := map[string]int{}
	gameRevenue := map[string]float64{}
	var revenue float64
	var previousRevenue float64
	for _, row := range previousOrders {
		if strings.EqualFold(stringValue(row["status"]), "PAID") {
			previousRevenue += numberValue(row["amount"])
		}
	}
	for _, row := range orders {
		status := strings.ToUpper(stringValue(row["status"]))
		statuses[status]++
		amount := numberValue(row["amount"])
		if status == "PAID" {
			revenue += amount
		}
		method := stringValue(row["method"])
		if method != "" {
			current := methods[method]
			if current == nil {
				current = map[string]any{"method": method, "count": 0, "amount": float64(0)}
				methods[method] = current
			}
			current["count"] = current["count"].(int) + 1
			current["amount"] = current["amount"].(float64) + amount
		}
		game := stringValue(row["game"])
		gameCounts[game]++
		gameRevenue[game] += amount
		date := stringValue(row["date"])
		label := dashboardBucketLabel(date, granularity)
		series := byDate[label]
		if series == nil {
			series = map[string]any{"label": label, "orders": 0, "revenue": float64(0)}
			byDate[label] = series
		}
		series["orders"] = series["orders"].(int) + 1
		if status == "PAID" {
			series["revenue"] = series["revenue"].(float64) + amount
		}
	}
	statusDistribution := make([]map[string]any, 0, len(statuses))
	for status, count := range statuses {
		percent := 0.0
		if len(orders) > 0 {
			percent = float64(count) * 100 / float64(len(orders))
		}
		statusDistribution = append(statusDistribution, map[string]any{"status": status, "count": count, "percent": percent})
	}
	paymentMethods := make([]map[string]any, 0, len(methods))
	for _, item := range methods {
		paymentMethods = append(paymentMethods, item)
	}
	series := make([]map[string]any, 0, len(byDate))
	for _, item := range byDate {
		series = append(series, item)
	}
	sort.Slice(series, func(i, j int) bool { return stringValue(series[i]["label"]) < stringValue(series[j]["label"]) })
	popularGames := make([]map[string]any, 0, len(gameCounts))
	for name, count := range gameCounts {
		popularGames = append(popularGames, map[string]any{"name": name, "count": count, "percent": float64(count) * 100 / float64(maxInt(1, len(orders)))})
	}
	sort.Slice(popularGames, func(i, j int) bool { return popularGames[i]["count"].(int) > popularGames[j]["count"].(int) })
	if len(popularGames) > 5 {
		popularGames = popularGames[:5]
	}
	gameRevenueRows := make([]map[string]any, 0, len(gameRevenue))
	for name, amount := range gameRevenue {
		gameRevenueRows = append(gameRevenueRows, map[string]any{"name": name, "amount": amount, "percent": amount * 100 / maxFloat(1, revenue)})
	}
	sort.Slice(gameRevenueRows, func(i, j int) bool {
		return gameRevenueRows[i]["amount"].(float64) > gameRevenueRows[j]["amount"].(float64)
	})
	if len(gameRevenueRows) > 5 {
		gameRevenueRows = gameRevenueRows[:5]
	}
	recentOrders := make([]map[string]any, 0, len(orders))
	for _, row := range orders {
		recentOrders = append(recentOrders, map[string]any{"id": row["orderNumber"], "date": row["date"], "user": row["user"], "game": row["game"], "service": row["service"], "total": row["total"], "status": row["status"]})
	}
	if len(recentOrders) > 5 {
		recentOrders = recentOrders[:5]
	}
	recentActivity := make([]map[string]any, 0, len(activity))
	for _, row := range activity {
		recentActivity = append(recentActivity, map[string]any{"id": row["id"], "title": row["action"], "detail": row["target"], "occurredAt": row["occurredAt"], "category": row["category"]})
	}
	if len(recentActivity) > 5 {
		recentActivity = recentActivity[:5]
	}
	overview := map[string]any{"metrics": map[string]any{"orderCount": len(orders), "orderChangePercent": percentChange(float64(len(orders)), float64(len(previousOrders))), "revenue": revenue, "revenueChangePercent": percentChange(revenue, previousRevenue), "userCount": len(users), "serviceCount": serviceCount}, "series": series, "statusDistribution": statusDistribution, "popularGames": popularGames, "gameRevenue": gameRevenueRows, "paymentMethods": paymentMethods, "recentOrders": recentOrders, "recentActivity": recentActivity}
	return map[string]any{"overview": overview}, nil
}

func stringValue(value any) string { text, _ := value.(string); return text }
func numberValue(value any) float64 {
	switch number := value.(type) {
	case float64:
		return number
	case int:
		return float64(number)
	case int32:
		return float64(number)
	case int64:
		return float64(number)
	default:
		return 0
	}
}

func ordersForDashboardRange(orders []map[string]any, fromText, toText string) ([]map[string]any, []map[string]any) {
	from, fromErr := time.Parse("2006-01-02", fromText)
	to, toErr := time.Parse("2006-01-02", toText)
	if fromText == "" || toText == "" || fromErr != nil || toErr != nil || to.Before(from) {
		return orders, nil
	}
	to = to.AddDate(0, 0, 1)
	previousFrom := from.Add(-to.Sub(from))
	current := make([]map[string]any, 0)
	previous := make([]map[string]any, 0)
	for _, order := range orders {
		date, err := time.Parse(time.RFC3339, stringValue(order["date"]))
		if err != nil {
			continue
		}
		if !date.Before(from) && date.Before(to) {
			current = append(current, order)
		}
		if !date.Before(previousFrom) && date.Before(from) {
			previous = append(previous, order)
		}
	}
	return current, previous
}

func dashboardBucketLabel(value, granularity string) string {
	date, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return value
	}
	switch granularity {
	case "weekly":
		year, week := date.ISOWeek()
		return fmt.Sprintf("%d-W%02d", year, week)
	case "monthly":
		return date.Format("2006-01")
	default:
		return date.Format("2006-01-02")
	}
}

func percentChange(current, previous float64) float64 {
	if previous == 0 {
		if current == 0 {
			return 0
		}
		return 100
	}
	return (current - previous) * 100 / previous
}
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
