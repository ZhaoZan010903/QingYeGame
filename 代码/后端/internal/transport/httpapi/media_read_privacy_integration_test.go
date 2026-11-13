package httpapi

import (
	"context"
	"testing"
)

func TestR201ChatTransferKeepsImagePermissionsWithReadProtection(t *testing.T) {
	f := newChatFixture(t)
	f.online(t, f.first, true)
	id := f.start(t, f.member, f.product)
	image := f.image(t, f.member, "chat", id)
	f.call(t, f.member, "POST", "/api/v1/chat/conversations/"+id+"/messages", map[string]any{"body": "isolated photo", "client_id": "r201-private", "image_ids": []string{image}}, 200)
	ns := "test:r201-privacy:" + newID() + ":"
	enableReadProtection(t, f.a, ns)
	enableReadProtection(t, f.admin, ns)
	f.pub = f.a.Routes()
	f.adm = f.admin.Routes()
	path := "/api/v1/media/" + image + "/file"
	for _, actor := range []string{f.member, f.first} {
		f.mediaCall(t, actor, "GET", path, nil, 200)
	}
	for _, actor := range []string{f.other, f.second} {
		f.mediaCall(t, actor, "GET", path, nil, 404)
	}
	if _, e := f.a.DB.Exec(context.Background(), "UPDATE chat_conversations SET admin_id=$1 WHERE id=$2", f.second, id); e != nil {
		t.Fatal(e)
	}
	for _, suffix := range []string{"", "?variant=thumb"} {
		f.mediaCall(t, f.first, "GET", path+suffix, nil, 404)
		f.mediaCall(t, f.second, "GET", path+suffix, nil, 200)
	}
}
