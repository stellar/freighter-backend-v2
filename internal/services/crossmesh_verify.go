// ABOUTME: Derives Cross Mesh deposit addresses locally (CREATE2 of a clone with immutable args) and checks upstream ones.
// ABOUTME: Mirrors the factory's computeAddress(bytes recipient, uint256 index, bool fast); output is EIP-55 checksummed.
package services

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strings"

	"golang.org/x/crypto/sha3"
)

// Clone bytecode around the implementation address: a minimal proxy whose
// runtime code carries the immutable args after it. See computeInitCode.
var (
	cloneCreationPrefix = mustDecodeHex("3d81600a3d39f3363d3d373d3d3d363d73")
	cloneRuntimeSuffix  = mustDecodeHex("5af43d82803e903d91602b57fd5bf3")
)

// cloneRuntimeSize is the clone's runtime size without args (0x2d bytes).
const cloneRuntimeSize = 0x2d

// ComputeDepositAddress returns the EIP-55 deposit address the Cross Mesh
// factory deploys for recipient at index, as its computeAddress does:
//
//	args     = ASCII(recipient) ‖ byte(fast)
//	salt     = keccak256(abi.encode(bytes recipient, uint256 index))
//	initCode = 0x61 ‖ uint16(len(args)+0x2d) ‖ prefix ‖ implementation ‖ suffix ‖ args
//	address  = keccak256(0xff ‖ factory ‖ salt ‖ keccak256(initCode))[12:]
func ComputeDepositAddress(recipient string, index uint64, fast bool, factory, implementation string) (string, error) {
	factoryBytes, err := decodeHexAddress(factory)
	if err != nil {
		return "", fmt.Errorf("factory: %w", err)
	}
	implBytes, err := decodeHexAddress(implementation)
	if err != nil {
		return "", fmt.Errorf("implementation: %w", err)
	}

	args := append([]byte(recipient), 0)
	if fast {
		args[len(args)-1] = 1
	}

	initCode := []byte{0x61}
	initCode = binary.BigEndian.AppendUint16(initCode, uint16(len(args)+cloneRuntimeSize))
	initCode = append(initCode, cloneCreationPrefix...)
	initCode = append(initCode, implBytes...)
	initCode = append(initCode, cloneRuntimeSuffix...)
	initCode = append(initCode, args...)

	preimage := []byte{0xff}
	preimage = append(preimage, factoryBytes...)
	preimage = append(preimage, keccak256(abiEncodeBytesUint256([]byte(recipient), index))...)
	preimage = append(preimage, keccak256(initCode)...)
	return checksumAddress(keccak256(preimage)[12:]), nil
}

// VerifyDepositAddress derives the standard-speed address for recipient and
// index from the pinned factory and implementation, and reports whether it
// equals upstream ignoring case. derived is the EIP-55 form to show the user;
// it is empty when the pinned contracts are malformed.
func VerifyDepositAddress(upstream, recipient string, index uint64, factory, implementation string) (derived string, ok bool) {
	derived, err := ComputeDepositAddress(recipient, index, false, factory, implementation)
	if err != nil {
		return "", false
	}
	return derived, strings.EqualFold(derived, upstream)
}

// abiEncodeBytesUint256 is Solidity's abi.encode(bytes b, uint256 n): the
// offset of b's tail (0x40), n, then b's length and b right-padded to 32 bytes.
func abiEncodeBytesUint256(b []byte, n uint64) []byte {
	out := make([]byte, 0, 96+(len(b)+31)/32*32)
	out = append(out, uint256Word(0x40)...)
	out = append(out, uint256Word(n)...)
	out = append(out, uint256Word(uint64(len(b)))...)
	out = append(out, b...)
	return append(out, make([]byte, (32-len(b)%32)%32)...)
}

func uint256Word(n uint64) []byte {
	word := make([]byte, 32)
	binary.BigEndian.PutUint64(word[24:], n)
	return word
}

// checksumAddress renders a 20-byte address in EIP-55 mixed case: a hex
// letter is upper case when the matching nibble of keccak256(lowercase hex)
// is 8 or more.
func checksumAddress(addr []byte) string {
	lower := hex.EncodeToString(addr)
	hash := keccak256([]byte(lower))
	out := []byte(lower)
	for i, c := range out {
		nibble := hash[i/2] >> 4
		if i%2 == 1 {
			nibble = hash[i/2] & 0x0f
		}
		if c >= 'a' && nibble >= 8 {
			out[i] = c - 'a' + 'A'
		}
	}
	return "0x" + string(out)
}

// decodeHexAddress parses a 0x-prefixed 20-byte EVM address in any case.
func decodeHexAddress(s string) ([]byte, error) {
	b, err := hex.DecodeString(strings.TrimPrefix(strings.TrimPrefix(s, "0x"), "0X"))
	if err != nil {
		return nil, fmt.Errorf("invalid EVM address %q: %w", s, err)
	}
	if len(b) != 20 {
		return nil, fmt.Errorf("invalid EVM address %q: %d bytes, want 20", s, len(b))
	}
	return b, nil
}

func keccak256(data []byte) []byte {
	h := sha3.NewLegacyKeccak256()
	h.Write(data)
	return h.Sum(nil)
}

func mustDecodeHex(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}
