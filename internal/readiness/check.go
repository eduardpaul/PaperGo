// Package readiness implements the standalone readiness probe.
package readiness

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// Check probes the optional readiness URL, defaulting to the local server.
func Check(args []string) error {
	address := "http://127.0.0.1:8080/health/ready"
	if len(args) == 1 {
		address = args[0]
	} else if len(args) != 0 {
		return errors.New("--health accepts one readiness URL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return err
	}
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("readiness status %d", response.StatusCode)
	}
	return nil
}
