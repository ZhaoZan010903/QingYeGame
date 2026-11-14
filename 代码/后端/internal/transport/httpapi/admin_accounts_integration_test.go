package httpapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"golang.org/x/crypto/bcrypt"
	identityapp "qingye/backend/internal/application/identity"
	domain "qingye/backend/internal/domain/adminaccounts"
	"qingye/backend/internal/domain/chat"
	"qingye/backend/internal/domain/fault"
	"qingye/backend/internal/domain/management"
	"qingye/backend/internal/infrastructure/postgres"
	"strings"
	"sync"
	"testing"
	"time"
)

func adminAccountFixture(t *testing.T) *chatFixture {
	t.Helper()
	f := newChatFixture(t)
	f.admin.Config.ProductCredentialKey = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{37}, 32))
	return f
}
func adminAccountErrorCode(t *testing.T, err error, code string) {
	t.Helper()
	e, ok := fault.As(err)
	if !ok || e.Code != code {
		t.Fatalf("wanted %s got %v", code, err)
	}
}
func TestAdministratorAccountsCRUDVersionsPrivacyAndReplay(t *testing.T) {
	f := adminAccountFixture(t)
	ctx := context.Background()
	path := "/api/v1/admin/administrators"
	input := map[string]any{"login": "operator.one", "nickname": "运营一号", "password": "test-password-1234", "request_id": "create-admin-1"}
	created := memberData(t, f.call(t, f.first, "POST", path, input, 200))
	id := created["id"].(string)
	if created["version"] != float64(1) || created["login"] != "operator.one" || created["avatar_id"] != "" {
		t.Fatal("administrator DTO mismatch", created)
	}
	replay := memberData(t, f.call(t, f.first, "POST", path, input, 200))
	if replay["id"] != id {
		t.Fatal("create replay duplicated account")
	}
	input["nickname"] = "changed"
	f.call(t, f.first, "POST", path, input, 409)
	input["request_id"] = "create-admin-2"
	f.call(t, f.first, "POST", path, input, 409)
	var hash string
	if err := f.a.DB.QueryRow(ctx, `SELECT password_hash FROM users WHERE id=$1`, id).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if cost, err := bcrypt.Cost([]byte(hash)); err != nil || cost != 12 {
		t.Fatal("new admin password cost", cost, err)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte("test-password-1234")); err != nil {
		t.Fatal("new password unusable")
	}
	w := f.call(t, f.first, "GET", path+"?q=operator&limit=1", nil, 200)
	if w.Header().Get("Cache-Control") != "no-store" || strings.Contains(w.Body.String(), "password") || strings.Contains(w.Body.String(), hash) {
		t.Fatal("sensitive administrator projection")
	}
	var list struct {
		Data  []map[string]any
		Total int
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil || list.Total != 1 || len(list.Data) != 1 {
		t.Fatal("search pagination", err)
	}
	updated := memberData(t, f.call(t, f.first, "PATCH", path+"/"+id, map[string]any{"expected_version": 1, "nickname": " 新名字 "}, 200))
	if updated["nickname"] != "新名字" || updated["version"] != float64(2) {
		t.Fatal("profile update", updated)
	}
	f.call(t, f.first, "PATCH", path+"/"+id, map[string]any{"expected_version": 1, "enabled": false}, 409)
	f.call(t, f.first, "PATCH", path+"/"+f.first, map[string]any{"expected_version": 1, "enabled": false}, 422)
	f.call(t, f.first, "PATCH", "/api/v1/admin/users/"+id, map[string]any{"enabled": false}, 409)
	f.call(t, f.first, "PATCH", "/api/v1/admin/chat/profile", map[string]any{"avatar_id": ""}, 422)
	f.call(t, f.first, "PATCH", path+"/"+f.member, map[string]any{"expected_version": 1, "nickname": "oops"}, 404)
	if _, err := f.a.DB.Exec(ctx, `UPDATE users SET phone='19900002222' WHERE id=$1`, f.member); err != nil {
		t.Fatal(err)
	}
	users := f.call(t, f.first, "GET", "/api/v1/admin/users?role=member&q=19900002222", nil, 200)
	if !strings.Contains(users.Body.String(), `"phone":"19900002222"`) || strings.Contains(users.Body.String(), `"role":"admin"`) {
		t.Fatal("member full-phone scope")
	}
	me := f.call(t, f.member, "GET", "/api/v1/me", nil, 200)
	if strings.Contains(me.Body.String(), "19900002222") || !strings.Contains(me.Body.String(), "199****2222") {
		t.Fatal("public phone masking regressed")
	}
	f.call(t, f.first, "GET", "/api/v1/admin/users?role=admin", nil, 422)
	statusIs(t, surfaceRequest(t, f.pub, "GET", path, "", nil, f.cookies[f.first]), 404)
	statusIs(t, surfaceRequest(t, f.adm, "GET", path, "", nil, f.cookies[f.member]), 401)
	statusIs(t, surfaceRequest(t, f.adm, "POST", path, "http://foreign.test", input, f.cookies[f.first]), 403)
	var requestText string
	if err := f.a.DB.QueryRow(ctx, `SELECT string_agg(request_hash||response::text,'') FROM admin_account_requests`).Scan(&requestText); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(requestText, "test-password") || strings.Contains(requestText, hash) {
		t.Fatal("request replay retained secret")
	}
}

func TestAdministratorAvatarLibraryAndImmediateName(t *testing.T) {
	f := adminAccountFixture(t)
	ctx := context.Background()
	path := "/api/v1/admin/administrators/" + f.second
	mid := f.image(t, f.first, "avatar", "")
	f.call(t, f.first, "PATCH", path, map[string]any{"expected_version": 1, "avatar_id": mid}, 422)
	item := memberData(t, f.call(t, f.first, "POST", "/api/v1/admin/avatar-library", map[string]any{"name": "管理员头像", "media_id": mid, "sort_order": 0, "enabled": true}, 200))
	f.call(t, f.first, "PATCH", path, map[string]any{"expected_version": 1, "avatar_id": mid, "nickname": "即时生效"}, 200)
	if !strings.Contains(f.call(t, f.second, "GET", "/api/v1/admin/chat/profile", nil, 200).Body.String(), mid) {
		t.Fatal("administrator avatar not visible")
	}
	f.call(t, f.first, "PATCH", "/api/v1/admin/avatar-library/"+item["id"].(string), map[string]any{"name": "已停用", "sort_order": 0, "enabled": false, "version": item["version"]}, 200)
	f.call(t, f.first, "PATCH", path, map[string]any{"expected_version": 2, "avatar_id": mid, "nickname": "停用头像可保留"}, 200)
	f.call(t, f.first, "PATCH", "/api/v1/admin/administrators/"+f.first, map[string]any{"expected_version": 1, "avatar_id": mid}, 422)
	f.call(t, f.first, "PATCH", path, map[string]any{"expected_version": 3, "avatar_id": ""}, 200)
	var pending int
	var changed *time.Time
	if err := f.a.DB.QueryRow(ctx, `SELECT nickname_changed_at,(SELECT count(*) FROM nickname_requests WHERE user_id=$1) FROM users WHERE id=$1`, f.second).Scan(&changed, &pending); err != nil || changed != nil || pending != 0 {
		t.Fatal("administrator name entered member review/cooldown", err)
	}
}

func TestAdministratorPasswordRevokesSessionsAndOwnResetRequiresLogin(t *testing.T) {
	f := adminAccountFixture(t)
	ctx := context.Background()
	path := "/api/v1/admin/administrators/" + f.second + "/password"
	verified, oldHash, err := (postgres.IdentityStore{DB: f.a.DB}).Credentials(ctx, f.second)
	if err != nil {
		t.Fatal(err)
	}
	body := map[string]any{"expected_version": 1, "password": "replacement-password-1234", "request_id": "reset-other-1"}
	out := memberData(t, f.call(t, f.first, "POST", path, body, 200))
	if out["reauthenticate"] != false || out["version"] != float64(2) {
		t.Fatal("reset response", out)
	}
	f.call(t, f.first, "POST", path, body, 200)
	body["password"] = "different-password-1234"
	f.call(t, f.first, "POST", path, body, 409)
	statusIs(t, surfaceRequest(t, f.adm, "GET", "/api/v1/me", "", nil, f.cookies[f.second]), 401)
	err = (postgres.IdentityStore{DB: f.a.DB}).CreateSession(ctx, tokenHash(randomToken()), verified, oldHash, "admin", time.Now().Add(time.Hour))
	if !errors.Is(err, identityapp.ErrCredentialsChanged) {
		t.Fatal("old verification survived password reset", err)
	}
	var count int
	if err = f.a.DB.QueryRow(ctx, `SELECT count(*) FROM sessions WHERE user_id=$1`, f.second).Scan(&count); err != nil || count != 0 {
		t.Fatal("target sessions remain", err)
	}
	self := f.call(t, f.first, "POST", "/api/v1/admin/administrators/"+f.first+"/password", map[string]any{"expected_version": 1, "password": "self-password-12345", "request_id": "reset-myself-1"}, 200)
	if memberData(t, self)["reauthenticate"] != true {
		t.Fatal("own password reset did not request reauthentication")
	}
	cleared := false
	for _, cookie := range self.Result().Cookies() {
		if cookie.Name == adminSessionCookie && cookie.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Fatal("own reset cookie not cleared")
	}
	statusIs(t, surfaceRequest(t, f.adm, "GET", "/api/v1/me", "", nil, f.cookies[f.first]), 401)
}

func TestAdministratorConcurrentDisableRechecksActorAndKeepsOne(t *testing.T) {
	f := adminAccountFixture(t)
	ctx := context.Background()
	s := postgres.AdminAccountStore{DB: f.a.DB, NewID: newID, CredentialKey: f.admin.Config.ProductCredentialKey}
	no := false
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, pair := range [][2]string{{f.first, f.second}, {f.second, f.first}} {
		wg.Add(1)
		go func(pair [2]string) {
			defer wg.Done()
			<-start
			_, err := s.Update(ctx, management.Actor{ID: pair[0], Role: "admin"}, pair[1], domain.UpdateInput{ExpectedVersion: 1, Enabled: &no})
			results <- err
		}(pair)
	}
	close(start)
	wg.Wait()
	close(results)
	successes := 0
	rejects := 0
	for err := range results {
		if err == nil {
			successes++
		} else {
			adminAccountErrorCode(t, err, "FORBIDDEN")
			rejects++
		}
	}
	if successes != 1 || rejects != 1 {
		t.Fatal("cross-disable did not serialize", successes, rejects)
	}
	var remaining int
	if err := f.a.DB.QueryRow(ctx, `SELECT count(*) FROM users WHERE role='admin' AND enabled`).Scan(&remaining); err != nil || remaining != 1 {
		t.Fatal("last administrator lost", err)
	}
	var disabled string
	if err := f.a.DB.QueryRow(ctx, `SELECT id FROM users WHERE role='admin' AND NOT enabled`).Scan(&disabled); err != nil {
		t.Fatal(err)
	}
	_, err := (&postgres.ManagementStore{DB: f.a.DB, NewID: newID}).UpdateUser(ctx, management.Actor{ID: disabled, Role: "admin"}, f.member, management.UserInput{Enabled: &no})
	adminAccountErrorCode(t, err, "FORBIDDEN")
}

func TestAdministratorResetSerializesWithVerifiedLogin(t *testing.T) {
	f := adminAccountFixture(t)
	ctx := context.Background()
	identity := postgres.IdentityStore{DB: f.a.DB}
	u, oldHash, err := identity.Credentials(ctx, f.second)
	if err != nil {
		t.Fatal(err)
	}
	// Hold the exact row lock both paths need, then queue an old verification and
	// a reset. Either ordering must leave no session authenticated by the old hash.
	tx, err := f.a.DB.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, f.second); err != nil {
		t.Fatal(err)
	}
	loginResult := make(chan error, 1)
	resetResult := make(chan error, 1)
	go func() {
		loginResult <- identity.CreateSession(ctx, tokenHash(randomToken()), u, oldHash, "admin", time.Now().Add(time.Hour))
	}()
	go func() {
		_, e := (postgres.AdminAccountStore{DB: f.a.DB, NewID: newID, CredentialKey: f.admin.Config.ProductCredentialKey}).ResetPassword(ctx, management.Actor{ID: f.first, Role: "admin"}, f.second, domain.PasswordInput{ExpectedVersion: 1, Password: "race-password-1234", RequestID: "race-reset-1234"}, "new-password-hash")
		resetResult <- e
	}()
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-resetResult; err != nil {
		t.Fatal("reset failed", err)
	}
	if err = <-loginResult; err != nil && !errors.Is(err, identityapp.ErrCredentialsChanged) {
		t.Fatal("session operation failed", err)
	}
	var count int
	if err = f.a.DB.QueryRow(ctx, `SELECT count(*) FROM sessions WHERE user_id=$1`, f.second).Scan(&count); err != nil || count != 0 {
		t.Fatal("old login escaped reset", err)
	}
}

