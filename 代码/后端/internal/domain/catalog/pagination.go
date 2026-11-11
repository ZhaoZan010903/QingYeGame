package catalog

import (
	"encoding/base64"
	"errors"
	"strconv"
)

func DecodePage(limitValue, cursor string) (int, int, error) {
	limit := 20
	if s := limitValue; s != "" {
		v, e := strconv.Atoi(s)
		if e != nil || v < 1 || v > 50 {
			return 0, 0, errors.New("分页大小应为1至50")
		}
		limit = v
	}
	offset := 0
	if s := cursor; s != "" {
		b, e := base64.RawURLEncoding.DecodeString(s)
		if e != nil {
			return 0, 0, errors.New("分页游标无效")
		}
		v, e := strconv.Atoi(string(b))
		if e != nil || v < 0 || v > 1000000 {
			return 0, 0, errors.New("分页游标无效")
		}
		offset = v
	}
	return limit, offset, nil
}
func NextPage(offset, limit, total int) any {
	if offset+limit >= total {
		return nil
	}
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(offset + limit)))
}
