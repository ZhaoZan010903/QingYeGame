-- Separate private conversation images from publishable product media.
ALTER TABLE media_files ADD COLUMN purpose text NOT NULL DEFAULT 'product' CHECK(purpose IN ('product','chat','avatar'));
ALTER TABLE media_files ADD COLUMN conversation_id text REFERENCES chat_conversations(id);
ALTER TABLE media_files ADD CONSTRAINT media_conversation_scope CHECK((purpose='chat')=(conversation_id IS NOT NULL));
ALTER TABLE users ADD COLUMN avatar_media_id text REFERENCES media_files(id);
CREATE INDEX users_avatar_media ON users(avatar_media_id) WHERE avatar_media_id IS NOT NULL;
ALTER TABLE chat_messages DROP CONSTRAINT chat_messages_kind_check;
ALTER TABLE chat_messages ADD CONSTRAINT chat_messages_kind_check CHECK(kind IN ('text','product','image'));
CREATE TABLE chat_message_images (
 message_id bigint NOT NULL REFERENCES chat_messages(id),
 media_id text NOT NULL UNIQUE REFERENCES media_files(id),
 position smallint NOT NULL CHECK(position BETWEEN 0 AND 8),
 PRIMARY KEY(message_id,position)
);
CREATE INDEX media_chat_scope ON media_files(conversation_id) WHERE purpose='chat' AND deleted_at IS NULL;
