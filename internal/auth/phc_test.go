package auth

import (
	"bytes"
	"encoding/hex"
	"strings"
	"testing"
)

// The vectors of the reference implementation of Argon2 (phc-winner-argon2,
// src/test.c, version 0x13, Argon2id): the password, the salt, the hash in
// hexadecimal and the encoded string. They are public test data.
var referenceVectors = []struct {
	password, salt string
	hex            string
	encoded        string
}{
	{"password", "somesalt", "09316115d5cf24ed5a15a31a3ba326e5cf32edc24702987c02b6566f61913cf7",
		"$argon2id$v=19$m=65536,t=2,p=1$c29tZXNhbHQ$CTFhFdXPJO1aFaMaO6Mm5c8y7cJHAph8ArZWb2GRPPc"},
	{"password", "somesalt", "9dfeb910e80bad0311fee20f9c0e2b12c17987b4cac90c2ef54d5b3021c68bfe",
		"$argon2id$v=19$m=256,t=2,p=1$c29tZXNhbHQ$nf65EOgLrQMR/uIPnA4rEsF5h7TKyQwu9U1bMCHGi/4"},
	{"password", "somesalt", "6d093c501fd5999645e0ea3bf620d7b8be7fd2db59c20d9fff9539da2bf57037",
		"$argon2id$v=19$m=256,t=2,p=2$c29tZXNhbHQ$bQk8UB/VmZZF4Oo79iDXuL5/0ttZwg2f/5U52iv1cDc"},
	{"password", "somesalt", "f6a5adc1ba723dddef9b5ac1d464e180fcd9dffc9d1cbf76cca2fed795d9ca98",
		"$argon2id$v=19$m=65536,t=1,p=1$c29tZXNhbHQ$9qWtwbpyPd3vm1rB1GThgPzZ3/ydHL92zKL+15XZypg"},
	{"password", "somesalt", "9025d48e68ef7395cca9079da4c4ec3affb3c8911fe4f86d1a2520856f63172c",
		"$argon2id$v=19$m=65536,t=4,p=1$c29tZXNhbHQ$kCXUjmjvc5XMqQedpMTsOv+zyJEf5PhtGiUghW9jFyw"},
	{"differentpassword", "somesalt", "0b84d652cf6b0c4beaef0dfe278ba6a80df6696281d7e0d2891b817d8c458fde",
		"$argon2id$v=19$m=65536,t=2,p=1$c29tZXNhbHQ$C4TWUs9rDEvq7w3+J4umqA32aWKB1+DSiRuBfYxFj94"},
	{"password", "diffsalt", "bdf32b05ccc42eb15d58fd19b1f856b113da1e9a5874fdcc544308565aa8141c",
		"$argon2id$v=19$m=65536,t=2,p=1$ZGlmZnNhbHQ$vfMrBczELrFdWP0ZsfhWsRPaHppYdP3MVEMIVlqoFBw"},
}

// DESIGN.md §7.2: the codec reads the strings of the reference
// implementation, computes the same hash from them, and writes them back
// byte for byte.
func TestPHCReferenceVectors(t *testing.T) {
	for _, v := range referenceVectors {
		p, err := decodePHC(v.encoded)
		if err != nil {
			t.Errorf("%s: %v", v.encoded, err)
			continue
		}
		if string(p.salt) != v.salt || hex.EncodeToString(p.hash) != v.hex {
			t.Errorf("%s: decoded another salt or hash", v.encoded)
		}
		if got := p.encode(); got != v.encoded {
			t.Errorf("%s: encoded again as %s", v.encoded, got)
		}
		if got := hex.EncodeToString(key(v.password, p.salt, p.cost)); got != v.hex {
			t.Errorf("%s: argon2id gives %s", v.encoded, got)
		}
		if ok, err := verifyPassword(v.encoded, v.password); err != nil || !ok {
			t.Errorf("%s: the password is not verified: %v", v.encoded, err)
		}
		for _, wrong := range []string{"", v.password + " ", strings.ToUpper(v.password), v.password[1:]} {
			if ok, err := verifyPassword(v.encoded, wrong); err != nil || ok {
				t.Errorf("%s: a wrong password is verified: %v", v.encoded, err)
			}
		}
	}
}

