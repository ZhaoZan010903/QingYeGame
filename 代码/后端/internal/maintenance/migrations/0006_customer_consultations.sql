-- 一位会员保留一条客服会话；历史商品上下文跟随消息，不能被最新商品覆盖。
ALTER TABLE chat_conversations ADD COLUMN merged_into text REFERENCES chat_conversations(id);
ALTER TABLE chat_messages ADD COLUMN product_snapshot jsonb;
UPDATE chat_messages m SET product_snapshot=c.product_snapshot
FROM chat_conversations c WHERE m.conversation_id=c.id AND m.kind='product';

CREATE TABLE product_consultations (
 product_id text NOT NULL REFERENCES products(id),
 member_id text NOT NULL REFERENCES users(id),
 first_consulted_at timestamptz NOT NULL DEFAULT now(),
 last_consulted_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(product_id,member_id)
);
CREATE INDEX product_consultations_window ON product_consultations(product_id,first_consulted_at);
INSERT INTO product_consultations(product_id,member_id,first_consulted_at,last_consulted_at)
SELECT c.product_id,c.member_id,min(c.created_at),max(c.updated_at)
FROM chat_conversations c JOIN products p ON p.id=c.product_id
WHERE p.owner_id IS DISTINCT FROM c.member_id
GROUP BY c.product_id,c.member_id;

CREATE TEMP TABLE chat_merge_map ON COMMIT DROP AS
SELECT id,first_value(id) OVER(PARTITION BY member_id
 ORDER BY CASE status WHEN 'active' THEN 0 WHEN 'waiting' THEN 1 ELSE 2 END,updated_at DESC,id) AS canonical_id
FROM chat_conversations;
-- 旧会话可能使用相同客户端幂等键；保留消息而非在合并时删除冲突内容。
DO $$
DECLARE item record; candidate text; suffix integer;
BEGIN
 FOR item IN
  SELECT * FROM (
   SELECT m.id,m.sender_id,map.canonical_id,
    row_number() OVER(PARTITION BY map.canonical_id,m.sender_id,m.client_id
     ORDER BY (map.id=map.canonical_id) DESC,m.id) AS position
   FROM chat_messages m JOIN chat_merge_map map ON map.id=m.conversation_id
  ) ranked WHERE position>1 ORDER BY id
 LOOP
  candidate:='migrated:'||item.id::text; suffix:=0;
  WHILE EXISTS(SELECT 1 FROM chat_messages m JOIN chat_merge_map map ON map.id=m.conversation_id
   WHERE map.canonical_id=item.canonical_id AND m.sender_id=item.sender_id AND m.client_id=candidate) LOOP
   suffix:=suffix+1; candidate:='migrated:'||item.id::text||':'||suffix::text;
  END LOOP;
  UPDATE chat_messages SET client_id=candidate WHERE id=item.id;
 END LOOP;
END $$;
UPDATE chat_messages m SET conversation_id=map.canonical_id
FROM chat_merge_map map WHERE m.conversation_id=map.id AND map.id<>map.canonical_id;
-- 活跃接待优先决定保留哪条会话；列表中的商品仍取合并后最近一次咨询。
WITH latest AS (
 SELECT DISTINCT ON(conversation_id) conversation_id,product_snapshot
 FROM chat_messages WHERE kind='product' AND product_snapshot IS NOT NULL
 ORDER BY conversation_id,id DESC
)
UPDATE chat_conversations c SET product_id=latest.product_snapshot->>'id',
 product_snapshot=latest.product_snapshot FROM latest
WHERE c.id=latest.conversation_id;
WITH state AS (
 SELECT map.canonical_id,min(c.created_at) AS first_at,max(c.updated_at) AS last_at,
 min(c.member_read_id) AS read_id,count(*) AS count
 FROM chat_merge_map map JOIN chat_conversations c ON c.id=map.id GROUP BY map.canonical_id
)
UPDATE chat_conversations c SET created_at=s.first_at,updated_at=s.last_at,
 member_read_id=s.read_id,admin_read_id=CASE WHEN s.count>1 THEN 0 ELSE c.admin_read_id END
FROM state s WHERE c.id=s.canonical_id;
UPDATE chat_conversations c SET merged_into=map.canonical_id,status='closed'
FROM chat_merge_map map WHERE c.id=map.id AND map.id<>map.canonical_id;
DROP INDEX chat_open_product;
CREATE UNIQUE INDEX chat_one_customer ON chat_conversations(member_id) WHERE merged_into IS NULL;
CREATE INDEX chat_product_contexts ON chat_messages(conversation_id,id DESC) WHERE kind='product';
ALTER TABLE product_hot_scores ADD COLUMN consultations integer NOT NULL DEFAULT 0;
UPDATE site_settings SET value=value||'{"consultation_weight":8,"version":"engagement-v3"}'::jsonb
WHERE key='recommendation';
