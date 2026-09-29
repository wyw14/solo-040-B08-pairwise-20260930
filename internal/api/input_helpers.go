package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

func chiURLParam(r *http.Request, name string) string { return chi.URLParam(r, name) }

func parseUUID(value string) (uuid.UUID, error) {
	if value == "" {
		return uuid.Nil, errors.New("uuid value is required")
	}
	return uuid.Parse(value)
}

func durationSeconds(value int) time.Duration {
	if value <= 0 {
		value = 30
	}
	if value > 3600 {
		value = 3600
	}
	return time.Duration(value) * time.Second
}