// A hash is written in the form of §7.2, with a salt of 16 random bytes and
// a hash of 32, and read back as it was written.
func TestPHCRoundTrip(t *testing.T) {
	const password = "a password of the tests"
	seen := map[string]bool{}
	for range 20 {
		encoded, err := hashPassword(password, testCost)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(encoded, "$argon2id$v=19$m=64,t=1,p=1$") {
			t.Fatal("the hash does not begin with the algorithm, the version and the cost")
		}
		// 16 and 32 bytes in base64 without padding.
		if fields := strings.Split(encoded, "$"); len(fields) != 6 || len(fields[4]) != 22 || len(fields[5]) != 43 {
			t.Fatal("the hash has not a salt of 16 bytes and a hash of 32")
		}
		p, err := decodePHC(encoded)
		if err != nil {
			t.Fatal(err)
		}
		if p.cost != testCost || len(p.salt) != saltLen || len(p.hash) != keyLen || p.encode() != encoded {
			t.Fatal("the hash is not read back as it was written")
		}
		if ok, err := verifyPassword(encoded, password); err != nil || !ok {
			t.Fatalf("the password is not verified: %v", err)
		}
		if ok, err := verifyPassword(encoded, password+"x"); err != nil || ok {
			t.Fatalf("a wrong password is verified: %v", err)
		}
		if strings.Contains(encoded, password) {
			t.Fatal("the hash holds the password")
		}
		// The salt is random: one password, never the same string twice.
		if seen[encoded] || seen[string(p.salt)] {
			t.Fatal("two hashes with the same salt")
		}
		seen[encoded], seen[string(p.salt)] = true, true
	}
}

// The one test with the cost of production (§7.2): 64 MiB, 3 passes, 1
// thread, as the string says.
func TestProductionCost(t *testing.T) {
	if got := ProductionCost(); got != (Cost{MemoryKiB: 65536, Time: 3, Threads: 1}) || !got.valid() {
		t.Fatalf("the production cost is %+v", got)
	}
	const password = "a password of the tests"
	encoded, err := hashPassword(password, ProductionCost())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(encoded, "$argon2id$v=19$m=65536,t=3,p=1$") {
		t.Fatal("the hash does not say m=65536,t=3,p=1")
	}
	if ok, err := verifyPassword(encoded, password); err != nil || !ok {
		t.Fatalf("the password is not verified: %v", err)
	}
	if ok, err := verifyPassword(encoded, "another password"); err != nil || ok {
		t.Fatalf("a wrong password is verified: %v", err)
	}
}

// A hash with another cost than the one of the server is verified with its
// own: the parameters are in the string, so they can change (§7.2).
func TestVerifyUsesTheCostOfTheHash(t *testing.T) {
	const password = "a password of the tests"
	for _, cost := range []Cost{{MemoryKiB: 8, Time: 1, Threads: 1}, {MemoryKiB: 128, Time: 2, Threads: 2}, {MemoryKiB: 32, Time: 3, Threads: 4}} {
		encoded, err := hashPassword(password, cost)
		if err != nil {
			t.Fatal(err)
		}
		p, err := decodePHC(encoded)
		if err != nil || p.cost != cost {
			t.Fatalf("cost %+v read back as %+v: %v", cost, p.cost, err)
		}
		if ok, err := verifyPassword(encoded, password); err != nil || !ok {
			t.Fatalf("cost %+v: the password is not verified: %v", cost, err)
		}
	}
}

