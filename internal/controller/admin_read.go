package controller

import (
	"context"
	"errors"
	"math"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"example.com/learn-golang/internal/model"
	"go.mongodb.org/mongo-driver/v2/bson"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

type AdminReadError struct {
	Code, Message string
	Status        int
}

func (e *AdminReadError) Error() string { return e.Message }
func ToAdminReadError(err error, prefix string) *AdminReadError {
	var e *AdminReadError
	if errors.As(err, &e) {
		return e
	}
	codes := map[string]string{"ACT": "ACTT03001", "TRX": "TRNT03001", "SET": "SETT03001", "DSH": "DSHT03001"}
	msgs := map[string]string{"ACT": "Log aktivitas sementara tidak tersedia.", "TRX": "Data transaksi sementara tidak tersedia.", "SET": "Pengaturan platform sementara tidak tersedia.", "DSH": "Ringkasan dashboard sementara tidak tersedia."}
	return &AdminReadError{Code: codes[prefix], Message: msgs[prefix], Status: 500}
}
func readFail(c, m string, s int) error { return &AdminReadError{Code: c, Message: m, Status: s} }

type AdminReadController struct{ repo model.AdminReadRepository }

func NewAdminReadController(repo model.AdminReadRepository) *AdminReadController {
	return &AdminReadController{repo: repo}
}
func (c *AdminReadController) ListTransactions(ctx context.Context, search, status, method, typ string, page, size int) (map[string]any, error) {
	rows, e := c.repo.ListTransactions(ctx)
	if e != nil {
		return nil, e
	}
	lower := cases.Lower(language.Indonesian).String
	search = lower(search)
	out := []map[string]any{}
	success := 0.0
	pending, refunded := 0, 0
	for _, r := range rows {
		v := transactionDTO(r)
		if status != "" && v["status"] != status || method != "" && v["method"] != method || typ != "" && v["type"] != typ {
			continue
		}
		hay := lower(strings.Join([]string{v["id"].(string), v["orderId"].(string), v["customerName"].(string), v["customerContact"].(string), v["providerReference"].(string)}, " "))
		if search != "" && !strings.Contains(hay, search) {
			continue
		}
		out = append(out, v)
		switch v["status"] {
		case "Berhasil":
			success += v["amount"].(float64)
		case "Menunggu":
			pending++
		case "Dikembalikan":
			refunded++
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i]["createdAt"].(time.Time).After(out[j]["createdAt"].(time.Time)) })
	total := len(out)
	start := (page - 1) * size
	if start > total {
		start = total
	}
	end := start + size
	if end > total {
		end = total
	}
	return map[string]any{"transactions": out[start:end], "total": total, "pagination": map[string]int{"page": page, "pageSize": size, "total": total, "totalPages": (total + size - 1) / size}, "summary": map[string]any{"successfulAmount": success, "pendingCount": pending, "refundedCount": refunded}}, nil
}
func (c *AdminReadController) GetTransaction(ctx context.Context, raw string) (map[string]any, error) {
	id, e := bson.ObjectIDFromHex(raw)
	if e != nil {
		return nil, readFail("TRNB02001", "ID transaksi tidak valid.", 400)
	}
	row, e := c.repo.FindTransaction(ctx, id)
	if e != nil {
		return nil, e
	}
	if row == nil {
		return nil, readFail("TRNB02002", "Transaksi tidak ditemukan.", 404)
	}
	history, e := c.repo.ListTransactionHistory(ctx, id)
	if e != nil {
		return nil, e
	}
	sort.SliceStable(history, func(i, j int) bool { return history[i].ChangedAt.Before(history[j].ChangedAt) })
	dto := transactionDTO(*row)
	events := make([]map[string]any, 0, len(history))
	for _, h := range history {
		events = append(events, map[string]any{"oldStatus": h.OldStatus, "newStatus": h.NewStatus, "reason": h.Reason, "occurredAt": h.ChangedAt})
	}
	dto["statusHistory"] = events
	return dto, nil
}
func (c *AdminReadController) ListActivity(ctx context.Context, search, category, from, to string, page, size int) (map[string]any, error) {
	rows, e := c.repo.ListActivity(ctx)
	if e != nil {
		return nil, e
	}
	lower := cases.Lower(language.Indonesian).String
	needle := lower(search)
	startDate, endDate := time.Time{}, time.Time{}
	invalidDateRange := false
	if from != "" {
		var valid bool
		startDate, valid = parseJSDateOnly(from)
		invalidDateRange = invalidDateRange || !valid
	}
	if to != "" {
		var valid bool
		endDate, valid = parseJSDateOnly(to)
		invalidDateRange = invalidDateRange || !valid
		if valid {
			endDate = endDate.Add(24*time.Hour - time.Millisecond)
		}
	}
	out := []map[string]any{}
	succeeded, failed := 0, 0
	for _, r := range rows {
		if invalidDateRange {
			continue
		}
		cat := activityLabel(r.Category)
		name, email, target, ip := "System", "", "—", "—"
		if r.ActorName != nil {
			name = *r.ActorName
		}
		if r.ActorEmail != nil {
			email = *r.ActorEmail
		}
		if r.TargetReference != nil {
			target = *r.TargetReference
		} else {
			target = r.TargetType
		}
		if r.SourceIP != nil {
			ip = *r.SourceIP
		}
		result := "Gagal"
		if r.Result == "SUCCESS" {
			result = "Berhasil"
		}
		if category != "" && category != cat || (!startDate.IsZero() && r.OccurredAt.Before(startDate)) || (!endDate.IsZero() && r.OccurredAt.After(endDate)) {
			continue
		}
		if needle != "" && !strings.Contains(lower(strings.Join([]string{name, email, r.ActionLabel, target, ip}, " ")), needle) {
			continue
		}
		out = append(out, map[string]any{"id": r.ID.Hex(), "adminName": name, "adminEmail": email, "action": r.ActionLabel, "target": target, "category": cat, "occurredAt": r.OccurredAt, "ipAddress": ip, "result": result})
		if result == "Berhasil" {
			succeeded++
		} else {
			failed++
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i]["occurredAt"].(time.Time).After(out[j]["occurredAt"].(time.Time))
	})
	total := len(out)
	start := (page - 1) * size
	if start > total {
		start = total
	}
	end := start + size
	if end > total {
		end = total
	}
	return map[string]any{"activityLogs": out[start:end], "total": total, "summary": map[string]int{"succeeded": succeeded, "failed": failed}, "pagination": map[string]int{"page": page, "pageSize": size, "total": total, "totalPages": (total + size - 1) / size}}, nil
}

