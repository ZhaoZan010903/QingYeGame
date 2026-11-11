package recommendation

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
)

type Config struct {
	Version        string `json:"version"`
	WindowDays     int    `json:"window_days"`
	ViewWeight     int    `json:"view_weight"`
	FavoriteWeight int    `json:"favorite_weight"`
	NewBonus       int    `json:"new_bonus"`
	NewHours       int    `json:"new_hours"`
}

func DefaultConfig() Config {
	return Config{"engagement-v2", 7, 1, 5, 0, 72}
}
func ParseConfig(raw []byte) (Config, error) {
	var c Config
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&c); err != nil {
		return c, errors.New("推荐配置结构无效")
	}
	if strings.TrimSpace(c.Version) == "" || len(c.Version) > 40 || c.WindowDays < 1 || c.WindowDays > 30 || c.NewHours < 1 || c.NewHours > 720 || c.ViewWeight < 0 || c.ViewWeight > 100 || c.FavoriteWeight < 0 || c.FavoriteWeight > 100 || c.NewBonus < 0 || c.NewBonus > 100 {
		return c, errors.New("推荐参数超出允许范围")
	}
	// Legacy keys remain readable; publication time never earns popularity points.
	c.NewBonus = 0
	return c, nil
}

const ItemCFVersion = "itemcf-v1"
const ItemCFLock = 1834182
