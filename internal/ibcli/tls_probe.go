package ibcli

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"
)

type untrustedTLSCertificateError struct {
	verifyErr   error
	certificate *x509.Certificate
}

func (e *untrustedTLSCertificateError) Error() string {
	if e.verifyErr != nil {
		return e.verifyErr.Error()
	}
	return "certificate is not trusted"
}

func (a *App) promptReachableServer(currentServer string, timeoutSeconds int) (string, bool, error) {
	defaultValue := currentServer
	for {
		server, err := a.gum.Input("Infoblox server", defaultValue, false)
		if err != nil {
			return "", false, err
		}
		normalized, err := normalizeServer(server)
		if err != nil {
			return "", false, err
		}
		verifySSL, err := a.validateServerReachability(normalized, timeoutSeconds)
		if err == nil {
			return normalized, verifySSL, nil
		}
		defaultValue = normalized
		if certErr, ok := err.(*untrustedTLSCertificateError); ok {
			a.printUntrustedCertificate(certErr)
			trust, promptErr := a.gum.Confirm("Trust this Infoblox HTTPS certificate for this profile?", false)
			if promptErr != nil {
				return "", false, promptErr
			}
			if trust {
				a.printConfigureWarning("WARNING: SSL verification will be disabled for this profile.")
				return normalized, false, nil
			}
			a.printConfigureWarning("WARNING: certificate was not trusted; enter a different Infoblox server.")
			continue
		}
		a.printConfigureWarning("WARNING: Infoblox server is not reachable: " + err.Error())
	}
}

func (a *App) validateServerReachability(server string, timeoutSeconds int) (bool, error) {
	parsed, err := url.Parse(server)
	if err != nil {
		return false, err
	}
	switch parsed.Scheme {
	case "https":
		if err := a.probeTrustedHTTPS(parsed, timeoutSeconds); err == nil {
			a.printConfigureInfo("INFO: Infoblox server is reachable over HTTPS with a trusted certificate.")
			return true, nil
		} else if certErr, ok := a.probeUntrustedHTTPS(parsed, timeoutSeconds, err); ok {
			return false, certErr
		} else {
			return false, err
		}
	case "http":
		if err := probeTCP(parsed, "80", timeoutSeconds); err != nil {
			return false, err
		}
		a.printConfigureWarning("WARNING: Infoblox server is reachable over plain HTTP; SSL verification is not available.")
		return false, nil
	default:
		return false, cliError("unsupported server URL scheme %q", parsed.Scheme)
	}
}

func (a *App) probeTrustedHTTPS(parsed *url.URL, timeoutSeconds int) error {
	conn, err := tls.DialWithDialer(tlsProbeDialer(timeoutSeconds), "tcp", hostPort(parsed, "443"), a.tlsProbeConfig(parsed, false))
	if err != nil {
		return err
	}
	_ = conn.Close()
	return nil
}

func (a *App) probeUntrustedHTTPS(parsed *url.URL, timeoutSeconds int, verifyErr error) (*untrustedTLSCertificateError, bool) {
	conn, err := tls.DialWithDialer(tlsProbeDialer(timeoutSeconds), "tcp", hostPort(parsed, "443"), a.tlsProbeConfig(parsed, true))
	if err != nil {
		return nil, false
	}
	defer conn.Close()
	certificates := conn.ConnectionState().PeerCertificates
	if len(certificates) == 0 {
		return nil, false
	}
	return &untrustedTLSCertificateError{
		verifyErr:   verifyErr,
		certificate: certificates[0],
	}, true
}

func (a *App) tlsProbeConfig(parsed *url.URL, insecure bool) *tls.Config {
	config := &tls.Config{
		ServerName:         parsed.Hostname(),
		InsecureSkipVerify: insecure, // #nosec G402 -- used only for operator-confirmed certificate inspection
	}
	if a.tlsRootCAs != nil {
		config.RootCAs = a.tlsRootCAs
	}
	return config
}

func probeTCP(parsed *url.URL, defaultPort string, timeoutSeconds int) error {
	conn, err := tlsProbeDialer(timeoutSeconds).Dial("tcp", hostPort(parsed, defaultPort))
	if err != nil {
		return err
	}
	return conn.Close()
}

func tlsProbeDialer(timeoutSeconds int) *net.Dialer {
	if timeoutSeconds <= 0 {
		timeoutSeconds = defaultTimeoutSeconds
	}
	return &net.Dialer{Timeout: time.Duration(timeoutSeconds) * time.Second}
}

func hostPort(parsed *url.URL, defaultPort string) string {
	host := parsed.Hostname()
	port := parsed.Port()
	if port == "" {
		port = defaultPort
	}
	return net.JoinHostPort(host, port)
}

func (a *App) printUntrustedCertificate(err *untrustedTLSCertificateError) {
	a.printConfigureWarning("WARNING: HTTPS certificate is not trusted: " + err.Error())
	cert := err.certificate
	if cert == nil {
		return
	}
	for _, line := range certificateInfoLines(cert) {
		a.printConfigureInfo("INFO: " + line)
	}
}

func certificateInfoLines(cert *x509.Certificate) []string {
	lines := []string{
		"Subject: " + firstNonEmpty(cert.Subject.CommonName, cert.Subject.String()),
		"Issuer: " + firstNonEmpty(cert.Issuer.CommonName, cert.Issuer.String()),
		"Valid from: " + cert.NotBefore.Format(time.RFC3339),
		"Valid until: " + cert.NotAfter.Format(time.RFC3339),
		"SHA256 fingerprint: " + certificateFingerprint(cert),
	}
	if len(cert.DNSNames) > 0 {
		lines = append(lines, "DNS names: "+strings.Join(cert.DNSNames, ", "))
	}
	if len(cert.IPAddresses) > 0 {
		ips := make([]string, 0, len(cert.IPAddresses))
		for _, ip := range cert.IPAddresses {
			ips = append(ips, ip.String())
		}
		lines = append(lines, "IP addresses: "+strings.Join(ips, ", "))
	}
	return lines
}

func certificateFingerprint(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.Raw)
	raw := fmt.Sprintf("%X", sum[:])
	pairs := make([]string, 0, len(raw)/2)
	for i := 0; i < len(raw); i += 2 {
		pairs = append(pairs, raw[i:i+2])
	}
	return strings.Join(pairs, ":")
}