type SettingsInput struct {
	SiteName          string       `json:"siteName"`
	SiteDescription   *string      `json:"siteDescription"`
	ContactEmail      string       `json:"contactEmail"`
	ContactWhatsApp   string       `json:"contactWhatsapp"`
	SocialLinks       []SocialLink `json:"socialLinks"`
	BusinessHours     *string      `json:"businessHours"`
	MaintenanceMode   bool         `json:"maintenanceMode"`
	AllowRegistration bool         `json:"allowRegistration"`
	DefaultCurrency   string       `json:"defaultCurrency"`
	Timezone          string       `json:"timezone"`
}
type SocialLink struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

func (c *AdminReadController) GetSettings(ctx context.Context) (map[string]any, error) {
	row, e := c.repo.ListSettings(ctx)
	if e != nil {
		return nil, e
	}
	if row == nil {
		return map[string]any{"settings": defaultSettings(), "version": 0, "updatedAt": nil}, nil
	}
	return map[string]any{"settings": settingsDTO(*row), "version": row.Version, "updatedAt": row.UpdatedAt}, nil
}
func (c *AdminReadController) UpdateSettings(ctx context.Context, in SettingsInput, actor bson.ObjectID, activity model.SettingsActivityContext) (map[string]any, error) {
	if !validSettings(in) {
		return nil, readFail("SETB02001", "Periksa kembali nilai pengaturan.", 400)
	}
	current, e := c.repo.ListSettings(ctx)
	if e != nil {
		return nil, e
	}
	version := int32(1)
	id := bson.NewObjectID()
	if current != nil {
		version = current.Version + 1
		id = current.ID
	}
	links := make([]map[string]string, 0, len(in.SocialLinks))
	var insta *string
	for _, l := range in.SocialLinks {
		label, url := JSTrim(l.Label), JSTrim(l.URL)
		links = append(links, map[string]string{"label": label, "url": url})
		if strings.EqualFold(label, "instagram") {
			insta = &url
		}
	}
	row := model.PlatformSettings{ID: id, SingletonKey: "current", SiteName: JSTrim(in.SiteName), SiteDescription: trimPtr(in.SiteDescription), ContactEmail: strings.ToLower(JSTrim(in.ContactEmail)), ContactWhatsApp: JSTrim(in.ContactWhatsApp), SocialLinks: links, InstagramURL: insta, BusinessHours: trimPtr(in.BusinessHours), MaintenanceMode: in.MaintenanceMode, AllowRegistration: in.AllowRegistration, DefaultCurrency: in.DefaultCurrency, Timezone: in.Timezone, Version: version, UpdatedAt: time.Now().UTC()}
	if !actor.IsZero() {
		row.UpdatedBy = &actor
	}
	activity.ChangedFields = map[string]any{
		"siteName": in.SiteName, "siteDescription": in.SiteDescription, "contactEmail": in.ContactEmail,
		"contactWhatsapp": in.ContactWhatsApp, "socialLinks": in.SocialLinks, "businessHours": in.BusinessHours,
		"maintenanceMode": in.MaintenanceMode, "allowRegistration": in.AllowRegistration,
		"defaultCurrency": in.DefaultCurrency, "timezone": in.Timezone,
	}
	if current != nil {
		activity.TargetID = &current.ID
	}
	if e = c.repo.UpsertSettings(ctx, row); e != nil {
		return nil, e
	}
	_ = c.repo.RecordSettingsActivity(ctx, actor, row, activity)
	return c.GetSettings(ctx)
}

