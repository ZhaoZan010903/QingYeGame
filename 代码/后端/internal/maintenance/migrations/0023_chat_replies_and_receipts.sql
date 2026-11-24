ALTER TABLE chat_messages ADD COLUMN reply_to_id bigint REFERENCES chat_messages(id);
CREATE INDEX chat_replies_by_original ON chat_messages(reply_to_id) WHERE reply_to_id IS NOT NULL;

-- 接待人员的个人游标仍在转接时归零。面向会员的客服已读回执继续保留。
ALTER TABLE chat_conversations ADD COLUMN staff_read_id bigint NOT NULL DEFAULT 0;
UPDATE chat_conversations SET staff_read_id=admin_read_id WHERE admin_read_id>0;
