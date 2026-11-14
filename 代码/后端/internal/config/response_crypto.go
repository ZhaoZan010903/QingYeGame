package config

import (
	"errors"
	"strconv"
)

type ResponseCryptoConfig struct{ Enabled bool }

func loadResponseCrypto() (ResponseCryptoConfig, error) {
	enabled, err := strconv.ParseBool(env("RESPONSE_CRYPTO_ENABLED", "false"))
	if err != nil {
		return ResponseCryptoConfig{}, errors.New("RESPONSE_CRYPTO_ENABLED must be boolean")
	}
	return ResponseCryptoConfig{Enabled: enabled}, nil
}
