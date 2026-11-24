package httpapi

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestChatRepliesAndReadReceipts(t *testing.T) {
	f := newChatFixture(t)
	f.online(t, f.first, true)
	id := f.start(t, f.member, f.product)
	member := "/api/v1/chat/conversations/" + id
	admin := "/api/v1/admin/chat/conversations/" + id
	original := f.detail(t, f.member, id)["last_message_id"].(string)
	if got := f.detail(t, f.member, id)["peer_read_through"]; got != "0" {
		t.Fatalf("new conversation has an unexpected staff receipt: %v", got)
	}

	send := func(user, base, body, client, reply string) string {
		t.Helper()
		return memberData(t, f.call(t, user, "POST", base+"/messages", map[string]any{
			"body": body, "client_id": client, "reply_to_id": reply,
		}, 200))["id"].(string)
	}
	answer := send(f.first, admin, "可以，具体配置请看商品。", "quoted-answer", original)
	if retry := send(f.first, admin, "可以，具体配置请看商品。", "quoted-answer", original); retry != answer {
		t.Fatal("retry did not return the original message")
	}
	f.call(t, f.first, "POST", admin+"/messages", map[string]any{
		"body": "可以，具体配置请看商品。", "client_id": "quoted-answer", "reply_to_id": answer,
	}, 409)

	var history struct {
		Data []struct {
			ID    string `json:"id"`
			Reply *struct {
				ID      string `json:"id"`
				Kind    string `json:"kind"`
				Excerpt string `json:"excerpt"`
				Mine    bool   `json:"mine"`
			} `json:"reply"`
		} `json:"data"`
	}
	w := f.call(t, f.member, "GET", member+"/messages", nil, 200)
	if err := json.Unmarshal(w.Body.Bytes(), &history); err != nil || len(history.Data) != 2 {
		t.Fatalf("history length: %d, %v", len(history.Data), err)
	}
	if history.Data[0].Reply != nil {
		t.Fatal("legacy message unexpectedly has a quote")
	}
	reply := history.Data[1].Reply
	if history.Data[1].ID != answer || reply == nil || reply.ID != original || reply.Kind != "product" || reply.Excerpt != "咨询商品" || !reply.Mine {
		t.Fatalf("quote was not projected from the saved product message: %+v", reply)
	}

	otherID := f.start(t, f.other, f.product)
	foreign := f.detail(t, f.other, otherID)["last_message_id"].(string)
	for _, target := range []string{foreign, "9223372036854775808", "0"} {
		f.call(t, f.member, "POST", member+"/messages", map[string]any{
			"body": "请提供更多资料", "client_id": "invalid-reference", "reply_to_id": target,
		}, 422)
	}

	// Redis invalidates both participants after the read watermark actually advances.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	sub := f.a.Redis.Subscribe(ctx, chatChannel(f.member), chatChannel(f.first))
	defer sub.Close()
	for range 2 {
		if _, err := sub.Receive(ctx); err != nil {
			t.Fatal(err)
		}
	}
	beforeRead := f.detail(t, f.member, id)["updated_at"]
	f.call(t, f.member, "POST", member+"/read", map[string]string{"through": answer}, 200)
	channels := make(map[string]bool)
	for range 2 {
		event, err := sub.ReceiveMessage(ctx)
		if err != nil {
			t.Fatal(err)
		}
		channels[event.Channel] = true
	}
	if !channels[chatChannel(f.member)] || !channels[chatChannel(f.first)] {
		t.Fatalf("receipt did not notify both sides: %v", channels)
	}
	f.call(t, f.member, "POST", member+"/read", map[string]string{"through": original}, 200)
	if _, err := sub.ReceiveTimeout(ctx, 100*time.Millisecond); err == nil {
		t.Fatal("duplicate or stale read published another notification")
	}
	memberDetail := f.detail(t, f.member, id)
	if memberDetail["unread"] != float64(0) || memberDetail["updated_at"] != beforeRead ||
		f.detail(t, f.first, id)["peer_read_through"] != answer {
		t.Fatal("a read changed inbox order, failed to clear unread, or did not reach the sender")
	}

	followup := send(f.member, member, "好的，谢谢。", "quoted-followup", answer)
	f.call(t, f.first, "POST", admin+"/read", map[string]string{"through": followup}, 200)
	if got := f.detail(t, f.member, id)["peer_read_through"]; got != followup {
		t.Fatalf("staff receipt = %v, want %s", got, followup)
	}
	// Simulate a reassignment through the production dispatcher, including the new operator's unread reset.
	f.online(t, f.second, true)
	if _, err := f.a.DB.Exec(ctx, "UPDATE chat_presence SET last_seen=now()-interval '5 minutes' WHERE admin_id=$1", f.first); err != nil {
		t.Fatal(err)
	}
	if err := f.admin.DispatchChats(ctx); err != nil {
		t.Fatal(err)
	}
	if assigned := f.assigned(t, id); assigned != f.second {
		t.Fatalf("expected the second operator, got %s", assigned)
	}
	if got := f.detail(t, f.member, id)["peer_read_through"]; got != followup {
		t.Fatalf("reassignment regressed the member-facing receipt to %v", got)
	}
	if got := f.detail(t, f.second, id)["read_through"]; got != "0" {
		t.Fatalf("new operator inherited another operator's personal unread cursor: %v", got)
	}
}
