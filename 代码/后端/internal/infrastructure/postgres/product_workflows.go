package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/jackc/pgx/v5"
	"qingye/backend/internal/domain/catalog"
	"qingye/backend/internal/domain/fault"
	"qingye/backend/internal/domain/management"
	"qingye/backend/internal/infrastructure/security"
)

func nextProductCode(ctx context.Context, tx pgx.Tx, trade string) (string, error) {
	sequence, prefix := "product_sale_code_seq", "QYCH"
	if trade == "rent" {
		sequence, prefix = "product_rent_code_seq", "QYZH"
	}
	// The indexed maximum also covers legacy imports performed after an empty-schema migration.
	// The per-prefix lock makes sequence resynchronization safe across concurrent creators.
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", "product-number:"+prefix); err != nil {
		return "", err
	}
	var maximum int64
	err := tx.QueryRow(ctx, `SELECT COALESCE((SELECT substring(product_code from '[0-9]+$')::bigint FROM products WHERE product_code ~ '^QY(CH|ZH)[0-9]{1,15}$' AND left(product_code,4)=$1 ORDER BY substring(product_code from '[0-9]+$')::bigint DESC LIMIT 1),0)`, prefix).Scan(&maximum)
	if err != nil {
		return "", err
	}
	var n int64
	if err = tx.QueryRow(ctx, "SELECT nextval($1::regclass)", sequence).Scan(&n); err != nil {
		return "", err
	}
	if n <= maximum {
		if err = tx.QueryRow(ctx, "SELECT setval($1::regclass,$2,true)", sequence, maximum+1).Scan(&n); err != nil {
			return "", err
		}
	}
	return fmt.Sprintf("%s%d", prefix, n), nil
}

func saveProductExtras(ctx context.Context, tx pgx.Tx, id, key string, in catalog.ProductInput) error {
	quotes := in.RentalQuotes
	if quotes == nil {
		quotes = []catalog.RentalQuote{}
	}
	if _, err := tx.Exec(ctx, "UPDATE products SET rental_quotes=$2 WHERE id=$1", id, quotes); err != nil {
		return err
	}
	if in.PrivateCredentials == nil {
		return nil
	}
	if key == "" {
		return fault.New(fault.Unavailable, "CREDENTIAL_STORAGE_UNAVAILABLE", "私密资料存储尚未配置，请联系管理员")
	}
	raw, err := json.Marshal(in.PrivateCredentials)
	if err != nil {
		return err
	}
	sealed, err := security.SealCredentials(key, id, raw)
	if err != nil {
		return fault.New(fault.Unavailable, "CREDENTIAL_STORAGE_UNAVAILABLE", "私密资料存储配置无效")
	}
	_, err = tx.Exec(ctx, `INSERT INTO product_private_credentials(product_id,ciphertext) VALUES($1,$2) ON CONFLICT(product_id) DO UPDATE SET ciphertext=excluded.ciphertext,updated_at=now()`, id, sealed)
	return err
}
func (s *ManagementStore) PrivateCredentials(ctx context.Context, actor management.Actor, id string) (map[string]any, error) {
	if err := management.ValidateActor(actor); err != nil {
		return nil, err
	}
	if s.CredentialKey == "" {
		return nil, fault.New(fault.Unavailable, "CREDENTIAL_STORAGE_UNAVAILABLE", "私密资料存储尚未配置")
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, managementStoreError(err)
	}
	defer tx.Rollback(ctx)
	var ciphertext []byte
	if err = tx.QueryRow(ctx, "SELECT ciphertext FROM product_private_credentials WHERE product_id=$1", id).Scan(&ciphertext); err != nil {
		if err == pgx.ErrNoRows {
			return nil, fault.New(fault.NotFound, "NOT_FOUND", "该商品尚未提交私密资料")
		}
		return nil, managementStoreError(err)
	}
	raw, err := security.OpenCredentials(s.CredentialKey, id, ciphertext)
	if err != nil {
		return nil, fault.New(fault.Unavailable, "CREDENTIAL_STORAGE_UNAVAILABLE", "私密资料无法解密，请检查部署密钥")
	}
	var value catalog.PrivateCredentials
	if err = json.Unmarshal(raw, &value); err != nil {
		return nil, managementStoreError(err)
	}
	// Audit identity and product only; never store plaintext in audit payloads.
	if _, err = tx.Exec(ctx, "INSERT INTO admin_logs(id,user_id,action,resource_id) VALUES($1,$2,'product.credentials.read',$3)", s.NewID(), actor.ID, id); err != nil {
		return nil, managementStoreError(err)
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, managementStoreError(err)
	}
	return map[string]any{"data": value}, nil
}
