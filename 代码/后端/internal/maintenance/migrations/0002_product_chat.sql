-- Durable chat history; Redis only wakes connected clients, never owns messages.
CREATE TABLE chat_presence (
 admin_id text PRIMARY KEY REFERENCES users(id),
 session_hash text NOT NULL REFERENCES sessions(token_hash) ON DELETE CASCADE,
 accepting boolean NOT NULL DEFAULT false,
 last_seen timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE chat_conversations (
 id text PRIMARY KEY,
 member_id text NOT NULL REFERENCES users(id),
 product_id text NOT NULL REFERENCES products(id),
 product_snapshot jsonb NOT NULL,
 admin_id text REFERENCES users(id),
 status text NOT NULL DEFAULT 'waiting' CHECK(status IN ('waiting','active','closed')),
 member_read_id bigint NOT NULL DEFAULT 0,
 admin_read_id bigint NOT NULL DEFAULT 0,
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 CHECK (status <> 'active' OR admin_id IS NOT NULL)
);
CREATE UNIQUE INDEX chat_open_product ON chat_conversations(member_id,product_id) WHERE status <> 'closed';
CREATE INDEX chat_member_list ON chat_conversations(member_id,updated_at DESC,id);
CREATE INDEX chat_admin_list ON chat_conversations(admin_id,status,updated_at DESC,id);
CREATE INDEX chat_waiting_queue ON chat_conversations(created_at,id) WHERE status='waiting';
CREATE TABLE chat_messages (
 id bigserial PRIMARY KEY,
 conversation_id text NOT NULL REFERENCES chat_conversations(id),
 sender_id text NOT NULL REFERENCES users(id),
 sender_role text NOT NULL CHECK(sender_role IN ('member','admin')),
 client_id text NOT NULL CHECK(length(client_id) BETWEEN 1 AND 80),
 kind text NOT NULL DEFAULT 'text' CHECK(kind IN ('text','product')),
 body text NOT NULL DEFAULT '' CHECK(char_length(body) <= 2000),
 created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(conversation_id,sender_id,client_id)
);
CREATE INDEX chat_messages_history ON chat_messages(conversation_id,id DESC);
