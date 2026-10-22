package controller

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"example.com/learn-golang/internal/model"
	"go.mongodb.org/mongo-driver/v2/bson"
	"golang.org/x/text/cases"
	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

type CatalogPresenter struct {
	repository model.CatalogRepository
}

func NewCatalogPresenter(repository model.CatalogRepository) *CatalogPresenter {
	return &CatalogPresenter{repository: repository}
}

type PublicGame struct {
	ID             string               `json:"id"`
	Name           string               `json:"name"`
	Slug           string               `json:"slug"`
	ServiceSummary *string              `json:"serviceSummary"`
	ArtworkKey     *string              `json:"artworkKey"`
	ServiceCount   int                  `json:"serviceCount"`
	Categories     []PublicGameCategory `json:"categories"`
	IsPopular      bool                 `json:"isPopular"`
	SortOrder      int64                `json:"sortOrder"`
}

type PublicGameCategory struct {
	Name string `json:"name"`
	Slug string `json:"slug"`
}

type PublicGamesResult struct {
	Games      []PublicGame     `json:"games"`
	Total      int              `json:"total"`
	Limit      int              `json:"limit"`
	Offset     int              `json:"offset"`
	Categories []CategoryFilter `json:"-"`
}

type CategoryFilter struct {
	Slug  string `json:"slug"`
	Name  string `json:"name"`
	Count int    `json:"count"`
}

func (p *CatalogPresenter) ListGames(ctx context.Context, search string, limit, offset int, category, sortBy string) (PublicGamesResult, error) {
	games, err := p.repository.ListActiveGames(ctx)
	if err != nil {
		return PublicGamesResult{}, err
	}
	categories, err := p.repository.ListActiveCategories(ctx)
	if err != nil {
		return PublicGamesResult{}, err
	}
	services, err := p.repository.ListActiveServices(ctx, nil)
	if err != nil {
		return PublicGamesResult{}, err
	}

	categoryByID := make(map[bson.ObjectID]model.ServiceCategory, len(categories))
	for _, item := range categories {
		categoryByID[item.ID] = item
	}
	servicesByGame := make(map[bson.ObjectID][]model.GameService, len(games))
	for _, item := range services {
		servicesByGame[item.GameID] = append(servicesByGame[item.GameID], item)
	}

	lower := cases.Lower(language.Indonesian).String
	query := lower(strings.TrimFunc(search, func(r rune) bool { return unicode.IsSpace(r) || r == '\uFEFF' }))
	nameCollator := collate.New(language.Indonesian)
	matching := make([]PublicGame, 0, len(games))
	for _, game := range games {
		if query != "" && !strings.Contains(lower(game.Name), query) {
			continue
		}
		gameCategories := make([]PublicGameCategory, 0)
		seen := make(map[string]struct{})
		serviceCount := 0
		for _, service := range servicesByGame[game.ID] {
			category, found := categoryByID[service.CategoryID]
			if !found {
				continue
			}
			serviceCount++
			if _, found := seen[category.Slug]; found {
				continue
			}
			seen[category.Slug] = struct{}{}
			gameCategories = append(gameCategories, PublicGameCategory{Name: category.Name, Slug: category.Slug})
		}
		matching = append(matching, PublicGame{
			ID: game.ID.Hex(), Name: game.Name, Slug: game.Slug,
			ServiceSummary: game.ServiceSummary, ArtworkKey: game.ArtworkKey,
			ServiceCount: serviceCount, Categories: gameCategories,
			IsPopular: game.IsPopular, SortOrder: game.SortOrder,
		})
	}
	sort.SliceStable(matching, func(i, j int) bool {
		if sortBy == "NAME_ASC" {
			return nameCollator.CompareString(matching[i].Name, matching[j].Name) < 0
		}
		if matching[i].IsPopular != matching[j].IsPopular {
			return matching[i].IsPopular
		}
		if matching[i].SortOrder != matching[j].SortOrder {
			return matching[i].SortOrder < matching[j].SortOrder
		}
		return nameCollator.CompareString(matching[i].Name, matching[j].Name) < 0
	})

	categoryCounts := make(map[string]int)
	categoryNames := make(map[string]string)
	categoryOrder := make([]string, 0)
	for _, game := range matching {
		for _, item := range game.Categories {
			if _, exists := categoryCounts[item.Slug]; !exists {
				categoryOrder = append(categoryOrder, item.Slug)
			}
			categoryCounts[item.Slug]++
			categoryNames[item.Slug] = item.Name
		}
	}
	filters := make([]CategoryFilter, 0, len(categoryCounts))
	for _, slug := range categoryOrder {
		filters = append(filters, CategoryFilter{Slug: slug, Name: categoryNames[slug], Count: categoryCounts[slug]})
	}
	sort.SliceStable(filters, func(i, j int) bool { return nameCollator.CompareString(filters[i].Name, filters[j].Name) < 0 })

	if category != "" {
		filtered := matching[:0]
		for _, game := range matching {
			for _, item := range game.Categories {
				if item.Slug == category {
					filtered = append(filtered, game)
					break
				}
			}
		}
		matching = filtered
	}
	total := len(matching)
	if offset >= total {
		matching = []PublicGame{}
	} else {
		end := offset + limit
		if end > total {
			end = total
		}
		matching = matching[offset:end]
	}
	return PublicGamesResult{Games: matching, Total: total, Limit: limit, Offset: offset, Categories: filters}, nil
}