func TestAdministratorAndMemberCanSelectSameAvatarConcurrently(t *testing.T) {
	f := adminAccountFixture(t)
	ctx := context.Background()
	mid := f.image(t, f.first, "avatar", "")
	f.call(t, f.first, "POST", "/api/v1/admin/avatar-library", map[string]any{"name": "共享头像", "media_id": mid, "sort_order": 0, "enabled": true}, 200)
	start := make(chan struct{})
	results := make(chan error, 3)
	go func() {
		<-start
		_, err := (postgres.AdminAccountStore{DB: f.a.DB, NewID: newID}).Update(ctx, management.Actor{ID: f.first, Role: "admin"}, f.second, domain.UpdateInput{ExpectedVersion: 1, AvatarID: &mid})
		results <- err
	}()
	for _, id := range []string{f.member, f.other} {
		go func(id string) {
			<-start
			_, err := (&postgres.ChatStore{DB: f.a.DB}).Profile(ctx, chat.Actor{ID: id, Surface: "public", Role: "member"}, &chat.ProfileInput{AvatarID: &mid})
			results <- err
		}(id)
	}
	close(start)
	for i := 0; i < 3; i++ {
		if err := <-results; err != nil {
			t.Fatal("concurrent shared avatar selection failed", err)
		}
	}
	var count int
	if err := f.a.DB.QueryRow(ctx, `SELECT count(*) FROM users WHERE avatar_media_id=$1`, mid).Scan(&count); err != nil || count != 3 {
		t.Fatal("shared avatar not saved on all accounts", err)
	}
}
