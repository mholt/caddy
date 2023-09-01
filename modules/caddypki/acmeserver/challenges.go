package acmeserver

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/smallstep/certificates/authority/provisioner"
)

type ACMEChallenge string

const (
	HTTP_01     ACMEChallenge = "http-01"
	DNS_01      ACMEChallenge = "dns-01"
	TLS_ALPN_01 ACMEChallenge = "tls-alpn-01"
)

// validate checks if the given challenge is supported.
func (c ACMEChallenge) validate() error {
	switch c {
	case HTTP_01, DNS_01, TLS_ALPN_01:
		return nil
	default:
		return fmt.Errorf("acme challenge %q is not supported", c)
	}
}

func (c *ACMEChallenge) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	*c = ACMEChallenge(strings.ToLower(s))
	return nil
}

// String returns a string representation of the challenge.
func (c ACMEChallenge) String() string {
	return strings.ToLower(string(c))
}

type ACMEChallenges []ACMEChallenge

// validate checks if the given challenges are supported.
func (c ACMEChallenges) validate() error {
	for _, ch := range c {
		if err := ch.validate(); err != nil {
			return err
		}
	}
	return nil
}
func (c ACMEChallenges) toSmallstepType() []provisioner.ACMEChallenge {
	if len(c) == 0 {
		return nil
	}
	ac := make([]provisioner.ACMEChallenge, len(c))
	for i, ch := range c {
		ac[i] = provisioner.ACMEChallenge(ch)
	}
	return ac
}
func stringToChallenges(chs []string) ACMEChallenges {
	challenges := make(ACMEChallenges, len(chs))
	for i, ch := range chs {
		challenges[i] = ACMEChallenge(ch)
	}
	return challenges
}
