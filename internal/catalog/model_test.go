package catalog

import "testing"

func TestValidateCategoryRequest(t *testing.T) {
	description := "  Pilihan layanan  "
	category, err := ValidateCategoryRequest(CategoryRequest{Name: "  Rank ", Slug: " JOKI-RANK ", Description: &description, IsActive: true})
	if err != nil {
		t.Fatal(err)
	}
	if category.Name != "Rank" || category.Slug != "joki-rank" || category.Description != "Pilihan layanan" || !category.IsActive {
		t.Fatalf("unexpected category: %#v", category)
	}
	if _, err := ValidateCategoryRequest(CategoryRequest{Name: "Invalid", Slug: "not a slug"}); err != ErrInvalidCatalog {
		t.Fatalf("invalid category slug error=%v, want ErrInvalidCatalog", err)
	}
}

func TestValidateServiceRequestSupportsCustomAndPricedServices(t *testing.T) {
	priced := int64(25000)
	service, err := ValidateServiceRequest(ServiceRequest{Name: "  Rank Boost ", GameID: "game-id", CategoryID: "category-id", PriceAmount: &priced, EstimateText: "1 hari", IsActive: true})
	if err != nil || service.Name != "Rank Boost" || service.PriceAmount == nil || *service.PriceAmount != 25000 {
		t.Fatalf("priced service=%#v err=%v", service, err)
	}
	custom, err := ValidateServiceRequest(ServiceRequest{Name: "Custom", GameID: "game-id", CategoryID: "category-id", PriceAmount: &priced, IsCustomPrice: true})
	if err != nil || custom.PriceAmount != nil {
		t.Fatalf("custom service=%#v err=%v", custom, err)
	}
	zero := int64(0)
	if _, err := ValidateServiceRequest(ServiceRequest{Name: "Invalid", GameID: "game-id", CategoryID: "category-id", PriceAmount: &zero}); err != ErrInvalidCatalog {
		t.Fatalf("invalid price error=%v, want ErrInvalidCatalog", err)
	}
}
