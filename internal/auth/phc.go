package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Cost is the cost of one argon2id computation. It is written in every hash
// (DESIGN.md §7.2), so a hash made with another cost is still verified, with
// the cost it was made with.
type Cost struct {
	// MemoryKiB is the memory in KiB.
	MemoryKiB uint32
	// Time is the number of passes.
	Time uint32
	// Threads is the degree of parallelism.
	Threads uint8
}

// ProductionCost is the cost of the hashes the server makes (§7.2):
// 64 MiB, 3 passes, 1 thread. Only the tests use a lower one.
func ProductionCost() Cost {
	return Cost{MemoryKiB: 64 * 1024, Time: 3, Threads: 1}
}

const (
	// saltLen and keyLen are the lengths of the salt and of the hash that
	// are written (§7.2).
	saltLen = 16
	keyLen  = 32
	// A hash read from the database is refused outside these bounds, before
	// anything is computed: argon2 would take the memory and the time the
	// string asks for. The salt bounds are those of the PHC format.
	maxMemoryKiB = 256 * 1024 // 256 MiB
	maxTime      = 16
	maxThreads   = 4
	minSaltLen   = 8
	maxSaltLen   = 48

	phcPrefix = "$argon2id$v=19$"
)

// valid tells whether argon2 can run with c, within the bounds of this
// package. argon2 raises a memory below 8 KiB per thread to that, so less
// is refused: two costs would name one computation.
func (c Cost) valid() bool {
	return c.Time >= 1 && c.Time <= maxTime && c.Threads >= 1 && c.Threads <= maxThreads &&
		c.MemoryKiB >= 8*uint32(c.Threads) && c.MemoryKiB <= maxMemoryKiB
}

// phc is a decoded hash string.
type phc struct {
	cost Cost
	salt []byte
	hash []byte
}

// b64 is the encoding of the PHC format: standard base64 without padding.
// Strict: of the spellings of the same bytes, only one is accepted.
var b64 = base64.RawStdEncoding.Strict()

// encode writes p in the PHC format:
//
//	$argon2id$v=19$m=65536,t=3,p=1$<salt>$<hash>
func (p phc) encode() string {
	return fmt.Sprintf("%sm=%d,t=%d,p=%d$%s$%s", phcPrefix, p.cost.MemoryKiB, p.cost.Time, p.cost.Threads,
		b64.EncodeToString(p.salt), b64.EncodeToString(p.hash))
}

// errPHC is the error of every string that decodePHC refuses. It says
// nothing of the string: a hash never reaches a log or an error (I5).
var errPHC = errors.New("not an argon2id hash in the PHC format")

// decodePHC reads a string written by encode, and only that form: argon2id,
// version 19, the three parameters in the order m, t, p as plain decimal
// numbers within the bounds of Cost, a salt and a hash of keyLen bytes. A
// string it accepts is exactly the one encode writes for what it returns.
func decodePHC(s string) (phc, error) {
	rest, ok := strings.CutPrefix(s, phcPrefix)
	if !ok {
		return phc{}, errPHC
	}
	fields := strings.Split(rest, "$")
	if len(fields) != 3 {
		return phc{}, errPHC
	}
	params := strings.Split(fields[0], ",")
	if len(params) != 3 {
		return phc{}, errPHC
	}
	m, okM := parameter(params[0], "m=")
	t, okT := parameter(params[1], "t=")
	p, okP := parameter(params[2], "p=")
	if !okM || !okT || !okP || p > maxThreads {
		return phc{}, errPHC
	}
	cost := Cost{MemoryKiB: m, Time: t, Threads: uint8(p)}
	if !cost.valid() {
		return phc{}, errPHC
	}
	// The decoder of encoding/base64 skips line breaks: a field must also be
	// what its bytes encode to.
	salt, err := b64.DecodeString(fields[1])
	if err != nil || b64.EncodeToString(salt) != fields[1] || len(salt) < minSaltLen || len(salt) > maxSaltLen {
		return phc{}, errPHC
	}
	hash, err := b64.DecodeString(fields[2])
	if err != nil || b64.EncodeToString(hash) != fields[2] || len(hash) != keyLen {
		return phc{}, errPHC
	}
	return phc{cost: cost, salt: salt, hash: hash}, nil
}

// parameter reads "<name><n>", with n a plain decimal number: no sign, no
// leading zero, no space.
func parameter(s, name string) (uint32, bool) {
	digits, ok := strings.CutPrefix(s, name)
	if !ok {
		return 0, false
	}
	n, err := strconv.ParseUint(digits, 10, 32)
	if err != nil || strconv.FormatUint(n, 10) != digits {
		return 0, false
	}
	return uint32(n), true
}

// hashPassword makes the PHC string of a password, with a new random salt.
// It is a long computation that takes cost.MemoryKiB of memory: the callers
// run it in the slot of the Service. cost must be valid.
func hashPassword(password string, cost Cost) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("reading random bytes for a salt: %w", err)
	}
	return phc{cost: cost, salt: salt, hash: key(password, salt, cost)}.encode(), nil
}

// verifyPassword tells whether password is the one encoded was made from,
// with the cost encoded names. The comparison is in constant time (I5). An
// error means that encoded is not a hash this package reads, and says
// nothing of it.
func verifyPassword(encoded, password string) (bool, error) {
	p, err := decodePHC(encoded)
	if err != nil {
		return false, err
	}
	return subtle.ConstantTimeCompare(key(password, p.salt, p.cost), p.hash) == 1, nil
}

func key(password string, salt []byte, cost Cost) []byte {
	return argon2.IDKey([]byte(password), salt, cost.Time, cost.MemoryKiB, cost.Threads, keyLen)
}
