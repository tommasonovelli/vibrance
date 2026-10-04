package auth

import (
	"bytes"
	"math/big"
	"regexp"
	"testing"
)

// The PHC form of §7.2 written a second time, with a regular expression,
// numbers without a limit and a base64 decoder made by hand: the fuzz
// target compares decodePHC with it.
var (
	modelPHC = regexp.MustCompile(`^\$argon2id\$v=19\$m=([0-9]+),t=([0-9]+),p=([0-9]+)\$([A-Za-z0-9+/]*)\$([A-Za-z0-9+/]*)$`)
	// A plain decimal number: no leading zero.
	modelNumber = regexp.MustCompile(`^(0|[1-9][0-9]*)$`)
)

const modelAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

// modelBase64 decodes unpadded standard base64, bit by bit. It refuses a
// length no bytes encode to, and bits left over that are not zero.
func modelBase64(s string) ([]byte, bool) {
	bits := new(big.Int)
	for _, c := range []byte(s) {
		bits.Lsh(bits, 6)
		bits.Or(bits, big.NewInt(int64(bytes.IndexByte([]byte(modelAlphabet), c))))
	}
	spare := len(s) * 6 % 8
	if spare == 6 {
		// One character more than whole bytes: it encodes nothing.
		return nil, false
	}
	rest := new(big.Int)
	bits.QuoRem(bits, big.NewInt(1<<spare), rest)
	if rest.Sign() != 0 {
		return nil, false
	}
	out := make([]byte, len(s)*6/8)
	bits.FillBytes(out)
	return out, true
}

// modelDecode is decodePHC by the second writing.
func modelDecode(s string) (phc, bool) {
	m := modelPHC.FindStringSubmatch(s)
	if m == nil {
		return phc{}, false
	}
	var n [3]*big.Int
	for i, digits := range m[1:4] {
		if !modelNumber.MatchString(digits) {
			return phc{}, false
		}
		n[i], _ = new(big.Int).SetString(digits, 10)
	}
	memory, time, threads := n[0], n[1], n[2]
	between := func(v *big.Int, lo, hi int64) bool {
		return v.Cmp(big.NewInt(lo)) >= 0 && v.Cmp(big.NewInt(hi)) <= 0
	}
	if !between(time, 1, 16) || !between(threads, 1, 4) || !between(memory, 8*threads.Int64(), 256*1024) {
		return phc{}, false
	}
	salt, ok := modelBase64(m[4])
	if !ok || len(salt) < 8 || len(salt) > 48 {
		return phc{}, false
	}
	hash, ok := modelBase64(m[5])
	if !ok || len(hash) != 32 {
		return phc{}, false
	}
	return phc{cost: Cost{MemoryKiB: uint32(memory.Int64()), Time: uint32(time.Int64()), Threads: uint8(threads.Int64())},
		salt: salt, hash: hash}, true
}

// FuzzPHC (§12.5): decodePHC never panics; it accepts exactly the strings
// of the second writing, with the same values; what it accepts is within
// the bounds and is written back byte for byte; and a password hashed with
// a cost a hash names is verified, and no other.
func FuzzPHC(f *testing.F) {
	for _, v := range referenceVectors {
		f.Add(v.encoded, v.password)
	}
	const body = "c29tZXNhbHQ$CTFhFdXPJO1aFaMaO6Mm5c8y7cJHAph8ArZWb2GRPPc"
	for _, s := range []string{
		"", "$", "$argon2id$", "$argon2id$v=19$", "$argon2id$v=19$m=8,t=1,p=1$$",
		"$argon2id$v=19$m=8,t=1,p=1$" + body,
		"$argon2id$v=19$m=64,t=1,p=1$" + body,
		"$argon2id$v=19$m=064,t=1,p=1$" + body,
		"$argon2id$v=19$m=15,t=1,p=2$" + body,
		"$argon2id$v=19$m=262144,t=16,p=4$" + body,
		"$argon2id$v=19$m=262145,t=1,p=1$" + body,
		"$argon2id$v=19$m=64,t=17,p=1$" + body,
		"$argon2id$v=19$m=64,t=1,p=5$" + body,
		"$argon2id$v=19$m=1048576,t=64,p=16$" + body,
		"$argon2id$v=19$m=1048577,t=1,p=1$" + body,
		"$argon2id$v=19$m=4294967296,t=1,p=1$" + body,
		"$argon2id$v=19$m=64,t=0,p=1$" + body,
		"$argon2id$v=19$m=64,t=1,p=256$" + body,
		"$argon2id$v=19$t=1,m=64,p=1$" + body,
		"$argon2i$v=19$m=64,t=1,p=1$" + body,
		"$argon2id$v=19$m=64,t=1,p=1$c29tZXNhbHR$CTFhFdXPJO1aFaMaO6Mm5c8y7cJHAph8ArZWb2GRPPc",
		"$argon2id$v=19$m=64,t=1,p=1$c29tZXNhbHQ=$CTFhFdXPJO1aFaMaO6Mm5c8y7cJHAph8ArZWb2GRPPc",
		"$argon2id$v=19$m=64,t=1,p=1$c29tZXNhbHQ$CTFhFdXPJO1aFaMaO6Mm5c8y7cJHAph8ArZWb2GRPPd",
		"$argon2id$v=19$m=64,t=1,p=1$" + body + "$",
		"$argon2id$v=19$m=64,t=1,p=1$" + body + "\n",
	} {
		f.Add(s, "a password of the tests")
	}

	f.Fuzz(func(t *testing.T, encoded, password string) {
		got, err := decodePHC(encoded)
		want, ok := modelDecode(encoded)
		if (err == nil) != ok {
			t.Fatalf("decodePHC accepted: %t, the second writing: %t", err == nil, ok)
		}
		if err != nil {
			if err != errPHC {
				t.Fatal("the error of a refusal is not the one that says nothing")
			}
			if verified, verr := verifyPassword(encoded, password); verified || verr == nil {
				t.Fatal("a password is verified against a string that is not a hash")
			}
			return
		}
		if got.cost != want.cost || !bytes.Equal(got.salt, want.salt) || !bytes.Equal(got.hash, want.hash) {
			t.Fatal("decodePHC and the second writing read different values")
		}
		if !got.cost.valid() {
			t.Fatal("an accepted hash has a cost outside the bounds")
		}
		if got.encode() != encoded {
			t.Fatal("an accepted hash is not written back as it was read")
		}
		// argon2 itself only with a cost that is cheap: the others would
		// take up to 256 MiB each.
		if got.cost.MemoryKiB > 256 || got.cost.Time > 2 {
			return
		}
		made := phc{cost: got.cost, salt: got.salt, hash: key(password, got.salt, got.cost)}.encode()
		if verified, verr := verifyPassword(made, password); verr != nil || !verified {
			t.Fatal("a password is not verified against its own hash")
		}
		if verified, verr := verifyPassword(made, password+"x"); verr != nil || verified {
			t.Fatal("another password is verified")
		}
		if verified, verr := verifyPassword(encoded, password); verr != nil || verified != bytes.Equal(got.hash, key(password, got.salt, got.cost)) {
			t.Fatal("verifyPassword disagrees with the hash it was given")
		}
	})
}
