package controller

import "testing"

func TestServiceCustomPriceRequiresExplicitNull(t *testing.T) {
	custom := true
	amount := 10.0
	base := ServiceInput{
		GameID:        stringPointer("0123456789abcdef01234567"),
		CategoryID:    stringPointer("0123456789abcdef01234568"),
		Name:          stringPointer("Leveling"),
		IsCustomPrice: &custom,
		EstimateText:  stringPointer("1-2 hari"),
	}

	if validServiceInput(base, true) {
		t.Fatal("custom price update without priceAmount accepted; source requires explicit null")
	}

	base.PriceSet = true
	base.PriceAmount = &amount
	if validServiceInput(base, true) {
		t.Fatal("custom price update with numeric priceAmount accepted")
	}

	base.PriceAmount = nil
	if !validServiceInput(base, true) {
		t.Fatal("custom price update with explicit null priceAmount rejected")
	}
}

func stringPointer(value string) *string { return &value }
