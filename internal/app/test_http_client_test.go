package app

import (
	"net/http"
	"time"
)

// appTestHTTPClient returns a deadline-bound client with extra room for race-instrumented workspace setup.
func appTestHTTPClient() http.Client {
	return appTestHTTPClientWithTimeout(time.Second)
}

// appTestHTTPClientWithTimeout preserves a test's normal deadline while scaling it under the race detector.
func appTestHTTPClientWithTimeout(base time.Duration) http.Client {
	return http.Client{Timeout: appTestHTTPTimeout(base)}
}

// appTestHTTPTimeout keeps ordinary app HTTP tests fast while making -race runs tolerant of migration overhead.
func appTestHTTPTimeout(base time.Duration) time.Duration {
	if appTestRaceDetectorEnabled && base < 10*time.Second {
		return 10 * time.Second
	}
	return base
}
