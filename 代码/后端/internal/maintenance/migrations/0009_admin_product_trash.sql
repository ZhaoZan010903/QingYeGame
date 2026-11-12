-- Admin recycle-bin entries retain their content and media references. Member
-- draft tombstones have already been erased and must never become restorable.
ALTER TABLE products ADD COLUMN deleted_by_admin boolean NOT NULL DEFAULT false;
ALTER TABLE products ADD CONSTRAINT products_admin_trash_check
 CHECK (NOT deleted_by_admin OR deleted_at IS NOT NULL);
CREATE INDEX products_admin_trash_idx ON products(updated_at DESC,id)
 WHERE deleted_by_admin AND deleted_at IS NOT NULL;