func (p *CatalogPresenter) ListPopularGames(ctx context.Context, limit, offset int) ([]PublicGame, int, error) {
	result, err := p.ListGames(ctx, "", 100, 0, "", "POPULAR")
	if err != nil {
		return nil, 0, err
	}
	popular := make([]PublicGame, 0)
	for _, game := range result.Games {
		if game.IsPopular {
			popular = append(popular, game)
		}
	}
	total := len(popular)
	if offset >= total {
		return []PublicGame{}, total, nil
	}
	end := offset + limit
	if end > total {
		end = total
	}
	return popular[offset:end], total, nil
}

type PublicService struct {
	ID          string                 `json:"id"`
	Name        string                 `json:"name"`
	Category    ServiceCategorySummary `json:"category"`
	Price       *float64               `json:"price"`
	CustomPrice bool                   `json:"customPrice"`
	Estimate    string                 `json:"estimate"`
}

type ServiceCategorySummary struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

type ServiceCategoryGroup struct {
	ID       string           `json:"id"`
	Name     string           `json:"name"`
	Slug     string           `json:"slug"`
	Services []GroupedService `json:"services"`
}

type GroupedService struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Price       *float64 `json:"price"`
	CustomPrice bool     `json:"customPrice"`
	Estimate    string   `json:"estimate"`
}

type GameSummary struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

type PublicGameServices struct {
	Game       GameSummary            `json:"game"`
	Categories []ServiceCategoryGroup `json:"categories"`
	Services   []PublicService        `json:"services"`
}

func priceValue(price *bson.Decimal128) (*float64, error) {
	if price == nil {
		return nil, nil
	}
	value, err := strconv.ParseFloat(price.String(), 64)
	if err != nil {
		return nil, fmt.Errorf("invalid service price: %w", err)
	}
	return &value, nil
}

func (p *CatalogPresenter) ListServicesByGame(ctx context.Context, slug string) (*PublicGameServices, error) {
	game, err := p.repository.FindActiveGameBySlug(ctx, slug)
	if err != nil || game == nil {
		return nil, err
	}
	categories, err := p.repository.ListActiveCategories(ctx)
	if err != nil {
		return nil, err
	}
	categoryByID := make(map[bson.ObjectID]model.ServiceCategory, len(categories))
	for _, category := range categories {
		categoryByID[category.ID] = category
	}
	services, err := p.repository.ListActiveServices(ctx, &game.ID)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(services, func(i, j int) bool { return services[i].Name < services[j].Name })
	result := &PublicGameServices{Game: GameSummary{ID: game.ID.Hex(), Name: game.Name, Slug: game.Slug}, Categories: []ServiceCategoryGroup{}, Services: []PublicService{}}
	groupIndex := make(map[bson.ObjectID]int)
	for _, service := range services {
		category, found := categoryByID[service.CategoryID]
		if !found {
			continue
		}
		price, err := priceValue(service.PriceAmount)
		if err != nil {
			return nil, err
		}
		customPrice := service.IsCustomPrice || price == nil
		result.Services = append(result.Services, PublicService{
			ID: service.ID.Hex(), Name: service.Name,
			Category: ServiceCategorySummary{ID: category.ID.Hex(), Name: category.Name, Slug: category.Slug},
			Price:    price, CustomPrice: customPrice, Estimate: service.EstimateText,
		})
		index, exists := groupIndex[category.ID]
		if !exists {
			index = len(result.Categories)
			groupIndex[category.ID] = index
			result.Categories = append(result.Categories, ServiceCategoryGroup{
				ID: category.ID.Hex(), Name: category.Name, Slug: category.Slug, Services: []GroupedService{},
			})
		}
		result.Categories[index].Services = append(result.Categories[index].Services, GroupedService{
			ID: service.ID.Hex(), Name: service.Name, Price: price, CustomPrice: customPrice, Estimate: service.EstimateText,
		})
	}
	return result, nil
}

