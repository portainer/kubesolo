package service

import "fmt"

// RunServiceWithStartupCheck is a utility function that runs a service with proper startup coordination.
// It takes a startup function that should return an error if the service fails to start.
// The function will return an error if the service fails to start, or nil if it starts successfully.
func RunServiceWithStartupCheck(startupFunc func() error) error {
	status := make(chan error, 1)

	go func() {
		defer func() {
			if err := recover(); err != nil {
				status <- fmt.Errorf("service panic: %v", err)
			}
		}()

		if err := startupFunc(); err != nil {
			status <- err
			return
		}
		status <- nil
	}()

	return <-status
}
