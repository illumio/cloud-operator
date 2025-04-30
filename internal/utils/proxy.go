package utils

import (
	"net/http"
	"net/url"
	"os"

	"go.uber.org/zap"
)

func ConfigureProxy(logger *zap.Logger) func(*http.Request) (*url.URL, error) {
	httpsProxy := os.Getenv("HTTPS_PROXY")
	if httpsProxy != "" {
		proxyURL, err := url.Parse(httpsProxy)
		if err != nil {
			logger.Warn("Invalid HTTPS_PROXY value; disabling proxy", zap.Error(err))
			return nil
		}
		return http.ProxyURL(proxyURL)
	}
	return nil // Disable proxy if not set
}
