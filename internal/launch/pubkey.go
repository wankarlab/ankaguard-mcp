package launch

import "math/big"

const base58Alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

var base58Index = func() [256]int {
	var idx [256]int
	for i := range idx {
		idx[i] = -1
	}
	for i := 0; i < len(base58Alphabet); i++ {
		idx[base58Alphabet[i]] = i
	}
	return idx
}()

// IsValidPublicKey reports whether s is a base58-encoded 32-byte Solana
// address (account or program id).
func IsValidPublicKey(s string) bool {
	b, ok := decodeBase58(s)
	return ok && len(b) == 32
}

func decodeBase58(s string) ([]byte, bool) {
	if s == "" {
		return nil, false
	}
	n := new(big.Int)
	radix := big.NewInt(58)
	leadingZeros := 0
	countingZeros := true
	for i := 0; i < len(s); i++ {
		d := base58Index[s[i]]
		if d < 0 {
			return nil, false
		}
		if countingZeros && d == 0 {
			leadingZeros++
		} else {
			countingZeros = false
		}
		n.Mul(n, radix)
		n.Add(n, big.NewInt(int64(d)))
	}
	return append(make([]byte, leadingZeros), n.Bytes()...), true
}
