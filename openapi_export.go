//go:build openapi

package main

import (
	"denkit-stash/handlers"
	"os"

	"github.com/danielgtaylor/huma/v2/adapters/humamux"
	"github.com/gorilla/mux"
)

func main() {
	router := mux.NewRouter()
	api := humamux.New(router, newAPIConfig())
	registerDenKitAPI(api, nil, handlers.NewCoreHandlers(nil), handlers.NewWharfHandlers(nil, nil, ""))

	data, err := api.OpenAPI().YAML()
	if err != nil {
		panic(err)
	}
	if err := os.WriteFile("docs/openapi.yaml", data, 0644); err != nil {
		panic(err)
	}
}
