package maintenance

import (
	"context"
	"qingye/backend/internal/domain/catalog"
	"sort"
)

// Imports append observed choices without undoing an operator's labels or disabled options.
func (s *Service) mergeLegacyDictionaries(ctx context.Context, groups []catalog.FilterGroup) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	sort.Slice(groups, func(i, j int) bool { return groups[i].Key < groups[j].Key })
	for _, g := range groups {
		var version int
		if err = tx.QueryRow(ctx, `SELECT version FROM product_dictionary_groups WHERE key=$1 FOR UPDATE`, g.Key).Scan(&version); err != nil {
			return err
		}
		var added int64
		for _, o := range g.Options {
			tag, e := tx.Exec(ctx, `INSERT INTO product_dictionary_options(dictionary_key,value,label) VALUES($1,$2,$3) ON CONFLICT(dictionary_key,value) DO NOTHING`, g.Key, o.Value, o.Label)
			if e != nil {
				return e
			}
			added += tag.RowsAffected()
		}
		if added > 0 {
			if _, err = tx.Exec(ctx, `UPDATE product_dictionary_groups SET version=version+1 WHERE key=$1`, g.Key); err != nil {
				return err
			}
		}
	}
	return tx.Commit(ctx)
}