// What is not exactly the form the codec writes is refused, with an error
// that repeats nothing of the string.
func TestDecodePHCRefuses(t *testing.T) {
	const (
		salt = "c29tZXNhbHQ"
		hash = "CTFhFdXPJO1aFaMaO6Mm5c8y7cJHAph8ArZWb2GRPPc"
		good = "$argon2id$v=19$m=65536,t=2,p=1$" + salt + "$" + hash
	)
	if _, err := decodePHC(good); err != nil {
		t.Fatal(err)
	}
	// The highest cost read from the database: 256 MiB, 16 passes, 4
	// threads (N-097). Above it, a damaged row would hold the slot for long.
	if p, err := decodePHC("$argon2id$v=19$m=262144,t=16,p=4$" + salt + "$" + hash); err != nil ||
		p.cost != (Cost{MemoryKiB: 262144, Time: 16, Threads: 4}) {
		t.Fatalf("the highest cost is refused: %v", err)
	}
	for name, s := range map[string]string{
		"empty":                       "",
		"a password":                  "correct horse battery staple",
		"argon2i":                     "$argon2i$v=19$m=65536,t=2,p=1$" + salt + "$" + hash,
		"argon2d":                     "$argon2d$v=19$m=65536,t=2,p=1$" + salt + "$" + hash,
		"bcrypt":                      "$2b$12$abcdefghijklmnopqrstuuKIXDm1eF1lUeEPySTnHz5vDeJkbUyqS",
		"version 16":                  "$argon2id$v=16$m=65536,t=2,p=1$" + salt + "$" + hash,
		"no version":                  "$argon2id$m=65536,t=2,p=1$" + salt + "$" + hash,
		"no leading dollar":           "argon2id$v=19$m=65536,t=2,p=1$" + salt + "$" + hash,
		"upper case":                  "$ARGON2ID$v=19$m=65536,t=2,p=1$" + salt + "$" + hash,
		"parameters in another order": "$argon2id$v=19$t=2,m=65536,p=1$" + salt + "$" + hash,
		"a parameter missing":         "$argon2id$v=19$m=65536,t=2$" + salt + "$" + hash,
		"a parameter more":            "$argon2id$v=19$m=65536,t=2,p=1,keyid=a$" + salt + "$" + hash,
		"a parameter twice":           "$argon2id$v=19$m=65536,m=65536,p=1$" + salt + "$" + hash,
		"memory zero":                 "$argon2id$v=19$m=0,t=2,p=1$" + salt + "$" + hash,
		"memory below 8 per thread":   "$argon2id$v=19$m=15,t=2,p=2$" + salt + "$" + hash,
		"memory over the bound":       "$argon2id$v=19$m=262145,t=2,p=1$" + salt + "$" + hash,
		"memory over 32 bits":         "$argon2id$v=19$m=4294967296,t=2,p=1$" + salt + "$" + hash,
		"memory huge":                 "$argon2id$v=19$m=99999999999999999999,t=2,p=1$" + salt + "$" + hash,
		"time zero":                   "$argon2id$v=19$m=65536,t=0,p=1$" + salt + "$" + hash,
		"time over the bound":         "$argon2id$v=19$m=65536,t=17,p=1$" + salt + "$" + hash,
		"threads zero":                "$argon2id$v=19$m=65536,t=2,p=0$" + salt + "$" + hash,
		"threads over the bound":      "$argon2id$v=19$m=65536,t=2,p=5$" + salt + "$" + hash,
		"threads over a byte":         "$argon2id$v=19$m=65536,t=2,p=257$" + salt + "$" + hash,
		"a leading zero":              "$argon2id$v=19$m=065536,t=2,p=1$" + salt + "$" + hash,
		"a sign":                      "$argon2id$v=19$m=+65536,t=2,p=1$" + salt + "$" + hash,
		"a negative number":           "$argon2id$v=19$m=65536,t=-2,p=1$" + salt + "$" + hash,
		"a space":                     "$argon2id$v=19$m=65536, t=2,p=1$" + salt + "$" + hash,
		"hexadecimal":                 "$argon2id$v=19$m=0x10000,t=2,p=1$" + salt + "$" + hash,
		"no number":                   "$argon2id$v=19$m=,t=2,p=1$" + salt + "$" + hash,
		"no salt":                     "$argon2id$v=19$m=65536,t=2,p=1$$" + hash,
		"a short salt":                "$argon2id$v=19$m=65536,t=2,p=1$c29tZXNhbA$" + hash,
		"a long salt":                 "$argon2id$v=19$m=65536,t=2,p=1$" + strings.Repeat("AAAA", 17) + "$" + hash,
		"a padded salt":               "$argon2id$v=19$m=65536,t=2,p=1$c29tZXNhbHQ=$" + hash,
		"a salt in base64url":         "$argon2id$v=19$m=65536,t=2,p=1$c29tZXNhbH_-$" + hash,
		"a salt with trailing bits":   "$argon2id$v=19$m=65536,t=2,p=1$c29tZXNhbHR$" + hash,
		"no hash":                     "$argon2id$v=19$m=65536,t=2,p=1$" + salt + "$",
		"no hash field":               "$argon2id$v=19$m=65536,t=2,p=1$" + salt,
		"a short hash":                "$argon2id$v=19$m=65536,t=2,p=1$" + salt + "$" + hash[:42],
		"a hash of 24 bytes":          "$argon2id$v=19$m=65536,t=2,p=1$" + salt + "$CTFhFdXPJO1aFaMaO6Mm5c8y7cJHAph8",
		"a hash of 35 bytes":          "$argon2id$v=19$m=65536,t=2,p=1$" + salt + "$" + hash + "AAAA",
		"a field more":                good + "$",
		"data after":                  good + "$AAAA",
		"a line break after":          good + "\n",
		"a space before":              " " + good,
		"a NUL inside":                strings.Replace(good, "t=2", "t=2\x00", 1),
	} {
		_, err := decodePHC(s)
		if err == nil {
			t.Errorf("%s: accepted", name)
			continue
		}
		if err != errPHC {
			t.Errorf("%s: the error is not the one that says nothing", name)
		}
		if ok, verr := verifyPassword(s, "password"); ok || verr == nil {
			t.Errorf("%s: verified against a string that is not a hash", name)
		}
	}
}

