-- Inbox ordering does not filter status; keep historical aliases out of its index.
CREATE INDEX chat_admin_inbox ON chat_conversations(admin_id,updated_at DESC,id DESC) WHERE merged_into IS NULL;
CREATE INDEX chat_member_inbox ON chat_conversations(member_id,updated_at DESC,id DESC) WHERE merged_into IS NULL;
-- Exact unread counts can read sender identity without fetching each message body.
CREATE INDEX chat_messages_unread ON chat_messages(conversation_id,id) INCLUDE(sender_id);
CREATE INDEX chat_distinct_products ON chat_messages(conversation_id,(product_snapshot->>'id')) WHERE kind='product';
