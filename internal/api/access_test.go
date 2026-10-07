package api

import (
	"testing"

	"vibrance/internal/auth"
)

// What each operation asks of a request is the column "Accesso" of the
// table of DESIGN.md §8.3, row by row. Access derives it from the
// specification (`security: []`) and from the path (/admin/): an operation
// that got the wrong one, or none, fails here.
func TestAccessIsTheOneOfTheDesign(t *testing.T) {
	access := Access(loadSpec(t))
	if len(access) != len(designOperations) {
		t.Errorf("Access knows %d routes, the design has %d operations", len(access), len(designOperations))
	}
	want := map[string]auth.Access{"public": auth.Public, "user": auth.Authenticated, "admin": auth.AdminOnly}
	counts := map[auth.Access]int{}
	for _, d := range designOperations {
		route := d.method + " " + BasePath + d.path
		got, ok := access[route]
		if !ok {
			t.Errorf("%s (%s): no access rule", d.id, route)
			continue
		}
		if got != want[d.access] {
			t.Errorf("%s (%s): access %d, want %d (%s)", d.id, route, got, want[d.access], d.access)
		}
		counts[got]++
	}
	// The table itself, with the operations of the erratum W1-W6: three
	// public operations, eight for admins.
	if counts[auth.Public] != 3 || counts[auth.AdminOnly] != 8 || counts[auth.Authenticated] != 35 {
		t.Errorf("%d public, %d for admins, %d for users; want 3, 8 and 35", counts[auth.Public], counts[auth.AdminOnly], counts[auth.Authenticated])
	}
}

// An operation under /admin/ is for admins even if the specification, by
// mistake, declared it public: the path decides first.
func TestAccessOfAnAdminPathMarkedPublic(t *testing.T) {
	doc := loadSpec(t)
	op := doc.Paths.Find("/admin/users").Get
	none := doc.Paths.Find("/server").Get.Security
	if none == nil || len(*none) != 0 {
		t.Fatal("GET /server is not public in the specification")
	}
	op.Security = none
	if got := Access(doc)["GET "+BasePath+"/admin/users"]; got != auth.AdminOnly {
		t.Fatalf("access %d, want AdminOnly", got)
	}
}
