-- R21-1 adds managed search keywords without rewriting product attribute codes.
ALTER TABLE product_dictionary_groups DROP CONSTRAINT product_dictionary_groups_key_check;
ALTER TABLE product_dictionary_groups ADD CONSTRAINT product_dictionary_groups_key_check
 CHECK(key IN ('server','gender','level','kind','keyword'));
INSERT INTO product_dictionary_groups(key,label,sort_order) VALUES ('keyword','热门筛选',0);
INSERT INTO product_dictionary_options(dictionary_key,value,label,sort_order)
SELECT 'keyword',keyword,keyword,position::integer
FROM unnest(ARRAY[
 '600外搭以上','500外搭以上','包子','400外搭以上','300外搭以上','200外搭以上',
 '永夜头','春日头','妄情头','狐惑外搭','墨离头','酒红头','永生头','宠溺头','纯黑名片',
 '独角兽','妖瞳','冷焰','余烬','叫人','狐狸','极地','白骨'
]) WITH ORDINALITY AS seed(keyword,position);

WITH ranked AS (
 SELECT dictionary_key,value,row_number() OVER(PARTITION BY dictionary_key ORDER BY
 CASE WHEN dictionary_key='server' THEN CASE label
   WHEN '一舞时光' THEN 1 WHEN '拾忆光阴' THEN 2 WHEN '十年之约' THEN 3
   WHEN '其他' THEN 4 WHEN '其他大区' THEN 4 ELSE 5 END
 ELSE CASE label WHEN '星钻' THEN 1 WHEN '银钻' THEN 2 WHEN '金钻' THEN 3
   WHEN '彩钻' THEN 4 WHEN '王钻' THEN 5 ELSE 6 END END,sort_order,value)::integer AS position
 FROM product_dictionary_options WHERE dictionary_key IN ('server','level')
)
UPDATE product_dictionary_options o SET sort_order=r.position,
 label=CASE WHEN o.dictionary_key='server' AND o.label='其他大区' THEN '其他' ELSE o.label END
FROM ranked r WHERE o.dictionary_key=r.dictionary_key AND o.value=r.value;
UPDATE product_dictionary_groups g SET version=version+1 WHERE key IN ('server','level')
 AND EXISTS(SELECT 1 FROM product_dictionary_options o WHERE o.dictionary_key=g.key);
