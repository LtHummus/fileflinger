package codegen

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"strings"

	"github.com/wordgen/wordlists/eff"
)

const (
	WordCount = 3
	PartCount = WordCount + 1 // words plus one number
	Separator = "-"
)

// GenerateCode returns WordCount words from the EFF short list plus a two digit number in a random position,
// e.g. "bunt-67-lemon-spry". that's about 40-ish bits of entropy (3*log2(1296) + log2(100) + log2(4)). That feeeeellllllsssss
// on the lower end, but also I don't want to make the codes too much longer
func GenerateCode() (string, error) {
	numberPos, err := intN(PartCount)
	if err != nil {
		return "", err
	}

	parts := make([]string, PartCount)
	for i := range parts {
		if i == numberPos {
			n, err := intN(100)
			if err != nil {
				return "", err
			}
			parts[i] = fmt.Sprintf("%02d", n)
		} else {
			n, err := intN(len(eff.Short1))
			if err != nil {
				return "", err
			}
			parts[i] = eff.Short1[n]
		}
	}
	return strings.Join(parts, Separator), nil
}

func intN(n int) (int, error) {
	v, err := rand.Int(rand.Reader, big.NewInt(int64(n)))
	if err != nil {
		return 0, fmt.Errorf("codegen: could not read random: %w", err)
	}
	return int(v.Int64()), nil
}
