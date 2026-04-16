package compat

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"flamingo.me/flamingo/v3/framework/web"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
)

func TestCompat(t *testing.T) {
	r := chi.NewRouter()
	r.Get("/hello", func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusTeapot)
	})

	for _, x := range r.Routes() {
		fmt.Println(x)
		for k, v := range x.Handlers {
			fmt.Println(k, v)
		}
	}

	router := web.NewRouter()

	handler := router.Handler()
	assert.NotNil(t, handler)

	registry := web.NewRegistry()

	_, err := registry.Route("/test", "test")
	assert.NoError(t, err)
	registry.HandleAny("test", func(context.Context, *web.Request) web.Result { return nil })
}
