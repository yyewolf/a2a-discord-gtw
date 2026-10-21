package render

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
)

// Kind is what a component does when used.
type Kind string

const (
	KindChoice        Kind = "c"  // button: pick choice Arg of question Index
	KindSelect        Kind = "s"  // select menu: pick choices of question Index
	KindOther         Kind = "o"  // button: open the free-text modal for question Index
	KindOtherSubmit   Kind = "m"  // modal: free-text answer to question Index
	KindApprove       Kind = "a"  // button: approve tool Index
	KindReject        Kind = "r"  // button: open the rejection modal for tool Index
	KindRejectSubmit  Kind = "rm" // modal: reject tool Index with a reason
	KindApproveAll    Kind = "aa" // button: approve every tool
	KindRejectAll     Kind = "ra" // button: open the rejection modal for every tool
	KindRejectAllDone Kind = "rx" // modal: reject every tool with a reason
)

const idPrefix = "adg"

// ID is the state carried in a component's custom ID. Discord gives it back
// on every interaction, so nothing needs to be kept in memory between the
// question and the click. Key is the ShortKey of the request key: a click on
// a question that has since been replaced is detected and refused.
type ID struct {
	Kind  Kind
	Task  string
	Key   string
	Index int
	Arg   int
}

// keyLen characters of base64url(sha256(key)): 72 bits, so two requests'
// keys never collide in practice, even when they share a long prefix as
// Anthropic tool IDs (toolu_01…) do.
const keyLen = 12

// String encodes the ID; it stays well under Discord's 100-character limit
// for a UUID task ID.
func (id ID) String() string {
	return strings.Join([]string{idPrefix, string(id.Kind), id.Task, id.Key, strconv.Itoa(id.Index), strconv.Itoa(id.Arg)}, ":")
}

// ShortKey is the digest of a request key carried in custom IDs.
func ShortKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return base64.RawURLEncoding.EncodeToString(sum[:])[:keyLen]
}

// ParseID decodes a custom ID made by ID.String.
func ParseID(s string) (ID, error) {
	parts := strings.Split(s, ":")
	if len(parts) != 6 || parts[0] != idPrefix {
		return ID{}, fmt.Errorf("render: not a gateway component: %q", s)
	}
	idx, err1 := strconv.Atoi(parts[4])
	arg, err2 := strconv.Atoi(parts[5])
	if err1 != nil || err2 != nil || parts[2] == "" {
		return ID{}, fmt.Errorf("render: malformed component id: %q", s)
	}
	return ID{Kind: Kind(parts[1]), Task: parts[2], Key: parts[3], Index: idx, Arg: arg}, nil
}

// Matches reports whether the ID was made for the request with this key.
func (id ID) Matches(key string) bool { return id.Key == ShortKey(key) }
