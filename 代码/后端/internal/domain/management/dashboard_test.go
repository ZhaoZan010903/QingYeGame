package management

import "testing"

func TestDashboardQueryBoundaries(t *testing.T) {
	in, err := DecodeDashboardQuery("", "")
	if err != nil || in.Days != 7 || in.RankBy != "views" {
		t.Fatal("invalid dashboard defaults", in, err)
	}
	for _, days := range []string{"7", "30"} {
		for _, rank := range []string{"views", "favorites", "consultations"} {
			if _, err := DecodeDashboardQuery(days, rank); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, days := range []string{"0", "1", "31", "365", "-7", "07", "+7", "7.0", " 7", "7 ", "invalid"} {
		if _, err := DecodeDashboardQuery(days, "views"); err == nil {
			t.Fatal("accepted invalid dashboard range", days)
		}
	}
	for _, rank := range []string{"price", "newest", "Views", " views", "views DESC"} {
		if _, err := DecodeDashboardQuery("7", rank); err == nil {
			t.Fatal("accepted invalid dashboard ranking", rank)
		}
	}
}
