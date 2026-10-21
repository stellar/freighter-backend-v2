package swap

import "github.com/stellar/go-stellar-sdk/xdr"

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
