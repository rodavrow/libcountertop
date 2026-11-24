package cast

import "crypto/x509"

const audioOnlyPolicy = "1.3.6.1.4.1.11129.2.5.2"

func AudioOnly(c *Credentials) bool {
	if c == nil {
		return false
	}
	for _, der := range append([][]byte{c.Device}, c.Intermediates...) {
		cert, err := x509.ParseCertificate(der)
		if err != nil {
			continue
		}
		for _, p := range cert.Policies {
			if p.String() == audioOnlyPolicy {
				return true
			}
		}
	}
	return false
}