func (c *AdminReadController) Dashboard(ctx context.Context, start, end time.Time, granularity string) (map[string]any, error) {
	orders, err := c.repo.ListAllOrders(ctx)
	if err != nil {
		return nil, err
	}
	transactions, err := c.repo.ListTransactions(ctx)
	if err != nil {
		return nil, err
	}
	users, err := c.repo.ListAllUsers(ctx)
	if err != nil {
		return nil, err
	}
	services, err := c.repo.ListAllGameServices(ctx)
	if err != nil {
		return nil, err
	}
	items, err := c.repo.ListAllOrderItems(ctx)
	if err != nil {
		return nil, err
	}
	activity, err := c.repo.ListActivity(ctx)
	if err != nil {
		return nil, err
	}
	duration := end.Sub(start) + 24*time.Hour
	previousStart, previousEnd := start.Add(-duration), start.Add(-time.Millisecond)
	period, previous := []model.Order{}, []model.Order{}
	for _, o := range orders {
		if !o.CreatedAt.Before(start) && !o.CreatedAt.After(end) {
			period = append(period, o)
		}
		if !o.CreatedAt.Before(previousStart) && !o.CreatedAt.After(previousEnd) {
			previous = append(previous, o)
		}
	}
	validTx := func(t model.FinancialTransaction, a, b time.Time) bool {
		return !t.CreatedAt.Before(a) && !t.CreatedAt.After(b) && t.Type == "PAYMENT" && containsString([]string{"SUCCESS", "SETTLED", "COMPLETED"}, t.Status)
	}
	tx, prevTx := []model.FinancialTransaction{}, []model.FinancialTransaction{}
	for _, t := range transactions {
		if validTx(t, start, end) {
			tx = append(tx, t)
		}
		if validTx(t, previousStart, previousEnd) {
			prevTx = append(prevTx, t)
		}
	}
	money := func(t model.FinancialTransaction) float64 {
		v, _ := strconv.ParseFloat(t.Amount.String(), 64)
		return v
	}
	revenue, prevRevenue := 0.0, 0.0
	for _, t := range tx {
		revenue += money(t)
	}
	for _, t := range prevTx {
		prevRevenue += money(t)
	}
	change := func(a, b float64) int {
		if b == 0 {
			return 0
		}
		return int(math.Floor((a-b)/b*100 + 0.5))
	}
	labels := map[string]string{"PENDING_PAYMENT": "Menunggu", "PENDING_VALIDATION": "Menunggu Validasi", "PROCESSING": "Diproses", "COMPLETED": "Selesai", "CANCELLED": "Dibatalkan"}
	gameByOrder := map[bson.ObjectID]string{}
	for _, it := range items {
		if gameByOrder[it.OrderID] == "" {
			gameByOrder[it.OrderID] = defaultStringValue(it.GameNameSnapshot, "Game")
		}
	}
	buckets := map[string]map[string]any{}
	bucket := func(d time.Time) map[string]any {
		d = time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, time.UTC)
		if granularity == "weekly" {
			d = d.AddDate(0, 0, -int(d.Weekday()))
		}
		if granularity == "monthly" {
			d = time.Date(d.Year(), d.Month(), 1, 0, 0, 0, 0, time.UTC)
		}
		key := d.Format("2006-01-02")
		if buckets[key] == nil {
			buckets[key] = map[string]any{"label": key, "orders": 0, "revenue": 0.0}
		}
		return buckets[key]
	}
	statusCounts := map[string]int{}
	statusOrder := []string{}
	games := map[string]map[string]float64{}
	gameOrder := []string{}
	orderGame := map[bson.ObjectID]string{}
	for _, o := range period {
		b := bucket(o.CreatedAt)
		b["orders"] = b["orders"].(int) + 1
		label := defaultStringValue(labels[o.OrderStatus], o.OrderStatus)
		if _, exists := statusCounts[label]; !exists {
			statusOrder = append(statusOrder, label)
		}
		statusCounts[label]++
		name := defaultStringValue(gameByOrder[o.ID], "Game")
		orderGame[o.ID] = name
		if games[name] == nil {
			games[name] = map[string]float64{"count": 0, "revenue": 0}
			gameOrder = append(gameOrder, name)
		}
		games[name]["count"]++
	}
	methods := map[string]map[string]float64{}
	methodOrder := []string{}
	for _, t := range tx {
		b := bucket(t.CreatedAt)
		b["revenue"] = b["revenue"].(float64) + money(t)
		name := orderGame[t.OrderID]
		if name != "" {
			games[name]["revenue"] += money(t)
		}
		method := t.Method
		if method == "BCA_TRANSFER" {
			method = "Transfer Bank"
		}
		if methods[method] == nil {
			methods[method] = map[string]float64{"count": 0, "amount": 0}
			methodOrder = append(methodOrder, method)
		}
		methods[method]["count"]++
		methods[method]["amount"] += money(t)
	}
	series := []map[string]any{}
	for _, v := range buckets {
		series = append(series, v)
	}
	sort.SliceStable(series, func(i, j int) bool { return series[i]["label"].(string) < series[j]["label"].(string) })
	distribution := []map[string]any{}
	for _, name := range statusOrder {
		count := statusCounts[name]
		pct := 0
		if len(period) > 0 {
			pct = int(math.Round(float64(count) / float64(len(period)) * 100))
		}
		distribution = append(distribution, map[string]any{"status": name, "count": count, "percent": pct})
	}
	maxCount, maxRevenue := 1.0, 1.0
	for _, name := range gameOrder {
		v := games[name]
		if v["count"] > maxCount {
			maxCount = v["count"]
		}
		if v["revenue"] > maxRevenue {
			maxRevenue = v["revenue"]
		}
	}
	popular, gameRevenue := []map[string]any{}, []map[string]any{}
	for _, name := range gameOrder {
		v := games[name]
		popular = append(popular, map[string]any{"name": name, "count": int(v["count"]), "percent": int(math.Round(v["count"] / maxCount * 100))})
		gameRevenue = append(gameRevenue, map[string]any{"name": name, "amount": v["revenue"], "percent": int(math.Round(v["revenue"] / maxRevenue * 100))})
	}
	sort.SliceStable(popular, func(i, j int) bool { return popular[i]["count"].(int) > popular[j]["count"].(int) })
	sort.SliceStable(gameRevenue, func(i, j int) bool { return gameRevenue[i]["amount"].(float64) > gameRevenue[j]["amount"].(float64) })
	if len(popular) > 5 {
		popular = popular[:5]
	}
	if len(gameRevenue) > 5 {
		gameRevenue = gameRevenue[:5]
	}
	paymentMethods := []map[string]any{}
	for _, name := range methodOrder {
		v := methods[name]
		paymentMethods = append(paymentMethods, map[string]any{"method": name, "count": int(v["count"]), "amount": v["amount"]})
	}
	latest := append([]model.Order(nil), orders...)
	sort.SliceStable(latest, func(i, j int) bool { return latest[i].CreatedAt.After(latest[j].CreatedAt) })
	recentOrders := []map[string]any{}
	for _, o := range latest {
		if len(recentOrders) == 5 {
			break
		}
		recentOrders = append(recentOrders, map[string]any{"id": o.OrderNumber, "date": o.CreatedAt, "user": o.CustomerName, "game": defaultStringValue(gameByOrder[o.ID], "Game"), "service": firstOrderService(items, o.ID), "total": decimalFloat(o.TotalAmount), "status": defaultStringValue(labels[o.OrderStatus], o.OrderStatus)})
	}
	recentActivityRows := append([]model.ActivityLog(nil), activity...)
	sort.SliceStable(recentActivityRows, func(i, j int) bool {
		return recentActivityRows[i].OccurredAt.After(recentActivityRows[j].OccurredAt)
	})
	recentActivity := []map[string]any{}
	for _, a := range recentActivityRows {
		if len(recentActivity) == 5 {
			break
		}
		target := a.TargetType
		if a.TargetReference != nil {
			target = *a.TargetReference
		}
		recentActivity = append(recentActivity, map[string]any{"id": a.ID.Hex(), "title": a.ActionLabel, "detail": target, "occurredAt": a.OccurredAt, "category": a.Category})
	}
	userCount, serviceCount := 0, 0
	for _, u := range users {
		if u.DeletedAt == nil {
			userCount++
		}
	}
	for _, s := range services {
		if s.IsActive && s.DeletedAt == nil {
			serviceCount++
		}
	}
	return map[string]any{"from": start, "to": end, "granularity": granularity, "metrics": map[string]any{"orderCount": len(period), "orderChangePercent": change(float64(len(period)), float64(len(previous))), "revenue": revenue, "revenueChangePercent": change(revenue, prevRevenue), "userCount": userCount, "serviceCount": serviceCount}, "series": series, "statusDistribution": distribution, "popularGames": popular, "gameRevenue": gameRevenue, "paymentMethods": paymentMethods, "recentOrders": recentOrders, "recentActivity": recentActivity}, nil
}
func decimalFloat(v bson.Decimal128) float64 { x, _ := strconv.ParseFloat(v.String(), 64); return x }
func firstOrderService(items []model.OrderItem, id bson.ObjectID) string {
	for _, i := range items {
		if i.OrderID == id {
			return defaultStringValue(i.ServiceNameSnapshot, "Layanan")
		}
	}
	return "Layanan"
}
func defaultStringValue(a, b string) string {
	if a == "" {
		return b
	}
	return a
}
func transactionDTO(r model.FinancialTransaction) map[string]any {
	status := map[string]string{"SUCCESS": "Berhasil", "SETTLED": "Berhasil", "COMPLETED": "Berhasil", "PENDING": "Menunggu", "PROCESSING": "Menunggu", "FAILED": "Gagal", "CANCELLED": "Gagal", "REFUNDED": "Dikembalikan"}[r.Status]
	if status == "" {
		status = r.Status
	}
	typ := "Pembayaran Pesanan"
	if r.Type == "REFUND" {
		typ = "Refund"
	}
	method := r.Method
	if method == "BCA_TRANSFER" {
		method = "Transfer Bank BCA"
	}
	amount, _ := strconv.ParseFloat(r.Amount.String(), 64)
	name, contact, reference := "Pelanggan", "", ""
	if r.CustomerNameSnapshot != nil {
		name = *r.CustomerNameSnapshot
	}
	if r.CustomerContactSnapshot != nil {
		contact = *r.CustomerContactSnapshot
	}
	if r.ProviderReference != nil {
		reference = *r.ProviderReference
	}
	settled := any("")
	if r.SettledAt != nil {
		settled = *r.SettledAt
	}
	return map[string]any{"id": r.TransactionNumber, "internalId": r.ID.Hex(), "orderId": r.OrderID.Hex(), "customerName": name, "customerContact": contact, "type": typ, "method": method, "amount": amount, "status": status, "providerReference": reference, "createdAt": r.CreatedAt, "settledAt": settled}
}
func activityLabel(s string) string {
	switch s {
	case "ORDER":
		return "Pesanan"
	case "CATALOG":
		return "Produk"
	case "USER":
		return "Pengguna"
	case "CONTENT":
		return "Konten"
	}
	return "Pengaturan"
}
func defaultSettings() SettingsInput {
	return SettingsInput{SiteName: "JokiHub", SiteDescription: strPtr(""), SocialLinks: []SocialLink{}, DefaultCurrency: "IDR", Timezone: "Asia/Jakarta", AllowRegistration: true}
}
func settingsDTO(r model.PlatformSettings) SettingsInput {
	description := r.SiteDescription
	if description == nil {
		description = strPtr("")
	}
	out := SettingsInput{SiteName: r.SiteName, SiteDescription: description, ContactEmail: r.ContactEmail, ContactWhatsApp: r.ContactWhatsApp, BusinessHours: r.BusinessHours, MaintenanceMode: r.MaintenanceMode, AllowRegistration: r.AllowRegistration, DefaultCurrency: r.DefaultCurrency, Timezone: r.Timezone, SocialLinks: []SocialLink{}}
	if rows, ok := r.SocialLinks.(bson.A); ok {
		for _, value := range rows {
			var label, url string
			switch m := value.(type) {
			case bson.M:
				label, _ = m["label"].(string)
				url, _ = m["url"].(string)
			case bson.D:
				for _, field := range m {
					if field.Key == "label" {
						label, _ = field.Value.(string)
					}
					if field.Key == "url" {
						url, _ = field.Value.(string)
					}
				}
			}
			if label != "" || url != "" {
				out.SocialLinks = append(out.SocialLinks, SocialLink{Label: label, URL: url})
			}
		}
	}
	if len(out.SocialLinks) == 0 && r.InstagramURL != nil {
		out.SocialLinks = append(out.SocialLinks, SocialLink{Label: "Instagram", URL: *r.InstagramURL})
	}
	return out
}
func validSettings(in SettingsInput) bool {
	if JSTrim(in.SiteName) == "" || JSStringLength(JSTrim(in.SiteName)) > 100 || (in.SiteDescription != nil && JSStringLength(*in.SiteDescription) > 500) || (in.ContactEmail != "" && !regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`).MatchString(in.ContactEmail)) || (in.ContactWhatsApp != "" && !regexp.MustCompile(`^\+?[1-9]\d{7,14}$`).MatchString(normalizeSettingsPhone(in.ContactWhatsApp))) || (in.BusinessHours != nil && JSStringLength(*in.BusinessHours) > 160) || len(in.SocialLinks) > 12 || in.DefaultCurrency != "IDR" || !containsString([]string{"Asia/Jakarta", "Asia/Makassar", "Asia/Jayapura"}, in.Timezone) {
		return false
	}
	for _, l := range in.SocialLinks {
		u, e := urlParseHTTPS(l.URL)
		if JSTrim(l.Label) == "" || JSStringLength(JSTrim(l.Label)) > 40 || JSStringLength(l.URL) > 255 || !u || e != nil {
			return false
		}
	}
	return true
}
func normalizeSettingsPhone(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || r == '\uFEFF' || r == '(' || r == ')' || r == '-' {
			return -1
		}
		return r
	}, value)
}
func urlParseHTTPS(raw string) (bool, error) {
	u, e := url.ParseRequestURI(raw)
	return e == nil && u.Scheme == "https" && u.Host != "", e
}
func parseJSDateOnly(value string) (time.Time, bool) {
	parts := regexp.MustCompile(`^(\d{4})-(\d{2})-(\d{2})$`).FindStringSubmatch(value)
	if len(parts) != 4 {
		return time.Time{}, false
	}
	year, yearErr := strconv.Atoi(parts[1])
	month, monthErr := strconv.Atoi(parts[2])
	day, dayErr := strconv.Atoi(parts[3])
	if yearErr != nil || monthErr != nil || dayErr != nil || month < 1 || month > 12 || day < 1 || day > 31 {
		return time.Time{}, false
	}
	return time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC), true
}
func trimPtr(p *string) *string {
	if p == nil {
		return nil
	}
	s := JSTrim(*p)
	if s == "" {
		return nil
	}
	return &s
}
func strPtr(s string) *string { return &s }

var _ = math.Round
var _ = sort.Strings