// The comparison of the hashes looks at every byte: a hash that differs in
// any one of them is not verified.
func TestVerifyComparesEveryByte(t *testing.T) {
	v := referenceVectors[1] // m=256: cheap
	p, err := decodePHC(v.encoded)
	if err != nil {
		t.Fatal(err)
	}
	for i := range p.hash {
		changed := phc{cost: p.cost, salt: p.salt, hash: bytes.Clone(p.hash)}
		changed.hash[i] ^= 0x01
		if ok, err := verifyPassword(changed.encode(), v.password); err != nil || ok {
			t.Fatalf("a hash that differs at byte %d is verified: %v", i, err)
		}
	}
}

func TestCostValid(t *testing.T) {
	for _, c := range []Cost{ProductionCost(), testCost, {MemoryKiB: 8, Time: 1, Threads: 1}, {MemoryKiB: maxMemoryKiB, Time: maxTime, Threads: maxThreads}} {
		if !c.valid() {
			t.Errorf("%+v is refused", c)
		}
	}
	for _, c := range []Cost{{}, {MemoryKiB: 64, Time: 0, Threads: 1}, {MemoryKiB: 64, Time: 1, Threads: 0}, {MemoryKiB: 7, Time: 1, Threads: 1},
		{MemoryKiB: 31, Time: 1, Threads: 4}, {MemoryKiB: maxMemoryKiB + 1, Time: 1, Threads: 1}, {MemoryKiB: 64, Time: maxTime + 1, Threads: 1},
		{MemoryKiB: 1024, Time: 1, Threads: maxThreads + 1}} {
		if c.valid() {
			t.Errorf("%+v is accepted", c)
		}
	}
}