type PublicCategory struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	Slug         string  `json:"slug"`
	Description  *string `json:"description"`
	ServiceCount int     `json:"serviceCount"`
}

func (p *CatalogPresenter) ListServiceCategories(ctx context.Context) ([]PublicCategory, error) {
	categories, err := p.repository.ListActiveCategories(ctx)
	if err != nil {
		return nil, err
	}
	games, err := p.repository.ListActiveGames(ctx)
	if err != nil {
		return nil, err
	}
	services, err := p.repository.ListActiveServices(ctx, nil)
	if err != nil {
		return nil, err
	}
	activeGames := make(map[bson.ObjectID]struct{}, len(games))
	for _, game := range games {
		activeGames[game.ID] = struct{}{}
	}
	counts := make(map[bson.ObjectID]int)
	for _, service := range services {
		if _, ok := activeGames[service.GameID]; ok {
			counts[service.CategoryID]++
		}
	}
	result := make([]PublicCategory, 0, len(categories))
	for _, category := range categories {
		if counts[category.ID] == 0 {
			continue
		}
		result = append(result, PublicCategory{ID: category.ID.Hex(), Name: category.Name, Slug: category.Slug, Description: category.Description, ServiceCount: counts[category.ID]})
	}
	categoryNames := collate.New(language.Indonesian)
	sort.SliceStable(result, func(i, j int) bool { return categoryNames.CompareString(result[i].Name, result[j].Name) < 0 })
	return result, nil
}

func (p *CatalogPresenter) PublicSettings(ctx context.Context) (map[string]any, error) {
	settings, err := p.repository.GetPublicSettings(ctx)
	if err != nil {
		return nil, err
	}
	if settings == nil {
		return map[string]any{
			"siteName": "JokiHub", "siteDescription": "", "contactEmail": "", "contactWhatsapp": "",
			"socialLinks": []any{}, "businessHours": nil,
		}, nil
	}
	links := []any{}
	if values, ok := settings.SocialLinks.(bson.A); ok {
		for _, value := range values {
			var label, url string
			var labelOK, urlOK bool
			switch link := value.(type) {
			case bson.M:
				label, labelOK = link["label"].(string)
				url, urlOK = link["url"].(string)
			case bson.D:
				for _, field := range link {
					if field.Key == "label" {
						label, labelOK = field.Value.(string)
					}
					if field.Key == "url" {
						url, urlOK = field.Value.(string)
					}
				}
			}
			if labelOK && urlOK {
				links = append(links, map[string]string{"label": label, "url": url})
			}
		}
	}
	if len(links) == 0 && settings.InstagramURL != nil && *settings.InstagramURL != "" {
		links = append(links, map[string]string{"label": "Instagram", "url": *settings.InstagramURL})
	}
	siteDescription := any("")
	if settings.SiteDescription != nil {
		siteDescription = *settings.SiteDescription
	}
	businessHours := any(nil)
	if settings.BusinessHours != nil {
		businessHours = *settings.BusinessHours
	}
	return map[string]any{
		"siteName": settings.SiteName, "siteDescription": siteDescription,
		"contactEmail": settings.ContactEmail, "contactWhatsapp": settings.ContactWhatsApp,
		"socialLinks": links, "businessHours": businessHours,
	}, nil
}
