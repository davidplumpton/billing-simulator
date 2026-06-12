package app

import (
	"errors"
	"net/http"
)

func exportHTTPStatus(err error) int {
	var accessErr exportAccessError
	if errors.As(err, &accessErr) {
		return http.StatusForbidden
	}
	return http.StatusBadRequest
}

func exportGenerationHTTPStatus(err error) int {
	var accessErr exportAccessError
	if errors.As(err, &accessErr) {
		return http.StatusForbidden
	}
	var storageErr exportStorageError
	if errors.As(err, &storageErr) {
		return http.StatusInternalServerError
	}
	return http.StatusBadRequest
}
