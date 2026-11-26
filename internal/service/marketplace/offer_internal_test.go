package marketplace

import (
	"testing"
)

func TestFilterByProviderName(t *testing.T) {
	offers := []offer{
		{providerID: "a", providerName: "Acme Hosting", planID: "a-1"},
		{providerID: "a", providerName: "Acme Hosting", planID: "a-2"},
		{providerID: "b", providerName: " Budget Cloud ", planID: "b-1"},
		{providerID: "c", providerName: "Twin", planID: "c-1"},
		{providerID: "d", providerName: "twin", planID: "d-1"},
	}
	t.Run("case and spaces ignored", func(t *testing.T) {
		got, err := filterByProviderName(offers, "  acme HOSTING ")
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 || got[0].planID != "a-1" || got[1].planID != "a-2" {
			t.Fatalf("got %+v", got)
		}
		got, err = filterByProviderName(offers, "budget cloud")
		if err != nil || len(got) != 1 || got[0].providerID != "b" {
			t.Fatalf("got %+v, %v", got, err)
		}
	})
	t.Run("no match", func(t *testing.T) {
		got, err := filterByProviderName(offers, "Nobody")
		if err != nil || len(got) != 0 {
			t.Fatalf("got %+v, %v", got, err)
		}
	})
	t.Run("ambiguous", func(t *testing.T) {
		_, err := filterByProviderName(offers, " TWIN ")
		want := "more than one provider is named TWIN; give its provider_id or storefront_slug instead"
		if err == nil || err.Error() != want {
			t.Fatalf("got %v, want %q", err, want)
		}
	})
}
