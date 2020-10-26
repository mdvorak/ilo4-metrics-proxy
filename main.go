package main

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
	"github.com/namsral/flag"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"
	"golang.org/x/net/publicsuffix"
	"gopkg.in/fsnotify.v1"
	"ilo4-metrics-proxy/pkg/ilo4"
	"io"
	"io/ioutil"
	"net/http"
	"net/http/cookiejar"
	"os"
	"time"
)

func newZapLogger(production bool) (*zap.Logger, error) {
	if production {
		return zap.NewProduction()
	} else {
		return zap.NewDevelopment()
	}
}

func main() {
	var err error

	// Get configuration
	var production bool
	flag.BoolVar(&production, "production", false, "production logging format")

	var listen string
	flag.StringVar(&listen, "listen", ":2112", "address and port server should listen to")

	var url string
	flag.StringVar(&url, "ilo-url", "", "iLO server base URL, e.g. https://ilo.example.com")

	var certificatePath string
	flag.StringVar(&certificatePath, "ilo-certificate-path", "", "path to a iLO server certificate, in PEM format")

	var credentialsPath string
	flag.StringVar(&credentialsPath, "ilo-credentials-path", "", "path to a valid JSON with server credentials")

	flag.Parse()

	// Logger
	zapLog, err := newZapLogger(production)
	if err != nil {
		panic(err)
	}
	log := zapr.NewLogger(zapLog)

	// Validate flags
	if url == "" {
		panic(fmt.Errorf("ilo-url not set"))
	}
	if credentialsPath == "" {
		panic(fmt.Errorf("ilo-credentials-path not set"))
	}

	// HTTP
	httpClient, err := newHttpClient(log, certificatePath)
	if err != nil {
		panic(err)
	}

	// Client
	log.Info("initializing iLO4 client", "url", url)
	iloClient := &ilo4.Client{
		Log:    log.WithName("ilo4-client"),
		Client: httpClient,
		Url:    url,
		CredentialsProvider: func() (io.Reader, error) {
			log.Info("reading credentials", "path", credentialsPath)
			return os.Open(credentialsPath)
		},
		LoginCounts: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace:   "ilo",
			Subsystem:   "proxy",
			Name:        "logins_total",
			Help:        "Number of logins, proxy had to do to authenticate session against iLO server",
			ConstLabels: map[string]string{"target": url},
		}),
	}

	// Watch certificates
	err = watchCertificateChanges(log, certificatePath, iloClient)
	if err != nil {
		log.Error(err, "failed to setup filesystem watcher, certificate updates won't be available")
	} else {
		log.Info("watching certificate for changes", "path", certificatePath)
	}

	// Metrics
	prometheus.MustRegister(ilo4.NewTemperatureMetrics(iloClient))

	// Start
	http.HandleFunc("/health", healthHandler)
	http.Handle("/metrics", promhttp.Handler())

	log.Info("listening on " + listen)
	if err := http.ListenAndServe(listen, nil); err != nil {
		panic(err)
	}
}

func healthHandler(writer http.ResponseWriter, _ *http.Request) {
	_, _ = writer.Write([]byte(time.Now().String()))
}

func newHttpClient(log logr.Logger, certificatePath string) (*http.Client, error) {
	// TLS
	tlsConfig := &tls.Config{}

	if certificatePath != "" {
		log.Info("reading certificate", "path", certificatePath)
		serverCert, err := ioutil.ReadFile(certificatePath)
		if err != nil {
			return nil, err
		}

		certs := x509.NewCertPool()
		certs.AppendCertsFromPEM(serverCert)

		tlsConfig.RootCAs = certs
	}

	// Cookies are needed for session
	jar, err := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
	if err != nil {
		return nil, err
	}

	// Create client
	client := &http.Client{
		Transport: &http.Transport{TLSClientConfig: tlsConfig},
		Jar:       jar,
	}

	// Success
	return client, nil
}

func watchCertificateChanges(log logr.Logger, certificatePath string, iloClient *ilo4.Client) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}

	// Async certificate updates
	go func() {
		for {
			select {
			case _ = <-watcher.Events:
				log.Info("server certificate changed")
				if httpClient, err := newHttpClient(log, certificatePath); err != nil {
					log.Error(err, "failed to replace http client with new certificate")
				} else {
					// Replace client with new certificate
					iloClient.Client = httpClient
				}

			case err := <-watcher.Errors:
				log.Error(err, "filesystem watcher failed")
			}
		}
	}()

	// Watch certificate
	return watcher.Add(certificatePath)
}
