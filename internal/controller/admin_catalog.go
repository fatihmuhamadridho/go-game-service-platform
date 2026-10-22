package controller

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"time"

	"example.com/learn-golang/internal/model"
	"go.mongodb.org/mongo-driver/v2/bson"
)

type AdminCatalogError struct {
	Code    string
	Message string
	Status  int
	Data    any
}

func (e *AdminCatalogError) Error() string { return e.Message }
func ToAdminCatalogError(err error, prefix string) *AdminCatalogError {
	var target *AdminCatalogError
	if errors.As(err, &target) {
		return target
	}
	return &AdminCatalogError{Code: prefix + "T03001", Message: "Data katalog sementara tidak tersedia.", Status: 500}
}

type AdminCatalogController struct {
	repo  model.AdminCatalogRepository
	games model.AdminGameRepository
}

func NewAdminCatalogController(repo model.AdminCatalogRepository, games model.AdminGameRepository) *AdminCatalogController {
	return &AdminCatalogController{repo: repo, games: games}
}

type CategoryInput struct {
	Name        *string
	Slug        *string
	Description model.OptionalString
	IsActive    *bool
}
type ServiceInput struct {
	GameID        *string
	CategoryID    *string
	Name          *string
	PriceAmount   *float64
	PriceSet      bool
	IsCustomPrice *bool
	EstimateText  *string
	IsActive      *bool
}
type AdminCategory struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Slug         string    `json:"slug"`
	Description  string    `json:"description"`
	ServiceCount int       `json:"serviceCount"`
	Active       bool      `json:"active"`
	UpdatedAt    time.Time `json:"updatedAt"`
}
type AdminService struct {
	ID         string    `json:"id"`
	GameID     string    `json:"gameId"`
	Game       string    `json:"game"`
	CategoryID string    `json:"categoryId"`
	Category   string    `json:"category"`
	Name       string    `json:"name"`
	Price      *float64  `json:"price"`
	Estimate   string    `json:"estimate"`
	Active     bool      `json:"active"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

var adminCategorySlug = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
var adminServicePrice = regexp.MustCompile(`^\d{1,16}(?:\.\d{1,2})?$`)

func (p *AdminCatalogController) ListCategories(ctx context.Context) ([]AdminCategory, error) {
	rows, err := p.repo.ListAllCategories(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]AdminCategory, 0, len(rows))
	for _, row := range rows {
		if row.DeletedAt != nil {
			continue
		}
		count, e := p.repo.CountServicesByCategory(ctx, row.ID, false)
		if e != nil {
			return nil, e
		}
		out = append(out, categoryDTO(row, count))
	}
	return out, nil
}
func (p *AdminCatalogController) GetCategory(ctx context.Context, raw string) (AdminCategory, error) {
	id, err := catalogObjectID(raw, "SCAB02001", "ID kategori tidak valid.")
	if err != nil {
		return AdminCategory{}, err
	}
	row, err := p.repo.FindCategoryByID(ctx, id)
	if err != nil {
		return AdminCategory{}, err
	}
	if row == nil || row.DeletedAt != nil {
		return AdminCategory{}, catalogFailure("SCAB02002", "Kategori tidak ditemukan.", 404, nil)
	}
	count, err := p.repo.CountServicesByCategory(ctx, id, false)
	if err != nil {
		return AdminCategory{}, err
	}
	return categoryDTO(*row, count), nil
}
func (p *AdminCatalogController) CreateCategory(ctx context.Context, input CategoryInput, audit model.ActivityContext) (AdminCategory, error) {
	if input.Name == nil || input.Slug == nil || !validCategory(*input.Name, *input.Slug, input.Description) || (input.IsActive != nil && *input.IsActive != true && *input.IsActive != false) {
		return AdminCategory{}, catalogFailure("SCAB02001", "Data kategori tidak valid.", 400, nil)
	}
	name := JSTrim(*input.Name)
	exists, err := p.repo.CategoryNameOrSlugExists(ctx, name, *input.Slug, bson.NilObjectID)
	if err != nil {
		return AdminCategory{}, err
	}
	if exists {
		return AdminCategory{}, catalogFailure("SCAB02004", "Nama atau slug kategori sudah digunakan.", 409, nil)
	}
	now := time.Now().UTC()
	row := model.ServiceCategory{ID: bson.NewObjectID(), Name: name, Slug: *input.Slug, Description: optionalString(input.Description), IsActive: input.IsActive == nil || *input.IsActive, CreatedAt: now, UpdatedAt: now}
	if err = p.repo.CreateCategory(ctx, row); err != nil {
		return AdminCategory{}, err
	}
	_ = p.repo.RecordCatalogActivity(ctx, audit, "SERVICE_CATEGORY_CREATED", "SERVICE_CATEGORY", row.ID, categoryInputAuditFields(input))
	return categoryDTO(row, 0), nil
}
func (p *AdminCatalogController) UpdateCategory(ctx context.Context, raw string, input CategoryInput, audit model.ActivityContext) (AdminCategory, error) {
	id, err := catalogObjectID(raw, "SCAB02001", "ID kategori tidak valid.")
	if err != nil {
		return AdminCategory{}, err
	}
	current, err := p.repo.FindCategoryByID(ctx, id)
	if err != nil {
		return AdminCategory{}, err
	}
	if current == nil || current.DeletedAt != nil {
		return AdminCategory{}, catalogFailure("SCAB02002", "Kategori tidak ditemukan.", 404, nil)
	}
	if input.Name == nil && input.Slug == nil && !input.Description.Set && input.IsActive == nil {
		return AdminCategory{}, catalogFailure("SCAB02001", "Data kategori tidak valid.", 400, nil)
	}
	name, slug := current.Name, current.Slug
	if input.Name != nil {
		name = *input.Name
	}
	if input.Slug != nil {
		slug = *input.Slug
	}
	if !validCategory(name, slug, input.Description) {
		return AdminCategory{}, catalogFailure("SCAB02001", "Data kategori tidak valid.", 400, nil)
	}
	exists, err := p.repo.CategoryNameOrSlugExists(ctx, JSTrim(name), slug, id)
	if err != nil {
		return AdminCategory{}, err
	}
	if exists {
		return AdminCategory{}, catalogFailure("SCAB02004", "Nama atau slug kategori sudah digunakan.", 409, nil)
	}
	patch := map[string]any{}
	if input.Name != nil {
		patch["name"] = JSTrim(name)
	}
	if input.Slug != nil {
		patch["slug"] = slug
	}
	if input.Description.Set {
		patch["description"] = optionalString(input.Description)
	}
	if input.IsActive != nil {
		patch["is_active"] = *input.IsActive
	}
	if err = p.repo.UpdateCategory(ctx, id, patch); err != nil {
		return AdminCategory{}, err
	}
	_ = p.repo.RecordCatalogActivity(ctx, audit, "SERVICE_CATEGORY_UPDATED", "SERVICE_CATEGORY", id, patch)
	return p.GetCategory(ctx, raw)
}
func (p *AdminCatalogController) DeleteCategory(ctx context.Context, raw string, audit model.ActivityContext) (map[string]any, error) {
	id, err := catalogObjectID(raw, "SCAB02001", "ID kategori tidak valid.")
	if err != nil {
		return nil, err
	}
	row, err := p.repo.FindCategoryByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if row == nil || row.DeletedAt != nil {
		return nil, catalogFailure("SCAB02002", "Kategori tidak ditemukan.", 404, nil)
	}
	count, err := p.repo.CountServicesByCategory(ctx, id, true)
	if err != nil {
		return nil, err
	}
	if count > 0 {
		return nil, catalogFailure("SCAB02003", "Kategori masih digunakan layanan.", 409, map[string]int{"linkedServiceCount": count})
	}
	now := time.Now().UTC()
	if err = p.repo.SoftDeleteCategory(ctx, id, now); err != nil {
		return nil, err
	}
	_ = p.repo.RecordCatalogActivity(ctx, audit, "SERVICE_CATEGORY_DELETED", "SERVICE_CATEGORY", id, map[string]any{"deleted_at": now.Format(time.RFC3339Nano)})
	return map[string]any{"categoryId": raw, "deletedAt": now.Format(time.RFC3339Nano)}, nil
}

func (p *AdminCatalogController) ListServices(ctx context.Context) (map[string]any, error) {
	rows, err := p.repo.ListAllServices(ctx)
	if err != nil {
		return nil, err
	}
	games, err := p.games.ListAdminGames(ctx)
	if err != nil {
		return nil, err
	}
	if allGamesRepository, ok := p.games.(interface {
		ListAllGames(context.Context) ([]model.GameCatalog, error)
	}); ok {
		games, err = allGamesRepository.ListAllGames(ctx)
		if err != nil {
			return nil, err
		}
	}
	categories, err := p.repo.ListAllCategories(ctx)
	if err != nil {
		return nil, err
	}
	gameByID := map[bson.ObjectID]model.GameCatalog{}
	for _, g := range games {
		gameByID[g.ID] = g
	}
	catByID := map[bson.ObjectID]model.ServiceCategory{}
	for _, c := range categories {
		catByID[c.ID] = c
	}
	out := make([]AdminService, 0, len(rows))
	for _, row := range rows {
		if row.DeletedAt != nil {
			continue
		}
		g, c := gameByID[row.GameID], catByID[row.CategoryID]
		out = append(out, serviceDTO(row, g.Name, c.Name))
	}
	gameOptions := make([]map[string]string, 0, len(games))
	for _, g := range games {
		if g.DeletedAt == nil {
			gameOptions = append(gameOptions, map[string]string{"id": g.ID.Hex(), "name": g.Name})
		}
	}
	categoryOptions := make([]map[string]string, 0, len(categories))
	for _, c := range categories {
		if c.DeletedAt == nil {
			categoryOptions = append(categoryOptions, map[string]string{"id": c.ID.Hex(), "name": c.Name})
		}
	}
	return map[string]any{"services": out, "games": gameOptions, "categories": categoryOptions}, nil
}
func (p *AdminCatalogController) GetService(ctx context.Context, raw string) (AdminService, error) {
	id, err := catalogObjectID(raw, "SRVB02001", "ID layanan tidak valid.")
	if err != nil {
		return AdminService{}, err
	}
	row, err := p.repo.FindServiceByID(ctx, id)
	if err != nil {
		return AdminService{}, err
	}
	if row == nil || row.DeletedAt != nil {
		return AdminService{}, catalogFailure("SRVB02003", "Layanan tidak ditemukan.", 404, nil)
	}
	g, err := p.games.FindGameByID(ctx, row.GameID)
	if err != nil {
		return AdminService{}, err
	}
	c, err := p.repo.FindCategoryByID(ctx, row.CategoryID)
	if err != nil {
		return AdminService{}, err
	}
	return serviceDTO(*row, gameName(g), categoryName(c)), nil
}
func (p *AdminCatalogController) CreateService(ctx context.Context, input ServiceInput, audit model.ActivityContext) (AdminService, error) {
	if !validServiceInput(input, false) {
		return AdminService{}, catalogFailure("SRVB02001", "Data layanan tidak valid.", 400, nil)
	}
	row, err := p.makeService(ctx, input, nil)
	if err != nil {
		return AdminService{}, err
	}
	now := time.Now().UTC()
	row.ID = bson.NewObjectID()
	row.CreatedAt = now
	row.UpdatedAt = now
	if err = p.repo.CreateService(ctx, row); err != nil {
		return AdminService{}, err
	}
	_ = p.repo.RecordCatalogActivity(ctx, audit, "SERVICE_CREATED", "SERVICE", row.ID, serviceInputAuditFields(input))
	return p.GetService(ctx, row.ID.Hex())
}
func (p *AdminCatalogController) UpdateService(ctx context.Context, raw string, input ServiceInput, audit model.ActivityContext) (AdminService, error) {
	id, err := catalogObjectID(raw, "SRVB02001", "ID layanan tidak valid.")
	if err != nil {
		return AdminService{}, err
	}
	current, err := p.repo.FindServiceByID(ctx, id)
	if err != nil {
		return AdminService{}, err
	}
	if current == nil || current.DeletedAt != nil {
		return AdminService{}, catalogFailure("SRVB02003", "Layanan tidak ditemukan.", 404, nil)
	}
	if !validServiceInput(input, true) {
		return AdminService{}, catalogFailure("SRVB02001", "Data layanan tidak valid.", 400, nil)
	}
	merged := ServiceInput{GameID: stringPtr(current.GameID.Hex()), CategoryID: stringPtr(current.CategoryID.Hex()), Name: stringPtr(current.Name), IsCustomPrice: boolPtr(current.IsCustomPrice), EstimateText: stringPtr(current.EstimateText), IsActive: boolPtr(current.IsActive), PriceSet: true}
	if current.PriceAmount != nil {
		value, _ := strconv.ParseFloat(current.PriceAmount.String(), 64)
		merged.PriceAmount = &value
	}
	mergeServiceInput(&merged, input)
	row, err := p.makeService(ctx, merged, current)
	if err != nil {
		return AdminService{}, err
	}
	patch := map[string]any{"game_id": row.GameID, "category_id": row.CategoryID, "name": row.Name, "price_amount": row.PriceAmount, "is_custom_price": row.IsCustomPrice, "estimate_text": row.EstimateText, "is_active": row.IsActive}
	if err = p.repo.UpdateService(ctx, id, patch); err != nil {
		return AdminService{}, err
	}
	_ = p.repo.RecordCatalogActivity(ctx, audit, "SERVICE_UPDATED", "SERVICE", id, patch)
	return p.GetService(ctx, raw)
}
func (p *AdminCatalogController) DeleteService(ctx context.Context, raw string, audit model.ActivityContext) (map[string]any, error) {
	id, err := catalogObjectID(raw, "SRVB02001", "ID layanan tidak valid.")
	if err != nil {
		return nil, err
	}
	row, err := p.repo.FindServiceByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if row == nil || row.DeletedAt != nil {
		return nil, catalogFailure("SRVB02003", "Layanan tidak ditemukan.", 404, nil)
	}
	now := time.Now().UTC()
	if err = p.repo.SoftDeleteService(ctx, id, now); err != nil {
		return nil, err
	}
	_ = p.repo.RecordCatalogActivity(ctx, audit, "SERVICE_DELETED", "SERVICE", id, map[string]any{"deleted_at": now.Format(time.RFC3339Nano)})
	return map[string]any{"serviceId": raw, "deletedAt": now.Format(time.RFC3339Nano)}, nil
}

func (p *AdminCatalogController) makeService(ctx context.Context, input ServiceInput, old *model.GameService) (model.GameService, error) {
	gid, e := bson.ObjectIDFromHex(valueString(input.GameID))
	if e != nil {
		return model.GameService{}, catalogFailure("SRVB02001", "Referensi game atau kategori tidak valid.", 400, nil)
	}
	cid, e := bson.ObjectIDFromHex(valueString(input.CategoryID))
	if e != nil {
		return model.GameService{}, catalogFailure("SRVB02001", "Referensi game atau kategori tidak valid.", 400, nil)
	}
	game, e := p.games.FindGameByID(ctx, gid)
	if e != nil {
		return model.GameService{}, e
	}
	category, e := p.repo.FindCategoryByID(ctx, cid)
	if e != nil {
		return model.GameService{}, e
	}
	active := input.IsActive == nil || *input.IsActive
	if game == nil || game.DeletedAt != nil || category == nil || category.DeletedAt != nil || (active && (!game.IsActive || !category.IsActive)) {
		return model.GameService{}, catalogFailure("SRVB02002", "Game atau kategori tidak tersedia untuk layanan ini.", 400, nil)
	}
	var price *bson.Decimal128
	if input.IsCustomPrice != nil && *input.IsCustomPrice {
		price = nil
	} else if input.PriceAmount != nil {
		v, e := bson.ParseDecimal128(strconv.FormatFloat(*input.PriceAmount, 'f', -1, 64))
		if e != nil {
			return model.GameService{}, catalogFailure("SRVB02001", "Data layanan tidak valid.", 400, nil)
		}
		price = &v
	}
	row := model.GameService{GameID: gid, CategoryID: cid, Name: JSTrim(valueString(input.Name)), PriceAmount: price, IsCustomPrice: input.IsCustomPrice != nil && *input.IsCustomPrice, EstimateText: JSTrim(valueString(input.EstimateText)), IsActive: active}
	if old != nil {
		row.ID = old.ID
		row.CreatedAt = old.CreatedAt
	}
	return row, nil
}

func categoryDTO(row model.ServiceCategory, count int) AdminCategory {
	description := ""
	if row.Description != nil {
		description = *row.Description
	}
	return AdminCategory{ID: row.ID.Hex(), Name: row.Name, Slug: row.Slug, Description: description, ServiceCount: count, Active: row.IsActive, UpdatedAt: row.UpdatedAt}
}
func serviceDTO(row model.GameService, game, category string) AdminService {
	var price *float64
	if !row.IsCustomPrice {
		v := float64(0)
		if row.PriceAmount != nil {
			v, _ = strconv.ParseFloat(row.PriceAmount.String(), 64)
		}
		price = &v
	}
	return AdminService{ID: row.ID.Hex(), GameID: row.GameID.Hex(), Game: game, CategoryID: row.CategoryID.Hex(), Category: category, Name: row.Name, Price: price, Estimate: row.EstimateText, Active: row.IsActive, UpdatedAt: row.UpdatedAt}
}
func validCategory(name, slug string, description model.OptionalString) bool {
	return JSTrim(name) != "" && JSStringLength(JSTrim(name)) <= 100 && adminCategorySlug.MatchString(slug) && len(slug) <= 100 && (!description.Set || description.Value == nil || JSStringLength(JSTrim(*description.Value)) <= 500)
}
func validServiceInput(in ServiceInput, partial bool) bool {
	if partial && in.GameID == nil && in.CategoryID == nil && in.Name == nil && !in.PriceSet && in.IsCustomPrice == nil && in.EstimateText == nil && in.IsActive == nil {
		return false
	}
	if !partial && (in.GameID == nil || in.CategoryID == nil || in.Name == nil || in.IsCustomPrice == nil || in.EstimateText == nil) {
		return false
	}
	if in.GameID != nil && (JSTrim(*in.GameID) == "" || len(*in.GameID) > 64) {
		return false
	}
	if in.CategoryID != nil && (JSTrim(*in.CategoryID) == "" || len(*in.CategoryID) > 64) {
		return false
	}
	if in.Name != nil && (JSTrim(*in.Name) == "" || JSStringLength(JSTrim(*in.Name)) > 150) {
		return false
	}
	if in.EstimateText != nil && (JSTrim(*in.EstimateText) == "" || JSStringLength(JSTrim(*in.EstimateText)) > 100) {
		return false
	}
	if in.IsCustomPrice != nil && *in.IsCustomPrice && (!in.PriceSet || in.PriceAmount != nil) {
		return false
	}
	if in.IsCustomPrice != nil && !*in.IsCustomPrice && (!in.PriceSet || in.PriceAmount == nil) {
		return false
	}
	if in.PriceAmount != nil && (*in.PriceAmount < 0 || !adminServicePrice.MatchString(strconv.FormatFloat(*in.PriceAmount, 'f', -1, 64))) {
		return false
	}
	return true
}
func mergeServiceInput(dst *ServiceInput, src ServiceInput) {
	if src.GameID != nil {
		dst.GameID = src.GameID
	}
	if src.CategoryID != nil {
		dst.CategoryID = src.CategoryID
	}
	if src.Name != nil {
		dst.Name = src.Name
	}
	if src.PriceSet {
		dst.PriceSet = true
		dst.PriceAmount = src.PriceAmount
	}
	if src.IsCustomPrice != nil {
		dst.IsCustomPrice = src.IsCustomPrice
	}
	if src.EstimateText != nil {
		dst.EstimateText = src.EstimateText
	}
	if src.IsActive != nil {
		dst.IsActive = src.IsActive
	}
}
func categoryInputAuditFields(input CategoryInput) map[string]any {
	fields := map[string]any{}
	if input.Name != nil {
		fields["name"] = *input.Name
	}
	if input.Slug != nil {
		fields["slug"] = *input.Slug
	}
	if input.Description.Set {
		fields["description"] = input.Description.Value
	}
	if input.IsActive != nil {
		fields["isActive"] = *input.IsActive
	}
	return fields
}
func serviceInputAuditFields(input ServiceInput) map[string]any {
	fields := map[string]any{}
	if input.GameID != nil {
		fields["gameId"] = *input.GameID
	}
	if input.CategoryID != nil {
		fields["categoryId"] = *input.CategoryID
	}
	if input.Name != nil {
		fields["name"] = *input.Name
	}
	if input.PriceSet {
		fields["priceAmount"] = input.PriceAmount
	}
	if input.IsCustomPrice != nil {
		fields["isCustomPrice"] = *input.IsCustomPrice
	}
	if input.EstimateText != nil {
		fields["estimateText"] = *input.EstimateText
	}
	if input.IsActive != nil {
		fields["isActive"] = *input.IsActive
	}
	return fields
}
func optionalString(v model.OptionalString) *string {
	if !v.Set || v.Value == nil {
		return nil
	}
	s := JSTrim(*v.Value)
	if s == "" {
		return nil
	}
	return &s
}
func catalogObjectID(raw, code, msg string) (bson.ObjectID, error) {
	id, e := bson.ObjectIDFromHex(raw)
	if e != nil {
		return bson.NilObjectID, catalogFailure(code, msg, 400, nil)
	}
	return id, nil
}
func catalogFailure(code, msg string, status int, data any) error {
	return &AdminCatalogError{Code: code, Message: msg, Status: status, Data: data}
}
func valueString(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
func boolPtr(v bool) *bool       { return &v }
func stringPtr(v string) *string { return &v }
func gameName(v *model.GameCatalog) string {
	if v == nil {
		return ""
	}
	return v.Name
}
func categoryName(v *model.ServiceCategory) string {
	if v == nil {
		return ""
	}
	return v.Name
}
