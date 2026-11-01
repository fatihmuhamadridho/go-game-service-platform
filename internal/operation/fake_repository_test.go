package operation

import (
	"context"
	"sync"

	"go.mongodb.org/mongo-driver/v2/bson"
)

type fakeRepository struct {
	mu          sync.Mutex
	settings    map[string]any
	records     map[string]map[string]map[string]any
	orders      map[string]map[string]any
	idempotency map[string]map[string]any
}

func newFakeRepository() *fakeRepository {
	return &fakeRepository{settings: DefaultSettings(), records: map[string]map[string]map[string]any{}, orders: map[string]map[string]any{}, idempotency: map[string]map[string]any{}}
}

func (r *fakeRepository) GetSettings(context.Context) (map[string]any, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return cloneTestMap(r.settings), nil
}
func (r *fakeRepository) SaveSettings(_ context.Context, value map[string]any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.settings = cloneTestMap(value)
	return nil
}
func (r *fakeRepository) List(_ context.Context, bucket string) ([]map[string]any, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	items := []map[string]any{}
	for _, row := range r.records[bucket] {
		items = append(items, cloneTestMap(row))
	}
	return items, nil
}
func (r *fakeRepository) Get(_ context.Context, bucket, id string) (map[string]any, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return cloneTestMap(r.records[bucket][id]), nil
}
func (r *fakeRepository) Put(_ context.Context, bucket, id string, row map[string]any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.records[bucket] == nil {
		r.records[bucket] = map[string]map[string]any{}
	}
	r.records[bucket][id] = cloneTestMap(row)
	return nil
}
func (r *fakeRepository) Delete(_ context.Context, bucket, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.records[bucket], id)
	return nil
}
func (r *fakeRepository) ListOrders(context.Context) ([]map[string]any, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	items := []map[string]any{}
	for _, entry := range r.orders {
		items = append(items, cloneEntry(entry))
	}
	return items, nil
}
func (r *fakeRepository) FindOrder(_ context.Context, filter bson.D) (map[string]any, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, entry := range r.orders {
		if matchesOrderFilter(entry, filter) {
			return cloneEntry(entry), nil
		}
	}
	return nil, nil
}
func (r *fakeRepository) CreateOrder(_ context.Context, entry map[string]any, key, hash string) (map[string]any, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if key != "" {
		if previous := r.idempotency[key]; previous != nil {
			if previous["hash"] != hash {
				return nil, false, ErrIdempotencyConflict
			}
			return cloneEntry(previous["entry"].(map[string]any)), true, nil
		}
	}
	order := entry["order"].(map[string]any)
	id := order["id"].(string)
	r.orders[id] = cloneEntry(entry)
	if key != "" {
		r.idempotency[key] = map[string]any{"hash": hash, "entry": cloneEntry(entry)}
	}
	return nil, false, nil
}
func (r *fakeRepository) TransitionOrder(_ context.Context, entry map[string]any, paymentStatus, orderStatus string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	incoming := entry["order"].(map[string]any)
	stored := r.orders[incoming["id"].(string)]
	if stored == nil {
		return false, nil
	}
	current := stored["order"].(map[string]any)
	if paymentStatus != "" && current["paymentStatus"] != paymentStatus {
		return false, nil
	}
	if orderStatus != "" && current["orderStatus"] != orderStatus {
		return false, nil
	}
	r.orders[incoming["id"].(string)] = cloneEntry(entry)
	return true, nil
}
func (r *fakeRepository) ClearPrimaryBankAccounts(context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, row := range r.records["bankAccounts"] {
		row["isPrimary"] = false
	}
	return nil
}
func (r *fakeRepository) AddOrder(entry map[string]any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	order := entry["order"].(map[string]any)
	r.orders[order["id"].(string)] = cloneEntry(entry)
}
func (r *fakeRepository) OrderCount() int { r.mu.Lock(); defer r.mu.Unlock(); return len(r.orders) }

func matchesOrderFilter(entry map[string]any, filter bson.D) bool {
	order := entry["order"].(map[string]any)
	for _, field := range filter {
		key := field.Key
		if len(key) > 6 && key[:6] == "order." {
			key = key[6:]
		}
		if order[key] != field.Value {
			return false
		}
	}
	return true
}
func cloneEntry(entry map[string]any) map[string]any {
	if entry == nil {
		return nil
	}
	result := map[string]any{}
	for key, value := range entry {
		if row, ok := value.(map[string]any); ok {
			result[key] = cloneTestMap(row)
		} else {
			result[key] = value
		}
	}
	return result
}
func cloneTestMap(row map[string]any) map[string]any {
	if row == nil {
		return nil
	}
	result := map[string]any{}
	for key, value := range row {
		if nested, ok := value.(map[string]any); ok {
			result[key] = cloneTestMap(nested)
		} else {
			result[key] = value
		}
	}
	return result
}

var _ Repository = (*fakeRepository)(nil)
