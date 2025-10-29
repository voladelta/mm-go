package x10

import (
	"errors"
	"fmt"
	"math/big"
	"strings"
)

type StarkPerpetualAccount struct {
	vault      uint64
	privateKey string
	publicKey  string
	apiKey     string
}

// NewStarkPerpetualAccount constructs the account, validating hex inputs.
func NewStarkPerpetualAccount(vault uint64, privateKeyHex, publicKeyHex, apiKey string) (*StarkPerpetualAccount, error) {
	if err := isHexString(privateKeyHex); err != nil {
		return nil, fmt.Errorf("invalid private key: %w", err)
	}
	if err := isHexString(publicKeyHex); err != nil {
		return nil, fmt.Errorf("invalid public key: %w", err)
	}

	// Ensure that private key and public key have 0x prefix
	if len(privateKeyHex) < 2 || privateKeyHex[:2] != "0x" {
		return nil, fmt.Errorf("private key must start with 0x")
	}
	if len(publicKeyHex) < 2 || publicKeyHex[:2] != "0x" {
		return nil, fmt.Errorf("public key must start with 0x")
	}

	// Check that API key does not start with 0x
	if len(apiKey) >= 2 && apiKey[:2] == "0x" {
		return nil, fmt.Errorf("api key should not start with 0x")
	}

	return &StarkPerpetualAccount{
		vault:      vault,
		privateKey: privateKeyHex,
		publicKey:  publicKeyHex,
		apiKey:     apiKey,
	}, nil
}

// Sign delegates to SignFunc, returning (r,s).
func (stark *StarkPerpetualAccount) Sign(msgHash string) (*big.Int, *big.Int, error) {
	if msgHash == "" {
		return big.NewInt(0), big.NewInt(0), errors.New("msgHash is empty")
	}

	sig, err := SignMessage(msgHash, stark.privateKey)
	if err != nil {
		return big.NewInt(0), big.NewInt(0), err
	}

	// Extract r, s from the signature string.
	// Signature is in the format of {r}{s}{v}, where r, s and v are 64 chars each (192 hex chars).
	r, isGoodR := big.NewInt(0).SetString(sig[:64], 16)
	s, isGoodS := big.NewInt(0).SetString(sig[64:128], 16)

	if !isGoodR || !isGoodS {
		return big.NewInt(0), big.NewInt(0), errors.New("big int setting failed")
	}

	return r, s, nil
}

func isHexString(s string) error {
	if s == "" {
		return errors.New("empty hex string")
	}
	if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X") {
		s = s[2:]
	}
	if len(s) == 0 {
		return errors.New("empty hex after 0x")
	}
	// Validate hex characters
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return fmt.Errorf("invalid hex char %q", c)
		}
	}
	return nil
}
