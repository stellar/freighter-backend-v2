package swap

import (
	"math/big"

	"github.com/stellar/go-stellar-sdk/xdr"
)

func addrString(a xdr.ScAddress) string {
	s, err := a.String()
	if err != nil {
		return ""
	}
	return s
}

// scAddr returns the strkey of an Address ScVal, or "".
func scAddr(v xdr.ScVal) string {
	a, ok := v.GetAddress()
	if !ok {
		return ""
	}
	return addrString(a)
}

// scField returns the value under Symbol or String key name in a Map ScVal.
func scField(v xdr.ScVal, name string) (xdr.ScVal, bool) {
	m, ok := v.GetMap()
	if !ok || m == nil {
		return xdr.ScVal{}, false
	}
	for _, e := range *m {
		if scSym(e.Key) == name {
			return e.Val, true
		}
	}
	return xdr.ScVal{}, false
}

// scVec returns the elements of a Vec ScVal, or nil.
func scVec(v xdr.ScVal) []xdr.ScVal {
	vec, ok := v.GetVec()
	if !ok || vec == nil {
		return nil
	}
	return *vec
}

// scSym returns the text of a Symbol or String ScVal, or "".
func scSym(v xdr.ScVal) string {
	if s, ok := v.GetSym(); ok {
		return string(s)
	}
	if s, ok := v.GetStr(); ok {
		return string(s)
	}
	return ""
}

func scI128(v xdr.ScVal) (*big.Int, bool) {
	p, ok := v.GetI128()
	if !ok {
		return nil, false
	}
	n := new(big.Int).Lsh(big.NewInt(int64(p.Hi)), 64)
	return n.Add(n, new(big.Int).SetUint64(uint64(p.Lo))), true
}

func scU32(v xdr.ScVal) (uint32, bool) {
	u, ok := v.GetU32()
	return uint32(u), ok
}
