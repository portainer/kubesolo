package network

import (
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/portainer/kubesolo/types"
	"github.com/rs/zerolog/log"
)

// IsComponentHealthy checks if a component is healthy by sending a health check request
// and waiting for a response.
func IsComponentHealthy(client *http.Client, request *http.Request, component string) error {
	for range types.DefaultRetryCount {
		resp, err := client.Do(request)
		if err != nil {
			log.Warn().Str("component", component).Msgf("component health check failed: %v", err)
			time.Sleep(2 * time.Second)
			continue
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("failed to read health check response: %v", err)
		}

		if resp.StatusCode == http.StatusOK {
			log.Debug().Str("component", component).Msg("component health check passed")
			return nil
		}

		log.Warn().Str("component", component).Msgf("component health check failed: status=%d, body=%s", resp.StatusCode, string(body))
		time.Sleep(2 * time.Second)
	}

	return fmt.Errorf("component health check failed after multiple attempts")
}
