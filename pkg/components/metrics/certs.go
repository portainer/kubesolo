package metrics

import (
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"time"

	"github.com/portainer/kubesolo/types"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog/log"
)

// trackedCertificate pairs the label value reported for a certificate with the
// path it is read from.
type trackedCertificate struct {
	name string
	path string
}

// certificateCollector reports the validity window of every PKI certificate
// kubesolo manages.
//
// Certificates are read and parsed at scrape time rather than cached at
// startup: kubesolo re-signs its leaf certificates when the node IP changes or
// an existing cert has expired (see pki.InvalidateIfIPChanged), so a cached
// value would keep reporting the pre-rotation expiry until the next restart.
// The cost is a handful of small file reads per scrape.
type certificateCollector struct {
	certs  []trackedCertificate
	expiry *prometheus.Desc
	valid  *prometheus.Desc
}

func newCertificateCollector(embedded types.Embedded) *certificateCollector {
	return &certificateCollector{
		certs: trackedCertificates(embedded),
		expiry: prometheus.NewDesc(
			prometheus.BuildFQName(metricsNamespace, "", "certificate_expiry_timestamp_seconds"),
			"Unix timestamp at which the named control plane certificate expires. Absent if the certificate cannot be read or parsed.",
			[]string{"name"}, nil,
		),
		valid: prometheus.NewDesc(
			prometheus.BuildFQName(metricsNamespace, "", "certificate_valid"),
			"1 if the named control plane certificate is readable and currently inside its validity window, 0 otherwise.",
			[]string{"name"}, nil,
		),
	}
}

// trackedCertificates enumerates the certificates that make up the kubesolo
// control plane PKI. Private keys and the service account signing key are
// deliberately absent: they carry no validity window to report.
func trackedCertificates(embedded types.Embedded) []trackedCertificate {
	certs := []trackedCertificate{
		{"ca", embedded.CACerts.Cert},
		{"apiserver", embedded.APIServerCerts.Cert},
		{"controller-manager", embedded.ControllerManagerCerts.Cert},
		{"kubelet", embedded.KubeletCerts.Cert},
		{"admin", embedded.AdminCerts.Cert},
		{"webhook", embedded.WebhookCerts.Cert},
		{"request-header-ca", embedded.RequestHeaderCerts.CACert},
		{"request-header-client", embedded.RequestHeaderCerts.ClientCert},
	}

	// The d2k certificates are only generated when the integration is enabled;
	// tracking them unconditionally would report certificate_valid 0 for every
	// deployment that does not use d2k.
	if embedded.D2K {
		certs = append(certs,
			trackedCertificate{"d2k-server", embedded.D2KCerts.ServerCert},
			trackedCertificate{"d2k-client", embedded.D2KCerts.ClientCert},
		)
	}

	return certs
}

func (c *certificateCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.expiry
	ch <- c.valid
}

func (c *certificateCollector) Collect(ch chan<- prometheus.Metric) {
	now := time.Now()

	for _, tracked := range c.certs {
		cert, err := parseCertificateFile(tracked.path)
		if err != nil {
			// A missing or corrupt certificate is reported as invalid rather
			// than omitted entirely, so that scrapers see an actionable 0
			// instead of a silently absent series.
			log.Debug().
				Str("component", "metrics").
				Str("certificate", tracked.name).
				Str("path", tracked.path).
				Err(err).
				Msg("failed to read certificate for expiry metric")
			ch <- prometheus.MustNewConstMetric(c.valid, prometheus.GaugeValue, 0, tracked.name)
			continue
		}

		ch <- prometheus.MustNewConstMetric(c.expiry, prometheus.GaugeValue, float64(cert.NotAfter.Unix()), tracked.name)
		ch <- prometheus.MustNewConstMetric(c.valid, prometheus.GaugeValue, boolToFloat(now.After(cert.NotBefore) && now.Before(cert.NotAfter)), tracked.name)
	}
}

// parseCertificateFile reads a PEM file and returns its leaf certificate. Any
// non-CERTIFICATE blocks are skipped so that a bundle beginning with a comment
// or key block still parses.
func parseCertificateFile(path string) (*x509.Certificate, error) {
	pemBytes, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	for rest := pemBytes; len(rest) > 0; {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		return x509.ParseCertificate(block.Bytes)
	}

	return nil, fmt.Errorf("no CERTIFICATE block found in %s", path)
}
