package httpapi

import (
	"net/http"
	"testing"
)

func TestLegacySyncRoutesAreAdminOnly(t *testing.T) {
	admin := (&App{Config: Config{Surface: "admin"}}).Routes()
	public := (&App{Config: Config{Surface: "public"}}).Routes()
	const batches = "/api/v1/admin/legacy-sync/batches"
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, batches},
		{http.MethodPost, batches},
		{http.MethodGet, batches + "/one"},
		{http.MethodPost, batches + "/one/apply"},
		{http.MethodPost, batches + "/one/rollback"},
		{http.MethodPut, "/api/v1/admin/legacy-sync/mode"},
	} {
		statusIs(t, surfaceRequest(t, public, route.method, route.path, "", nil), http.StatusNotFound)
		statusIs(t, surfaceRequest(t, admin, route.method, route.path, "", nil), http.StatusUnauthorized)
	}
}
