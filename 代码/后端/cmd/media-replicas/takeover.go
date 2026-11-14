package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

const takeoverOrderName = "product-image-order.jsonl"

type productImageOrder struct {
	MediaID   string `json:"media_id"`
	ProductID string `json:"product_id"`
	SortOrder int    `json:"sort_order"`
}

// The original manifest remains complete; this separately bound list selects
// only the product originals imported into production, with their exact order.
func loadTakeoverOrder(parent string, receipt completionReceipt, hashes map[string]string, count int) (map[string]productImageOrder, error) {
	path := filepath.Join(parent, takeoverOrderName)
	real, err := filepath.EvalSymlinks(path)
	if err != nil || real != path || hashes[takeoverOrderName] != receipt.ProductImageOrderSHA {
		return nil, errors.New("product image order is not bound to takeover")
	}
	data, err := readBounded(path, 32<<20)
	if err != nil || sha(data) != receipt.ProductImageOrderSHA {
		return nil, errors.New("product image order checksum mismatch")
	}
	out, positions := map[string]productImageOrder{}, map[struct {
		ProductID string
		SortOrder int
	}]bool{}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		var row productImageOrder
		fields := map[string]json.RawMessage{}
		if decodeObject(scanner.Bytes(), &fields) != nil || len(fields["sort_order"]) == 0 || string(fields["sort_order"]) == "null" {
			return nil, errors.New("missing takeover image order")
		}
		if len(out) >= count || decodeObject(scanner.Bytes(), &row) != nil || !idPattern.MatchString(row.MediaID) || !idPattern.MatchString(row.ProductID) || row.SortOrder < 0 || row.SortOrder > 1000000 {
			return nil, errors.New("invalid takeover image order")
		}
		position := struct {
			ProductID string
			SortOrder int
		}{row.ProductID, row.SortOrder}
		if _, exists := out[row.MediaID]; exists || positions[position] {
			return nil, errors.New("duplicate takeover image order")
		}
		out[row.MediaID], positions[position] = row, true
	}
	if scanner.Err() != nil || len(out) != count {
		return nil, errors.New("incomplete takeover image order")
	}
	return out, nil
}

func validateTakeoverRecord(r replicaRecord, sources map[string]replicaRecord, order map[string]productImageOrder) error {
	source, found := sources[r.MediaID]
	_, selected := order[r.MediaID]
	if !found || !selected || !legacyNamePattern.MatchString(r.StorageKey) || r.Status != "existing_verified" ||
		r.ObjectKey != source.ObjectKey || r.SourceObjectKey != source.ObjectKey || r.StorageKey != source.StorageKey ||
		r.Bucket != source.Bucket || r.MIME != source.MIME || r.Size != source.Size || r.SHA256 != source.SHA256 || r.CRC64 != source.CRC64 ||
		r.DatabaseSHA != source.DatabaseSHA || r.DatabaseHashAvailable != source.DatabaseHashAvailable ||
		r.VerifiedAt.Before(source.VerifiedAt) || !validCopyETag(r.SourceETag) ||
		r.CopySourceIfMatch != "" || r.TargetETag != "" || r.CopiedCRC64 != "" {
		return errors.New("takeover differs from selected original evidence")
	}
	return nil
}

func validateTakeoverCLI(input verifiedManifest) error {
	if !input.ProductionTakeover {
		return nil
	}
	if os.Getenv("APP_ENV") != "production" || len(input.Records) != 48332 || input.Legacy != 48332 || input.Images != 0 || input.Bytes != 26841243424 ||
		input.SourceRecords != 48345 || input.SourceBytes != 26845180398 || len(input.Order) != 48332 {
		return errors.New("takeover requires the complete production original selection")
	}
	return nil
}
